// Package jitter adds random delays to Read/Write operations
// to defeat cross-layer RTT fingerprinting (NDSS 2025 attack).
//
// Without jitter: TCP RTT = 20ms, TLS RTT = 120ms → difference exposes proxy
// With jitter: TCP RTT = 20-80ms (random), TLS RTT = 120-180ms → difference is noise
package jitter

import (
	"crypto/rand"
	"math/big"
	"net"
	"time"

	"github.com/njkzbby/nightveil/internal/transport"
)

// Config for the jitter middleware.
type Config struct {
	MinMs int // minimum delay in milliseconds
	MaxMs int // maximum delay in milliseconds
	// WriteOnly: if true, only add jitter to writes (less latency impact)
	WriteOnly bool
}

// Middleware implements random delay injection.
type Middleware struct {
	config Config
}

func New(cfg Config) *Middleware {
	if cfg.MinMs < 0 {
		cfg.MinMs = 0
	}
	if cfg.MaxMs < cfg.MinMs {
		cfg.MaxMs = cfg.MinMs
	}
	return &Middleware{config: cfg}
}

func (m *Middleware) Wrap(conn transport.Conn) transport.Conn {
	return &jitterConn{Conn: conn, config: m.config}
}

type jitterConn struct {
	transport.Conn
	config Config
}

func (c *jitterConn) Write(p []byte) (int, error) {
	c.delay()
	return c.Conn.Write(p)
}

func (c *jitterConn) Read(p []byte) (int, error) {
	if !c.config.WriteOnly {
		c.delay()
	}
	return c.Conn.Read(p)
}

func (c *jitterConn) delay() {
	if c.config.MaxMs <= 0 {
		return
	}
	ms := c.config.MinMs
	if c.config.MaxMs > c.config.MinMs {
		diff := c.config.MaxMs - c.config.MinMs
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(diff+1)))
		ms += int(n.Int64())
	}
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

func (c *jitterConn) LocalAddr() net.Addr  { return c.Conn.LocalAddr() }
func (c *jitterConn) RemoteAddr() net.Addr { return c.Conn.RemoteAddr() }
