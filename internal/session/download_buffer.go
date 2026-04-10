package session

import (
	"sync"
)

// DefaultDownloadBufferBytes is the default replay window for the download
// sliding buffer. It caps how much already-delivered data is retained for
// reconnect scenarios — once a reader falls further behind than this, its
// ReadFrom call returns ErrOffsetTooOld and the tunnel must be reestablished.
const DefaultDownloadBufferBytes = 4 * 1024 * 1024

// DownloadBuffer is a bounded sliding-window byte buffer that supports
// reconnect-aware reads via absolute offsets.
//
// Semantics:
//
//   - Write appends new bytes; callers on the other side read by absolute
//     offset (bytes since the first ever write on this buffer).
//   - Advance is called by the active GET reader after successfully flushing
//     bytes to the HTTP response. It tells the buffer "you may compact up to
//     here". The compactor keeps at least maxRetain bytes before the
//     delivered watermark as a replay cushion so a reconnecting reader can
//     resume from a slightly earlier offset without data loss.
//   - ReadFrom(offset) at an offset older than the current discard floor
//     returns ErrOffsetTooOld — the bytes are gone, caller must abandon
//     the session.
//
// Old behavior (without any Advance call) is preserved: the buffer grows
// until Close, and absolute offsets remain valid. That lets tests and
// unmigrated callers keep working; only the server hot path calls Advance.
type DownloadBuffer struct {
	mu        sync.Mutex
	buf       []byte
	discarded int           // absolute offset of buf[0]
	delivered int           // absolute offset acknowledged by active reader
	maxRetain int           // replay window (bytes kept past delivered)
	done      bool
	notify    chan struct{} // signaled on every Write/Close
}

// NewDownloadBuffer creates a buffer with the default replay window.
func NewDownloadBuffer() *DownloadBuffer {
	return NewDownloadBufferSized(DefaultDownloadBufferBytes)
}

// NewDownloadBufferSized creates a buffer with a specific replay window.
// A zero or negative size falls back to DefaultDownloadBufferBytes.
func NewDownloadBufferSized(maxRetain int) *DownloadBuffer {
	if maxRetain <= 0 {
		maxRetain = DefaultDownloadBufferBytes
	}
	return &DownloadBuffer{
		maxRetain: maxRetain,
		notify:    make(chan struct{}, 1),
	}
}

// Write appends data to the buffer and may trigger compaction if the buffer
// has grown past 2× the replay window and the reader has advanced.
func (db *DownloadBuffer) Write(p []byte) (int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if db.done {
		return 0, ErrSessionClosed
	}

	cp := make([]byte, len(p))
	copy(cp, p)
	db.buf = append(db.buf, cp...)

	// Opportunistic compaction on write — avoids unbounded growth when a
	// reader is consuming fast enough.
	if len(db.buf) > 2*db.maxRetain {
		db.compactLocked()
	}

	// Non-blocking signal
	select {
	case db.notify <- struct{}{}:
	default:
	}

	return len(p), nil
}

// Advance informs the buffer that bytes up to the given absolute offset
// have been successfully delivered to an active reader. The compactor is
// then free to drop bytes older than (off - maxRetain). Offsets beyond the
// current write position are clamped. Offsets older than the existing
// delivered watermark are ignored.
func (db *DownloadBuffer) Advance(off int) {
	db.mu.Lock()
	defer db.mu.Unlock()

	end := db.discarded + len(db.buf)
	if off > end {
		off = end
	}
	if off <= db.delivered {
		return
	}
	db.delivered = off
	db.compactLocked()
}

// compactLocked drops bytes that are both already delivered and outside the
// replay window. Must be called with db.mu held.
func (db *DownloadBuffer) compactLocked() {
	if db.delivered <= db.discarded {
		return
	}
	// Floor of what we must keep: delivered - maxRetain (the replay window).
	dropTo := db.delivered - db.maxRetain
	if dropTo <= db.discarded {
		return
	}
	drop := dropTo - db.discarded
	if drop <= 0 || drop > len(db.buf) {
		return
	}
	// Shift to a fresh slice to release the underlying array memory when
	// the shifted region is small; otherwise re-slicing keeps the capacity
	// pinned until GC, which defeats the whole point.
	remaining := len(db.buf) - drop
	if remaining == 0 {
		db.buf = nil
	} else if cap(db.buf) > 4*remaining {
		// Over-allocated: force a new backing array.
		newBuf := make([]byte, remaining)
		copy(newBuf, db.buf[drop:])
		db.buf = newBuf
	} else {
		// Small overhang: cheap re-slice.
		db.buf = db.buf[drop:]
	}
	db.discarded = dropTo
}

// Close marks the buffer as done. Existing buffered data remains readable
// until ReadFrom advances past the end, at which point it returns
// ErrSessionClosed. This preserves the legacy "drain after close" semantics
// that the GET handler relies on.
func (db *DownloadBuffer) Close() {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.done = true

	select {
	case db.notify <- struct{}{}:
	default:
	}
}

// ReadFrom copies data starting at the given absolute offset into p.
// Returns (bytes copied, new absolute offset, error).
//
// Non-blocking: returns immediately if no new data is available. Callers
// should select on Notify() to wait for data.
//
// Returns ErrOffsetTooOld if the requested offset is older than the current
// discard floor — the bytes are gone and the session cannot resume.
// Returns ErrSessionClosed if the buffer is closed and offset is at or past
// the end.
func (db *DownloadBuffer) ReadFrom(offset int, p []byte) (int, int, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if offset < db.discarded {
		return 0, offset, ErrOffsetTooOld
	}

	local := offset - db.discarded
	if local >= len(db.buf) {
		if db.done {
			return 0, offset, ErrSessionClosed
		}
		return 0, offset, nil // no data yet — caller waits on Notify()
	}

	available := db.buf[local:]
	n := copy(p, available)
	return n, offset + n, nil
}

// Notify returns a channel that receives when new data is available.
// Use in select with context.Done() for cancellable reads.
func (db *DownloadBuffer) Notify() <-chan struct{} {
	return db.notify
}

// Len returns the current physical size of the buffer (bytes retained in
// memory, NOT the absolute end offset). Useful for tests and metrics.
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
