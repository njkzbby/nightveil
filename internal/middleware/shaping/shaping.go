// Package shaping implements traffic shaping profiles that make tunnel traffic
// resemble real browsing/streaming/idle patterns.
//
// Without shaping: constant bidirectional stream → obvious VPN tunnel
// With shaping: bursty downloads with pauses → looks like real web browsing
//
// Profiles:
//   - browsing: bursty reads (page loads), small writes (clicks), pauses (reading)
//   - streaming: steady high download, minimal upload, occasional buffering pauses
//   - idle: minimal traffic, periodic keepalive-like bursts
//   - auto: switches between profiles based on traffic pattern detection
package shaping

import (
	"crypto/rand"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/njkzbby/nightveil/internal/transport"
)

// Profile defines a traffic shaping behavior.
type Profile string

const (
	ProfileBrowsing  Profile = "browsing"
	ProfileStreaming  Profile = "streaming"
	ProfileIdle      Profile = "idle"
	ProfileAuto      Profile = "auto"
	ProfileNone      Profile = "none" // passthrough, no shaping
)

// Config for the shaping middleware.
type Config struct {
	Profile        Profile
	// Browsing profile params
	BurstMinBytes  int           // min bytes per burst (default 4096)
	BurstMaxBytes  int           // max bytes per burst (default 65536)
	PauseMinMs     int           // min pause between bursts (default 100)
	PauseMaxMs     int           // max pause between bursts (default 2000)
	// Streaming profile params
	RateBytesPerSec int          // target streaming rate (default 500KB/s)
	// Auto-detection
	AutoDetectWindow time.Duration // window for traffic pattern analysis (default 5s)
}

func (c *Config) defaults() {
	if c.Profile == "" {
		c.Profile = ProfileNone
	}
	if c.BurstMinBytes <= 0 {
		c.BurstMinBytes = 4096
	}
	if c.BurstMaxBytes <= c.BurstMinBytes {
		c.BurstMaxBytes = 65536
	}
	if c.PauseMinMs <= 0 {
		c.PauseMinMs = 100
	}
	if c.PauseMaxMs <= c.PauseMinMs {
		c.PauseMaxMs = 2000
	}
	if c.RateBytesPerSec <= 0 {
		c.RateBytesPerSec = 500 * 1024 // 500KB/s
	}
	if c.AutoDetectWindow <= 0 {
		c.AutoDetectWindow = 5 * time.Second
	}
}

// Middleware shapes traffic according to the configured profile.
type Middleware struct {
	config Config
}

// New creates a traffic shaping middleware.
func New(cfg Config) *Middleware {
	cfg.defaults()
	return &Middleware{config: cfg}
}

// Wrap applies traffic shaping to the connection.
func (m *Middleware) Wrap(conn transport.Conn) transport.Conn {
	if m.config.Profile == ProfileNone {
		return conn // passthrough
	}
	return &shapingConn{
		Conn:    conn,
		config:  m.config,
		profile: m.config.Profile,
	}
}

type shapingConn struct {
	transport.Conn
	config  Config
	profile Profile

	// Auto-detection state
	mu            sync.Mutex
	totalRead     atomic.Int64
	totalWritten  atomic.Int64
	lastBurst     time.Time
	burstBytes    int
}

// Write applies write-side shaping (upload pacing).
func (c *shapingConn) Write(p []byte) (int, error) {
	c.totalWritten.Add(int64(len(p)))

	profile := c.getProfile()
	switch profile {
	case ProfileBrowsing:
		// Small uploads — no delay, just track
		return c.Conn.Write(p)
	case ProfileStreaming:
		// Minimal upload — occasional small writes
		return c.Conn.Write(p)
	case ProfileIdle:
		// Add random delay before write to simulate idle keepalive
		delay := randRange(500, 2000)
		time.Sleep(time.Duration(delay) * time.Millisecond)
		return c.Conn.Write(p)
	default:
		return c.Conn.Write(p)
	}
}

// Read applies read-side shaping (download pacing).
func (c *shapingConn) Read(p []byte) (int, error) {
	profile := c.getProfile()
	switch profile {
	case ProfileBrowsing:
		return c.readBrowsing(p)
	case ProfileStreaming:
		return c.readStreaming(p)
	case ProfileIdle:
		return c.readIdle(p)
	default:
		return c.Conn.Read(p)
	}
}

// readBrowsing simulates page-load bursts with reading pauses.
func (c *shapingConn) readBrowsing(p []byte) (int, error) {
	c.mu.Lock()
	now := time.Now()

	// Check if we're in a burst or need to pause
	if c.burstBytes >= c.config.BurstMaxBytes {
		// Burst complete — pause (simulates user reading)
		pause := randRange(c.config.PauseMinMs, c.config.PauseMaxMs)
		c.burstBytes = 0
		c.lastBurst = now
		c.mu.Unlock()

		time.Sleep(time.Duration(pause) * time.Millisecond)

		c.mu.Lock()
	}
	c.mu.Unlock()

	n, err := c.Conn.Read(p)
	if n > 0 {
		c.mu.Lock()
		c.burstBytes += n
		c.mu.Unlock()
		c.totalRead.Add(int64(n))
	}
	return n, err
}

// readStreaming applies rate limiting to simulate video buffering.
func (c *shapingConn) readStreaming(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.totalRead.Add(int64(n))

		// Rate limit: sleep proportional to bytes read
		sleepMs := float64(n) / float64(c.config.RateBytesPerSec) * 1000
		if sleepMs > 1 {
			time.Sleep(time.Duration(sleepMs) * time.Millisecond)
		}
	}
	return n, err
}

// readIdle adds long pauses between reads.
func (c *shapingConn) readIdle(p []byte) (int, error) {
	delay := randRange(1000, 5000)
	time.Sleep(time.Duration(delay) * time.Millisecond)
	return c.Conn.Read(p)
}

// getProfile returns the active profile (supports auto-switching).
func (c *shapingConn) getProfile() Profile {
	if c.profile != ProfileAuto {
		return c.profile
	}

	// Auto-detect based on traffic ratio
	read := c.totalRead.Load()
	written := c.totalWritten.Load()
	total := read + written

	if total < 1024 {
		return ProfileIdle // not enough data yet
	}

	readRatio := float64(read) / float64(total)

	if readRatio > 0.9 {
		return ProfileStreaming // mostly download
	}
	if readRatio > 0.6 {
		return ProfileBrowsing // mix of download and upload
	}
	return ProfileBrowsing // default
}

func (c *shapingConn) LocalAddr() net.Addr  { return c.Conn.LocalAddr() }
func (c *shapingConn) RemoteAddr() net.Addr { return c.Conn.RemoteAddr() }

func randRange(min, max int) int {
	if max <= min {
		return min
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return min + int(n.Int64())
}
