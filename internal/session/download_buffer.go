package session

import (
	"sync"
)

// DownloadBuffer accumulates download data and allows multiple GET
// readers to consume from it with offset tracking.
// When a GET stream breaks, the next GET resumes from the last
// successfully delivered offset — zero data loss.
type DownloadBuffer struct {
	mu     sync.Mutex
	buf    []byte
	done   bool
	notify chan struct{} // signaled on every Write/Close
}

func NewDownloadBuffer() *DownloadBuffer {
	return &DownloadBuffer{
		notify: make(chan struct{}, 1),
	}
}

// Write appends data to the buffer.
func (db *DownloadBuffer) Write(p []byte) (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.done {
		return 0, ErrSessionClosed
	}

	cp := make([]byte, len(p))
	copy(cp, p)
	db.buf = append(db.buf, cp...)

	// Non-blocking signal
	select {
	case db.notify <- struct{}{}:
	default:
	}

	return len(p), nil
}

// Close marks the buffer as done.
func (db *DownloadBuffer) Close() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.done = true

	select {
	case db.notify <- struct{}{}:
	default:
	}
}

// ReadFrom copies data starting at offset into p.
// Returns (bytes copied, new offset, error).
// Non-blocking: returns immediately if no new data.
// Caller should select on Notify() channel to wait for data.
func (db *DownloadBuffer) ReadFrom(offset int, p []byte) (int, int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if offset >= len(db.buf) {
		if db.done {
			return 0, offset, ErrSessionClosed
		}
		return 0, offset, nil // no data yet — caller waits on Notify()
	}

	available := db.buf[offset:]
	n := copy(p, available)
	return n, offset + n, nil
}

// Notify returns a channel that receives when new data is available.
// Use in select with context.Done() for cancellable reads.
func (db *DownloadBuffer) Notify() <-chan struct{} {
	return db.notify
}

// Len returns current buffer size.
func (db *DownloadBuffer) Len() int {
	db.mu.Lock()
	defer db.mu.Unlock()
	return len(db.buf)
}

// IsDone returns whether the buffer is closed.
func (db *DownloadBuffer) IsDone() bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.done
}
