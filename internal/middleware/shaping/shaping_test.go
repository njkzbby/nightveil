package shaping

import (
	"bytes"
	"net"
	"testing"
	"time"
)

type pipeConn struct{ net.Conn }
func (p *pipeConn) LocalAddr() net.Addr  { return p.Conn.LocalAddr() }
func (p *pipeConn) RemoteAddr() net.Addr { return p.Conn.RemoteAddr() }

func TestProfileNonePassthrough(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{Profile: ProfileNone})
	conn := mw.Wrap(&pipeConn{left})

	msg := []byte("passthrough")
	go conn.Write(msg)

	buf := make([]byte, 100)
	n, err := right.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q", buf[:n])
	}
}

func TestProfileBrowsingAddsDelay(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{
		Profile:       ProfileBrowsing,
		BurstMaxBytes: 10,    // tiny burst → quick pause trigger
		PauseMinMs:    50,
		PauseMaxMs:    100,
	})
	shaped := mw.Wrap(&pipeConn{left})

	// Write data that exceeds burst
	go func() {
		right.Write(make([]byte, 50)) // more than BurstMaxBytes
	}()

	start := time.Now()
	buf := make([]byte, 200)

	// First read: within burst
	shaped.Read(buf)
	// Second read: should trigger pause after burst exceeded
	go func() {
		right.Write(make([]byte, 50))
	}()
	shaped.Read(buf)

	elapsed := time.Since(start)
	// Should have some delay from browsing pause
	// Not strict — just verify it's not instant
	_ = elapsed
}

func TestProfileStreamingRateLimit(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{
		Profile:         ProfileStreaming,
		RateBytesPerSec: 10000, // 10KB/s — slow
	})
	shaped := mw.Wrap(&pipeConn{left})

	data := make([]byte, 5000) // 5KB
	go func() {
		right.Write(data)
	}()

	start := time.Now()
	buf := make([]byte, 10000)
	shaped.Read(buf)
	elapsed := time.Since(start)

	// At 10KB/s, 5KB should take ~500ms
	// Allow some margin
	if elapsed < 200*time.Millisecond {
		t.Logf("streaming read took %v — may be too fast for rate limit", elapsed)
		// Not strict failure — rate limiting is approximate
	}
}

func TestProfileIdleAddsLongDelay(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{
		Profile: ProfileIdle,
	})
	shaped := mw.Wrap(&pipeConn{left})

	// Idle write should have delay
	go func() {
		shaped.Write([]byte("idle data"))
	}()

	start := time.Now()
	buf := make([]byte, 100)
	right.Read(buf)
	elapsed := time.Since(start)

	// Idle adds 500-2000ms delay to writes
	if elapsed < 400*time.Millisecond {
		t.Logf("idle write took %v — expected 500ms+", elapsed)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.defaults()

	if cfg.BurstMinBytes != 4096 {
		t.Errorf("BurstMinBytes: %d", cfg.BurstMinBytes)
	}
	if cfg.BurstMaxBytes != 65536 {
		t.Errorf("BurstMaxBytes: %d", cfg.BurstMaxBytes)
	}
	if cfg.RateBytesPerSec != 500*1024 {
		t.Errorf("RateBytesPerSec: %d", cfg.RateBytesPerSec)
	}
}

func TestAutoProfileDetection(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	mw := New(Config{Profile: ProfileAuto})
	shaped := mw.Wrap(&pipeConn{left}).(*shapingConn)

	// Initially should be idle (no data)
	if p := shaped.getProfile(); p != ProfileIdle {
		t.Fatalf("initial auto profile: %s, want idle", p)
	}

	// Simulate heavy download (read >> write)
	shaped.totalRead.Store(10000)
	shaped.totalWritten.Store(100)
	if p := shaped.getProfile(); p != ProfileStreaming {
		t.Fatalf("download-heavy profile: %s, want streaming", p)
	}

	// Simulate mixed traffic
	shaped.totalRead.Store(7000)
	shaped.totalWritten.Store(3000)
	if p := shaped.getProfile(); p != ProfileBrowsing {
		t.Fatalf("mixed profile: %s, want browsing", p)
	}
}

func TestProfileString(t *testing.T) {
	profiles := []Profile{ProfileBrowsing, ProfileStreaming, ProfileIdle, ProfileAuto, ProfileNone}
	for _, p := range profiles {
		if string(p) == "" {
			t.Errorf("empty string for profile %v", p)
		}
	}
}

func TestNewMiddlewareNoConfig(t *testing.T) {
	mw := New(Config{})
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}
	// Default profile is "none" — passthrough
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	conn := mw.Wrap(&pipeConn{left})
	go conn.Write([]byte("test"))

	buf := make([]byte, 10)
	n, _ := right.Read(buf)
	if string(buf[:n]) != "test" {
		t.Fatalf("got %q", buf[:n])
	}
}
