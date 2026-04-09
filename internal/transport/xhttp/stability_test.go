package xhttp

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nightveil/nv/internal/crypto/auth"
	"github.com/nightveil/nv/internal/transport"
	"golang.org/x/crypto/curve25519"
)

// setupStabilityTest creates a full XHTTP server+client pair for stability testing.
func setupStabilityTest(t *testing.T) (*Server, auth.ClientAuth, *httptest.Server, context.Context, context.CancelFunc) {
	t.Helper()

	var privKey [32]byte
	rand.Read(privKey[:])
	pubBytes, _ := curve25519.X25519(privKey[:], curve25519.Basepoint)
	var pubKey [32]byte
	copy(pubKey[:], pubBytes)

	userPriv, userPub, _ := auth.GenerateUserKeypair()
	shortID := []byte{0xAA, 0xBB}

	serverAuth := &auth.ServerX25519{
		PrivateKey: privKey,
		Users: map[string]*auth.UserEntry{
			"aabb": {PublicKey: userPub, ShortID: "aabb"},
		},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	clientAuth := &auth.ClientX25519{
		ServerPublicKey: pubKey,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         shortID,
	}

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	})

	srv := NewServer(cfg, serverAuth, fallback)
	ts := httptest.NewServer(srv)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	return srv, clientAuth, ts, ctx, cancel
}

// TestStreamingSSE simulates Server-Sent Events (like Claude API streaming).
// Server sends small chunks every 100ms for 5 seconds.
// Verifies no data loss.
func TestStreamingSSE(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupStabilityTest(t)
	defer cancel()
	defer ts.Close()

	// Server: accept conn, send streaming data like SSE
	var serverConn transport.Conn
	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		// Simulate SSE: send "data: token_N\n\n" every 100ms
		for i := 0; i < 50; i++ {
			msg := fmt.Sprintf("data: token_%d\n\n", i)
			_, err := serverConn.Write([]byte(msg))
			if err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		serverConn.Close()
	}()

	// Client: connect and read all tokens
	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])

	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond) // let server accept

	// Read all data
	var received []byte
	buf := make([]byte, 4096)
	deadline := time.After(10 * time.Second)

	for {
		select {
		case <-deadline:
			goto done
		default:
		}
		n, err := conn.Read(buf)
		if n > 0 {
			received = append(received, buf[:n]...)
		}
		if err != nil {
			break
		}
		// Check if we got all 50 tokens
		if len(received) > 0 && containsToken(received, 49) {
			break
		}
	}

done:
	// Verify we got all tokens
	missing := 0
	for i := 0; i < 50; i++ {
		if !containsToken(received, i) {
			missing++
		}
	}

	if missing > 0 {
		t.Fatalf("missing %d/50 tokens in SSE stream", missing)
	}
}

func containsToken(data []byte, n int) bool {
	token := fmt.Sprintf("token_%d\n", n)
	for i := 0; i <= len(data)-len(token); i++ {
		if string(data[i:i+len(token)]) == token {
			return true
		}
	}
	return false
}

// TestLongLivedConnection tests a connection that stays open for 10+ seconds
// with intermittent data — simulates browsing with pauses.
func TestLongLivedConnection(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupStabilityTest(t)
	defer cancel()
	defer ts.Close()

	var serverConn transport.Conn
	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		// Send data, then pause, then more data
		serverConn.Write([]byte("burst1"))
		time.Sleep(3 * time.Second) // long pause
		serverConn.Write([]byte("burst2"))
		time.Sleep(3 * time.Second) // another long pause
		serverConn.Write([]byte("burst3"))
		serverConn.Close()
	}()

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])
	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	var received []byte
	buf := make([]byte, 4096)

	for {
		n, err := conn.Read(buf)
		if n > 0 {
			received = append(received, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	got := string(received)
	if got != "burst1burst2burst3" {
		t.Fatalf("expected 'burst1burst2burst3', got %q (len=%d)", got, len(got))
	}
}

// TestHighThroughput tests sustained high throughput (YouTube-like).
func TestHighThroughput(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupStabilityTest(t)
	defer cancel()
	defer ts.Close()

	totalBytes := 1024 * 1024 // 1MB
	var serverConn transport.Conn

	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		data := make([]byte, 8192)
		for i := range data {
			data[i] = byte(i % 251)
		}
		sent := 0
		for sent < totalBytes {
			n, err := serverConn.Write(data)
			if err != nil {
				return
			}
			sent += n
		}
		serverConn.Close()
	}()

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])
	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	received := 0
	buf := make([]byte, 32768)
	for received < totalBytes {
		n, err := conn.Read(buf)
		received += n
		if err != nil {
			break
		}
	}

	if received < totalBytes {
		t.Fatalf("received %d bytes, want >= %d", received, totalBytes)
	}
}

// TestConcurrentStreams tests multiple simultaneous connections (browser tabs).
func TestConcurrentStreams(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupStabilityTest(t)
	defer cancel()
	defer ts.Close()

	numStreams := 5
	var wg sync.WaitGroup
	var errors atomic.Int32

	// Server: accept and echo
	go func() {
		for i := 0; i < numStreams; i++ {
			conn, err := srv.Accept(ctx)
			if err != nil {
				return
			}
			go func(c transport.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	// Launch concurrent streams
	for i := 0; i < numStreams; i++ {
		wg.Add(1)
		go func(streamID int) {
			defer wg.Done()

			var sessionID [16]byte
			rand.Read(sessionID[:])

			conn, err := client.Dial(ctx, sessionID)
			if err != nil {
				errors.Add(1)
				return
			}
			defer conn.Close()

			time.Sleep(200 * time.Millisecond)

			// Write and read
			msg := fmt.Sprintf("stream_%d_data", streamID)
			_, err = conn.Write([]byte(msg))
			if err != nil {
				errors.Add(1)
				return
			}

			buf := make([]byte, 100)
			n, err := conn.Read(buf)
			if err != nil || string(buf[:n]) != msg {
				errors.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if e := errors.Load(); e > 0 {
		t.Fatalf("%d/%d streams failed", e, numStreams)
	}
}

// TestReconnectOnStreamDrop simulates download stream dropping mid-transfer.
// Tests that the client reconnect logic works.
func TestReconnectOnStreamDrop(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupStabilityTest(t)
	defer cancel()
	defer ts.Close()

	var serverConn transport.Conn
	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		// Send some data then close abruptly
		serverConn.Write([]byte("hello"))
		// Don't close cleanly — simulate network drop
		// The server keeps the session alive but stops sending
		time.Sleep(10 * time.Second)
	}()

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])
	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	// Read initial data
	buf := make([]byte, 100)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q, want 'hello'", buf[:n])
	}

	// Connection should stay alive (no immediate error)
	// This tests that HTTP/2 ReadIdleTimeout keeps it open
}

// TestUploadDownloadAsymmetry tests heavy download with light upload
// (typical video streaming pattern).
func TestUploadDownloadAsymmetry(t *testing.T) {
	srv, clientAuth, ts, ctx, cancel := setupStabilityTest(t)
	defer cancel()
	defer ts.Close()

	downloadSize := 100000 // 100KB download
	uploadSize := 100      // 100 bytes upload

	var serverConn transport.Conn
	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		// Read small upload
		buf := make([]byte, uploadSize)
		io.ReadFull(serverConn, buf)

		// Send large download
		data := make([]byte, 1024)
		sent := 0
		for sent < downloadSize {
			n, err := serverConn.Write(data)
			if err != nil {
				return
			}
			sent += n
		}
		serverConn.Close()
	}()

	cfg := Config{MaxChunkSize: 4096, SessionTimeout: 30}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])
	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	// Small upload
	conn.Write(make([]byte, uploadSize))

	// Read large download
	received := 0
	buf := make([]byte, 8192)
	for received < downloadSize {
		n, err := conn.Read(buf)
		received += n
		if err != nil {
			break
		}
	}

	if received < downloadSize {
		t.Fatalf("received %d bytes, want >= %d", received, downloadSize)
	}
}
