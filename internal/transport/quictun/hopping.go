package quictun

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// HoppingConfig configures UDP port hopping.
type HoppingConfig struct {
	PortMin  int           // start of port range (e.g. 30000)
	PortMax  int           // end of port range (e.g. 40000)
	Interval time.Duration // hop interval (default 30s)
}

func (c *HoppingConfig) defaults() {
	if c.PortMin <= 0 {
		c.PortMin = 30000
	}
	if c.PortMax <= c.PortMin {
		c.PortMax = c.PortMin + 10000
	}
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
}

// HoppingPacketConn wraps a PacketConn and periodically switches
// the destination port. QUIC's connection ID mechanism means the
// logical connection survives port changes.
type HoppingPacketConn struct {
	inner     net.PacketConn
	config    HoppingConfig
	baseHost  string // server host (without port)
	currentPort atomic.Int32

	mu      sync.RWMutex
	destAddr net.Addr

	stopCh chan struct{}
}

// NewHoppingPacketConn creates a port-hopping wrapper.
// serverHost should be just the host/IP without port.
func NewHoppingPacketConn(inner net.PacketConn, serverHost string, cfg HoppingConfig) *HoppingPacketConn {
	cfg.defaults()

	// Pick initial random port
	initialPort := randomPort(cfg.PortMin, cfg.PortMax)

	h := &HoppingPacketConn{
		inner:    inner,
		config:   cfg,
		baseHost: serverHost,
		stopCh:   make(chan struct{}),
	}
	h.currentPort.Store(int32(initialPort))
	h.updateDestAddr(initialPort)

	go h.hopLoop()

	return h
}

// CurrentPort returns the current destination port.
func (h *HoppingPacketConn) CurrentPort() int {
	return int(h.currentPort.Load())
}

// ReadFrom reads from the underlying connection.
func (h *HoppingPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return h.inner.ReadFrom(p)
}

// WriteTo writes to the current hopped destination port.
func (h *HoppingPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	h.mu.RLock()
	addr := h.destAddr
	h.mu.RUnlock()

	return h.inner.WriteTo(p, addr)
}

func (h *HoppingPacketConn) Close() error {
	close(h.stopCh)
	return h.inner.Close()
}

func (h *HoppingPacketConn) LocalAddr() net.Addr                { return h.inner.LocalAddr() }
func (h *HoppingPacketConn) SetDeadline(t time.Time) error      { return h.inner.SetDeadline(t) }
func (h *HoppingPacketConn) SetReadDeadline(t time.Time) error  { return h.inner.SetReadDeadline(t) }
func (h *HoppingPacketConn) SetWriteDeadline(t time.Time) error { return h.inner.SetWriteDeadline(t) }

func (h *HoppingPacketConn) hopLoop() {
	ticker := time.NewTicker(h.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			newPort := randomPort(h.config.PortMin, h.config.PortMax)
			h.currentPort.Store(int32(newPort))
			h.updateDestAddr(newPort)
			log.Printf("[hopping] switched to port %d", newPort)
		case <-h.stopCh:
			return
		}
	}
}

func (h *HoppingPacketConn) updateDestAddr(port int) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", h.baseHost, port))
	if err != nil {
		return
	}
	h.mu.Lock()
	h.destAddr = addr
	h.mu.Unlock()
}

func randomPort(min, max int) int {
	diff := max - min
	if diff <= 0 {
		return min
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(diff+1)))
	return min + int(n.Int64())
}
