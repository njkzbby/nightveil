package xhttp

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// netsim is a tiny userspace network impairment helper for tests and
// benchmarks. It runs an in-process TCP proxy that forwards bytes between
// a client and a backend, adding configurable one-way latency to each
// direction (so RTT = 2 * Latency).
//
// It also supports pause/resume so tests can simulate brief network stalls
// — bytes accumulate in the proxy's queue and are released when resumed,
// reproducing the "stall and burst" pattern users observe in the field.
//
// Why a real TCP proxy and not a net.Conn wrapper? HTTP/2 issues many
// small Read/Write calls per logical operation, so per-call delays produce
// pessimistic, mode-dependent benchmark numbers. A proxy in the middle
// delays bytes-in-flight uniformly regardless of how the client framing
// chooses to call Read/Write, which more closely mirrors a real WAN.

// netsimProxy listens on a local port and forwards every accepted
// connection to backendAddr, applying delay both ways.
type netsimProxy struct {
	listener net.Listener
	backend  string
	delay    time.Duration

	pauseMu sync.Mutex
	paused  atomic.Bool
	resume  chan struct{}

	closed atomic.Bool
	wg     sync.WaitGroup
}

// startNetsimProxy starts a delay proxy in front of backendAddr. Returns
// the proxy listener (use .Addr() to get the host:port for clients).
func startNetsimProxy(t testing.TB, backendAddr string, delay time.Duration) *netsimProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("netsim listen: %v", err)
	}
	p := &netsimProxy{
		listener: ln,
		backend:  backendAddr,
		delay:    delay,
		resume:   make(chan struct{}),
	}
	close(p.resume) // start un-paused
	p.wg.Add(1)
	go p.acceptLoop()
	t.Cleanup(p.Close)
	return p
}

func (p *netsimProxy) Addr() net.Addr { return p.listener.Addr() }

func (p *netsimProxy) Close() {
	if !p.closed.CompareAndSwap(false, true) {
		return
	}
	p.listener.Close()
	p.wg.Wait()
}

// Pause halts forwarding in both directions. Bytes already buffered in the
// proxy's per-direction queue continue to accumulate; nothing is released
// until Resume is called. New connections are still accepted (but their
// data is paused too).
func (p *netsimProxy) Pause() {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	if p.paused.Load() {
		return
	}
	p.resume = make(chan struct{})
	p.paused.Store(true)
}

// Resume releases all paused traffic. Bytes that piled up during the pause
// are flushed in order, producing the user-visible "burst" effect.
func (p *netsimProxy) Resume() {
	p.pauseMu.Lock()
	defer p.pauseMu.Unlock()
	if !p.paused.Load() {
		return
	}
	p.paused.Store(false)
	close(p.resume)
}

// waitUnpaused blocks until the proxy is not paused. Returns true if it had
// to wait. Cheap fast-path when not paused.
func (p *netsimProxy) waitUnpaused() {
	if !p.paused.Load() {
		return
	}
	// Snapshot resume channel under lock to avoid racing with Pause.
	p.pauseMu.Lock()
	ch := p.resume
	paused := p.paused.Load()
	p.pauseMu.Unlock()
	if !paused {
		return
	}
	<-ch
}

func (p *netsimProxy) acceptLoop() {
	defer p.wg.Done()
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		p.wg.Add(1)
		go p.handle(client)
	}
}

func (p *netsimProxy) handle(client net.Conn) {
	defer p.wg.Done()
	defer client.Close()

	backend, err := net.Dial("tcp", p.backend)
	if err != nil {
		return
	}
	defer backend.Close()

	// Two delay pipes (client→backend, backend→client). They run until
	// either side closes; we wait for both via WaitGroup.
	var dwg sync.WaitGroup
	dwg.Add(2)
	go func() {
		defer dwg.Done()
		p.pipeDelayed(client, backend)
		// Closing the destination unblocks the other direction's Read.
		backend.Close()
	}()
	go func() {
		defer dwg.Done()
		p.pipeDelayed(backend, client)
		client.Close()
	}()
	dwg.Wait()
}

// pipeDelayed reads bytes from src, time-stamps each batch, then releases
// them to dst after p.delay has elapsed. Pause is honored on the release
// side, so paused bytes pile up in the in-memory queue.
func (p *netsimProxy) pipeDelayed(src, dst net.Conn) {
	type batch struct {
		data    []byte
		readyAt time.Time
	}
	queue := make(chan batch, 256)

	// Reader: pull from src, push timestamped batches.
	go func() {
		defer close(queue)
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				cp := make([]byte, n)
				copy(cp, buf[:n])
				queue <- batch{data: cp, readyAt: time.Now().Add(p.delay)}
			}
			if err != nil {
				return
			}
		}
	}()

	// Writer: dequeue, wait for readyAt + un-paused, write to dst.
	for b := range queue {
		if rem := time.Until(b.readyAt); rem > 0 {
			time.Sleep(rem)
		}
		p.waitUnpaused()
		if _, err := dst.Write(b.data); err != nil {
			// Drain rest so the reader goroutine can exit cleanly.
			for range queue {
			}
			return
		}
	}
}

// --- assertion helpers ---

// mustWriteAll writes the entire payload via conn and fails the test on
// short writes or errors.
func mustWriteAll(t testing.TB, conn io.Writer, payload []byte) {
	t.Helper()
	n, err := conn.Write(payload)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(payload) {
		t.Fatalf("short write: %d/%d", n, len(payload))
	}
}

// mustReadFull reads exactly len(buf) bytes from conn and fails on short
// reads, errors, or timeout.
func mustReadFull(t testing.TB, conn io.Reader, buf []byte) {
	t.Helper()
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read full: %v", err)
	}
}
