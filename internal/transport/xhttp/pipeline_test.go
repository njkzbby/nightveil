package xhttp

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"github.com/njkzbby/nightveil/internal/transport"
	"golang.org/x/crypto/curve25519"
)

// pipelineOpts configures a full-stack test/benchmark setup.
type pipelineOpts struct {
	// Latency is the one-way network delay added between the xhttp client
	// and the xhttp server. RTT = 2 * Latency. Zero means no delay (loopback).
	Latency time.Duration

	// UploadMode is "packet", "stream", or "auto" (defaults to packet for
	// repeatable benchmarks since auto-detect depends on Transport type).
	UploadMode string

	// MaxChunkSize for the xhttp transport. Defaults to 14336 (production
	// default under TSPU threshold).
	MaxChunkSize int

	// EchoTarget, if true, makes the harness install an echo handler on
	// the upstream "destination" — every byte the test writes through the
	// tunnel comes back out. Useful for round-trip benchmarks. If false,
	// the test reads/writes directly on the xhttp transport conn without
	// dialing through to a destination.
	EchoTarget bool
}

// pipeline is a fully-wired nightveil stack for tests and benchmarks.
//
// Layout (when EchoTarget = false):
//
//	[test code]
//	    │ Read/Write on .ClientConn
//	    ▼
//	[xhttp client]  ←TCP→  [netsim delay proxy]  ←TCP→  [xhttp server]
//	                                                          │
//	                                                          ▼
//	                                              [test code drains via .Server.Accept]
//
// When EchoTarget = true the harness also stands up a TCP echo server and
// runs an Accept loop on .Server that proxies bytes between the tunnel and
// that echo server. Tests can then write to .ClientConn and read the same
// bytes back, measuring full round-trip throughput.
type pipeline struct {
	Server     *Server
	ClientConn transport.Conn
	Proxy      *netsimProxy

	// ServerReceived tracks bytes that the server-side drainer has actually
	// read out of the tunnel. Used by transferUpload to measure end-to-end
	// upload time correctly even in stream mode where client.Write returns
	// before the bytes leave the local io.Pipe.
	ServerReceived atomicCounter

	cleanup     []func()
	cleanupOnce sync.Once
}

// atomicCounter is a tiny atomic int64 wrapper without pulling in another
// dep. Avoids using sync/atomic in the test public surface.
type atomicCounter struct {
	v int64
	m sync.Mutex
}

func (c *atomicCounter) Add(n int64) {
	c.m.Lock()
	c.v += n
	c.m.Unlock()
}
func (c *atomicCounter) Load() int64 {
	c.m.Lock()
	defer c.m.Unlock()
	return c.v
}

func (p *pipeline) Close() {
	p.cleanupOnce.Do(func() {
		// Run cleanups in reverse order so the client closes before the server.
		for i := len(p.cleanup) - 1; i >= 0; i-- {
			p.cleanup[i]()
		}
	})
}

// setupPipeline wires the entire stack and returns it ready to use.
// All resources are cleaned up via t.Cleanup automatically.
func setupPipeline(t testing.TB, opts pipelineOpts) *pipeline {
	t.Helper()

	if opts.MaxChunkSize == 0 {
		opts.MaxChunkSize = 14336
	}
	if opts.UploadMode == "" {
		opts.UploadMode = "packet"
	}

	// 1. Crypto pair
	var srvPriv [32]byte
	rand.Read(srvPriv[:])
	srvPubBytes, _ := curve25519.X25519(srvPriv[:], curve25519.Basepoint)
	var srvPub [32]byte
	copy(srvPub[:], srvPubBytes)

	userPriv, userPub, err := auth.GenerateUserKeypair()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	serverAuth := &auth.ServerX25519{
		PrivateKey: srvPriv,
		Users: map[string]*auth.UserEntry{
			"aabb": {PublicKey: userPub, ShortID: "aabb"},
		},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}
	clientAuth := &auth.ClientX25519{
		ServerPublicKey: srvPub,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         []byte{0xAA, 0xBB},
	}

	// 2. Nightveil xhttp server fronted by httptest. SessionTimeout is
	// short on purpose so packet-mode tests don't have to wait minutes
	// for the session manager to clean up after the test ends.
	cfg := Config{
		MaxChunkSize:   opts.MaxChunkSize,
		SessionTimeout: 2,
		UploadMode:     opts.UploadMode,
	}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	})
	srv := NewServer(cfg, serverAuth, fallback)
	ts := httptest.NewServer(srv)
	// Force close all server-side connections immediately on cleanup —
	// otherwise ts.Close waits for active GET streams to drain (the
	// download stream is long-lived) which hangs the test.
	ts.Config.SetKeepAlivesEnabled(false)

	p := &pipeline{Server: srv}
	p.cleanup = append(p.cleanup, ts.CloseClientConnections)
	p.cleanup = append(p.cleanup, ts.Close)
	p.cleanup = append(p.cleanup, func() { srv.Close() })
	p.cleanup = append(p.cleanup, func() { ts.Client().CloseIdleConnections() })

	// 3. Netsim delay proxy in front of the httptest server
	tcpAddr := ts.Listener.Addr().String()
	proxy := startNetsimProxy(t, tcpAddr, opts.Latency)
	p.Proxy = proxy
	p.cleanup = append(p.cleanup, proxy.Close)

	// 4. Build a server URL pointing at the proxy instead of the httptest
	// server directly. The proxy forwards to the same backend.
	proxyURL := strings.Replace(ts.URL, tcpAddr, proxy.Addr().String(), 1)

	// 5. xhttp client. Use the same http.Client as ts.Client() so cookies
	// and TLS settings are aligned, but the URL we hand it points at the
	// proxy so all traffic goes through the delay layer.
	xhttpClient := NewClient(proxyURL, cfg, clientAuth, ts.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	p.cleanup = append(p.cleanup, cancel)

	var sid [16]byte
	rand.Read(sid[:])
	conn, err := xhttpClient.Dial(ctx, sid)
	if err != nil {
		p.Close()
		t.Fatalf("dial pipeline: %v", err)
	}
	p.ClientConn = conn
	p.cleanup = append(p.cleanup, func() { conn.Close() })

	// 6. Optional echo destination + server-side relay loop
	if opts.EchoTarget {
		// Spawn server-side accept→echo loop. Each accepted xhttp tunnel conn
		// gets a goroutine that copies bytes back to itself: write returns to
		// the test as a read on the same ClientConn.
		go func() {
			for {
				tunnelConn, err := srv.Accept(ctx)
				if err != nil {
					return
				}
				go func(c transport.Conn) {
					defer c.Close()
					buf := make([]byte, 64*1024)
					for {
						n, err := c.Read(buf)
						if n > 0 {
							if _, werr := c.Write(buf[:n]); werr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}(tunnelConn)
			}
		}()
	} else {
		// Drain the accept channel so sessions can fully connect, and count
		// every byte so transferUpload can wait for end-to-end delivery.
		go func() {
			for {
				c, err := srv.Accept(ctx)
				if err != nil {
					return
				}
				go func(tc transport.Conn) {
					defer tc.Close()
					buf := make([]byte, 64*1024)
					for {
						n, err := tc.Read(buf)
						if n > 0 {
							p.ServerReceived.Add(int64(n))
						}
						if err != nil {
							return
						}
					}
				}(c)
			}
		}()
	}

	t.Cleanup(p.Close)
	return p
}

// transferUpload sends the payload through the tunnel and times it
// end-to-end: from the start of Write until the server-side drainer has
// counted the same number of bytes. Returns elapsed time and Mbps.
//
// This is the correct measurement for both modes:
//   - In packet-up mode, Write blocks until all 8 in-flight POSTs return
//     200, so the server has actually received the data when Write returns.
//     The byte counter just confirms it.
//   - In stream-up mode, Write returns as soon as bytes are queued in the
//     local io.Pipe. Without the counter we'd vastly under-measure latency
//     (~1 ms for 1 MB which is obviously wrong).
//
// EchoTarget must be false (the default).
func transferUpload(t testing.TB, p *pipeline, payload []byte) (time.Duration, float64) {
	t.Helper()
	start := time.Now()

	// Write in a goroutine so the wait loop below can race the byte counter.
	wErr := make(chan error, 1)
	go func() {
		_, err := p.ClientConn.Write(payload)
		wErr <- err
	}()

	deadline := time.Now().Add(2 * time.Minute)
	for {
		if p.ServerReceived.Load() >= int64(len(payload)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("transferUpload timeout: server received %d / %d bytes",
				p.ServerReceived.Load(), len(payload))
		}
		time.Sleep(1 * time.Millisecond)
	}
	elapsed := time.Since(start)

	// Drain the writer goroutine. In stream mode it's already returned;
	// in packet mode the wait above guarantees it.
	select {
	case err := <-wErr:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("writer goroutine did not exit")
	}

	mbps := float64(len(payload)*8) / elapsed.Seconds() / 1_000_000
	return elapsed, mbps
}

// transferEcho writes payload, reads the same number of bytes back, and
// times the round trip. Requires EchoTarget=true.
func transferEcho(t testing.TB, p *pipeline, payload []byte) (time.Duration, float64) {
	t.Helper()
	echo := make([]byte, len(payload))

	start := time.Now()

	// Writer goroutine
	var werr error
	wgDone := make(chan struct{})
	go func() {
		defer close(wgDone)
		_, werr = p.ClientConn.Write(payload)
	}()

	// Read echo back
	if _, err := io.ReadFull(p.ClientConn, echo); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	<-wgDone
	if werr != nil {
		t.Fatalf("write echo: %v", werr)
	}
	elapsed := time.Since(start)

	// Total bytes moved = 2 * len(payload) (up + down)
	mbps := float64(len(payload)*2*8) / elapsed.Seconds() / 1_000_000
	return elapsed, mbps
}

// mustResolveTCPAddr panics-on-test-fail wrapper.
func mustResolveTCPAddr(t testing.TB, s string) net.Addr {
	t.Helper()
	a, err := net.ResolveTCPAddr("tcp", s)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return a
}
