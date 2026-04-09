package proxy

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// CountingRelay copies data bidirectionally and tracks bytes transferred.
// Returns bytes sent (left→right), bytes received (right→left), and error.
type RelayStats struct {
	BytesSent atomic.Int64
	BytesRecv atomic.Int64
	StartTime time.Time
	Duration  time.Duration
}

// ThroughputSend returns average send throughput in bytes/sec.
func (s *RelayStats) ThroughputSend() float64 {
	d := s.Duration.Seconds()
	if d <= 0 {
		return 0
	}
	return float64(s.BytesSent.Load()) / d
}

// ThroughputRecv returns average receive throughput in bytes/sec.
func (s *RelayStats) ThroughputRecv() float64 {
	d := s.Duration.Seconds()
	if d <= 0 {
		return 0
	}
	return float64(s.BytesRecv.Load()) / d
}

// RelayWithStats copies data bidirectionally and returns transfer stats.
func RelayWithStats(left, right io.ReadWriteCloser) *RelayStats {
	stats := &RelayStats{StartTime: time.Now()}

	var once sync.Once
	closeAll := func() {
		left.Close()
		right.Close()
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// left → right (send)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(right, &countingReader{r: left, counter: &stats.BytesSent})
		_ = n
		once.Do(closeAll)
	}()

	// right → left (recv)
	go func() {
		defer wg.Done()
		n, _ := io.Copy(left, &countingReader{r: right, counter: &stats.BytesRecv})
		_ = n
		once.Do(closeAll)
	}()

	wg.Wait()
	stats.Duration = time.Since(stats.StartTime)
	return stats
}

type countingReader struct {
	r       io.Reader
	counter *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.counter.Add(int64(n))
	}
	return n, err
}
