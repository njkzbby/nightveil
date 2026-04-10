// Package session manages tunnel sessions with TTL-based cleanup.
package session

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// Session holds state for a single tunnel session.
type Session struct {
	ID        [16]byte
	CreatedAt time.Time

	// Upload reassembly: ordered queue of POST chunks (packet-up mode).
	// Mutually exclusive with the stream-up pipe below — a session uses one
	// or the other based on the first incoming POST's transport mode.
	uploadMu   sync.Mutex
	uploadBuf  map[int64][]byte // seq → data
	nextSeq    int64
	uploadCond *sync.Cond
	uploadDone bool

	// Stream-up pipe: a single in-process io.Pipe carrying the entire
	// upload stream from the long-lived POST request body to the splitConn
	// reader. Lazily initialized on first stream-mode interaction.
	streamMu   sync.Mutex
	streamPipeR *io.PipeReader
	streamPipeW *io.PipeWriter
	streamMode  atomic.Bool

	// Download: buffered — survives GET stream reconnects without data loss
	DownloadBuf *DownloadBuffer

	// Per-session state flags. Previously these lived in maps on the
	// xhttp.Server struct, but those maps grew unbounded because they were
	// never deleted on session close. Moving them onto Session lets the
	// session's normal GC path free everything together.
	HasUpload   atomic.Bool // POST seen at least once
	HasDownload atomic.Bool // GET seen at least once
	Accepted    atomic.Bool // emitted to Accept channel exactly once

	// Signals
	connected chan struct{} // closed when both upload+download registered
	connOnce  sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

// NewSession creates a new session with the default download replay window.
func NewSession(id [16]byte) *Session {
	return NewSessionWithDownloadBuf(id, DefaultDownloadBufferBytes)
}

// NewSessionWithDownloadBuf creates a new session with a custom download
// replay window size. A zero or negative size falls back to the default.
func NewSessionWithDownloadBuf(id [16]byte, downloadBufferBytes int) *Session {
	s := &Session{
		ID:          id,
		CreatedAt:   time.Now(),
		uploadBuf:   make(map[int64][]byte),
		connected:   make(chan struct{}),
		closed:      make(chan struct{}),
		DownloadBuf: NewDownloadBufferSized(downloadBufferBytes),
	}
	s.uploadCond = sync.NewCond(&s.uploadMu)
	return s
}

// PushUpload adds a chunk to the upload queue at the given sequence number.
func (s *Session) PushUpload(seq int64, data []byte) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	if s.uploadDone {
		return
	}

	cp := make([]byte, len(data))
	copy(cp, data)
	s.uploadBuf[seq] = cp
	s.uploadCond.Signal()
}

// ReadUpload reads the next in-order chunk. Blocks until available.
// Returns (nil, false) when:
//   - the session is closed (uploadDone), or
//   - the session has flipped into stream-up mode (caller should switch
//     to StreamReader instead).
//
// The streamMode check is what makes the lazy mode-switch race-safe: if a
// splitConn reader entered ReadUpload first and is waiting on uploadCond,
// the broadcast in ensureStreamPipe wakes it and this branch returns false
// so the splitConn can re-enter via the stream path.
func (s *Session) ReadUpload() ([]byte, bool) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	for {
		if s.streamMode.Load() {
			return nil, false
		}
		if s.uploadDone {
			return nil, false
		}
		if data, ok := s.uploadBuf[s.nextSeq]; ok {
			delete(s.uploadBuf, s.nextSeq)
			s.nextSeq++
			return data, true
		}
		s.uploadCond.Wait()
	}
}

// MarkConnected signals that both upload and download channels are ready.
func (s *Session) MarkConnected() {
	s.connOnce.Do(func() { close(s.connected) })
}

// WaitConnected blocks until the session is fully connected or closed.
func (s *Session) WaitConnected() bool {
	select {
	case <-s.connected:
		return true
	case <-s.closed:
		return false
	}
}

// Close terminates the session and releases reassembly state.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.uploadMu.Lock()
		s.uploadDone = true
		// Release any out-of-order chunks still pending — they would
		// otherwise live until GC and bloat memory if many sessions
		// die mid-pipeline.
		s.uploadBuf = nil
		s.uploadCond.Broadcast()
		s.uploadMu.Unlock()
		// Tear down the stream-up pipe if it's in use, unblocking both
		// the producer (server's r.Body→pipe copy) and the consumer
		// (splitConn.Read).
		s.streamMu.Lock()
		if s.streamPipeW != nil {
			s.streamPipeW.Close()
		}
		if s.streamPipeR != nil {
			s.streamPipeR.Close()
		}
		s.streamMu.Unlock()
		s.DownloadBuf.Close()
	})
}

// IsStreamMode reports whether the session uses stream-up upload (single
// long-lived POST) instead of packet-up (POST per chunk).
func (s *Session) IsStreamMode() bool {
	return s.streamMode.Load()
}

// ensureStreamPipe lazily creates the stream-up pipe and flips the
// streamMode flag. Safe to call concurrently — both producer (server upload
// handler) and consumer (splitConn reader) may race to initialize.
//
// Also wakes any packet-mode ReadUpload waiters so they can switch over to
// the stream path. Without this broadcast a splitConn.Read that started
// before the stream POST arrived would sit forever in uploadCond.Wait.
func (s *Session) ensureStreamPipe() {
	s.streamMu.Lock()
	created := false
	if s.streamPipeR == nil {
		s.streamPipeR, s.streamPipeW = io.Pipe()
		s.streamMode.Store(true)
		created = true
	}
	s.streamMu.Unlock()

	if created {
		s.uploadMu.Lock()
		s.uploadCond.Broadcast()
		s.uploadMu.Unlock()
	}
}

// StreamUploadWriter returns the io.Writer side of the upload pipe. Used
// by the server's stream-mode upload handler to io.Copy r.Body straight
// into the session.
func (s *Session) StreamUploadWriter() io.Writer {
	s.ensureStreamPipe()
	return s.streamPipeW
}

// StreamReader returns the io.Reader side of the upload pipe. Used by
// splitConn to read decrypted/raw bytes from the long-lived upload stream.
func (s *Session) StreamReader() io.Reader {
	s.ensureStreamPipe()
	return s.streamPipeR
}

// CloseStreamUpload closes the producer side of the pipe, signaling EOF
// to the consumer (splitConn reader). Called when the upload POST request
// body is fully consumed (client closed it).
func (s *Session) CloseStreamUpload() {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	if s.streamPipeW != nil {
		s.streamPipeW.Close()
	}
}

// Connected returns a channel that is closed when the session is connected.
func (s *Session) Connected() <-chan struct{} {
	return s.connected
}

// IsClosed returns true if the session has been closed.
func (s *Session) IsClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// --- Manager ---

// Manager tracks active sessions with automatic TTL cleanup.
type Manager struct {
	mu       sync.Mutex
	sessions map[[16]byte]*Session
	ttl      time.Duration

	// DownloadBufferBytes is applied to every newly created session's download
	// replay window. Zero falls back to DefaultDownloadBufferBytes. Read-only
	// after construction or set immediately after NewManager and before the
	// first GetOrCreate.
	DownloadBufferBytes int
}

// NewManager creates a session manager with the given TTL.
func NewManager(ttl time.Duration) *Manager {
	m := &Manager{
		sessions: make(map[[16]byte]*Session),
		ttl:      ttl,
	}
	go m.cleanupLoop()
	return m
}

// GetOrCreate returns an existing session or creates a new one.
func (m *Manager) GetOrCreate(id [16]byte) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[id]; ok {
		return s
	}
	s := NewSessionWithDownloadBuf(id, m.DownloadBufferBytes)
	m.sessions[id] = s
	return s
}

// Get returns an existing session or nil.
func (m *Manager) Get(id [16]byte) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

// Remove deletes a session.
func (m *Manager) Remove(id [16]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[id]; ok {
		s.Close()
		delete(m.sessions, id)
	}
}

// Count returns the number of active sessions.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(m.ttl / 2)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for id, s := range m.sessions {
			// Remove sessions that are closed or expired (not connected within TTL)
			select {
			case <-s.connected:
				// Connected — only remove if explicitly closed
				if s.IsClosed() {
					delete(m.sessions, id)
				}
			default:
				// Not yet connected — check TTL
				if now.Sub(s.CreatedAt) > m.ttl {
					s.Close()
					delete(m.sessions, id)
				}
			}
		}
		m.mu.Unlock()
	}
}

// Errors
var (
	ErrSessionClosed = &sessionError{"session closed"}
	ErrOffsetTooOld  = &sessionError{"download offset older than replay window"}
)

type sessionError struct{ msg string }

func (e *sessionError) Error() string { return e.msg }
