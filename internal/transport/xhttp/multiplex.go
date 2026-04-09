package xhttp

import (
	"sync"
	"time"

	"github.com/nightveil/nv/internal/throttle"
)

// MultiplexedWriter wraps the client upload to support N parallel POST streams.
// When throttling is detected, it increases parallelism automatically.
type MultiplexedWriter struct {
	conn     *clientConn
	adaptive *throttle.Adaptive

	// Semaphore for parallel uploads
	sem chan struct{}
	mu  sync.Mutex
}

// NewMultiplexedWriter creates a multiplexed upload writer.
func NewMultiplexedWriter(conn *clientConn, adaptive *throttle.Adaptive) *MultiplexedWriter {
	initial := 1
	if adaptive != nil {
		initial = adaptive.ParallelConns()
	}

	mw := &MultiplexedWriter{
		conn:     conn,
		adaptive: adaptive,
		sem:      make(chan struct{}, initial),
	}

	// Fill semaphore
	for i := 0; i < initial; i++ {
		mw.sem <- struct{}{}
	}

	// Watch for parallelism changes
	if adaptive != nil {
		go mw.watchAdaptive()
	}

	return mw
}

// Write sends data as one or more parallel POST chunks.
func (mw *MultiplexedWriter) Write(p []byte) (int, error) {
	maxChunk := mw.conn.client.Config.MaxChunkSize
	written := 0

	for written < len(p) {
		end := written + maxChunk
		if end > len(p) {
			end = len(p)
		}
		chunk := p[written:end]

		// Acquire semaphore slot
		<-mw.sem

		// Post chunk (release semaphore after)
		err := mw.conn.postChunk(chunk)
		mw.sem <- struct{}{}

		if err != nil {
			return written, err
		}
		written = end
	}

	return written, nil
}

// watchAdaptive monitors the adaptive controller and resizes the semaphore.
func (mw *MultiplexedWriter) watchAdaptive() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if mw.adaptive == nil {
			return
		}
		target := mw.adaptive.ParallelConns()
		mw.resize(target)
	}
}

// resize adjusts the semaphore capacity.
func (mw *MultiplexedWriter) resize(target int) {
	mw.mu.Lock()
	defer mw.mu.Unlock()

	current := cap(mw.sem)
	if target == current {
		return
	}

	// Create new semaphore with target capacity
	newSem := make(chan struct{}, target)
	// Fill with min(target, current) tokens
	fill := target
	if fill > current {
		fill = current
	}
	for i := 0; i < fill; i++ {
		newSem <- struct{}{}
	}
	// Add extra tokens if growing
	for i := current; i < target; i++ {
		newSem <- struct{}{}
	}

	mw.sem = newSem
}
