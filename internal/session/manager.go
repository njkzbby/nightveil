// Package session manages tunnel sessions with TTL-based cleanup.
package session

import (
	"sync"
	"time"
)

// Session holds state for a single tunnel session.
type Session struct {
	ID        [16]byte
	CreatedAt time.Time

	// Upload reassembly: ordered queue of POST chunks
	uploadMu   sync.Mutex
	uploadBuf  map[int64][]byte // seq → data
	nextSeq    int64
	uploadCond *sync.Cond
	uploadDone bool

	// Download: buffered — survives GET stream reconnects without data loss
	DownloadBuf *DownloadBuffer

	// Signals
	connected chan struct{} // closed when both upload+download registered
	connOnce  sync.Once
	closed    chan struct{}
	closeOnce sync.Once
}

// NewSession creates a new session.
func NewSession(id [16]byte) *Session {
	s := &Session{
		ID:          id,
		CreatedAt:   time.Now(),
		uploadBuf:   make(map[int64][]byte),
		connected:   make(chan struct{}),
		closed:      make(chan struct{}),
		DownloadBuf: NewDownloadBuffer(),
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
// Returns nil, false when the session is closed.
func (s *Session) ReadUpload() ([]byte, bool) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	for {
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

// Close terminates the session.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		s.uploadMu.Lock()
		s.uploadDone = true
		s.uploadCond.Broadcast()
		s.uploadMu.Unlock()
		s.DownloadBuf.Close()
	})
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
	s := NewSession(id)
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
var ErrSessionClosed = &sessionError{"session closed"}

type sessionError struct{ msg string }

func (e *sessionError) Error() string { return e.msg }
