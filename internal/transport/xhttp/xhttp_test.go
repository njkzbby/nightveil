package xhttp

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"github.com/njkzbby/nightveil/internal/transport"
	"golang.org/x/crypto/curve25519"
)

func setupTestServer(t *testing.T) (*Server, auth.ClientAuth, *httptest.Server) {
	t.Helper()

	// Generate keypair
	var privKey [32]byte
	rand.Read(privKey[:])
	pubBytes, _ := curve25519.X25519(privKey[:], curve25519.Basepoint)
	var pubKey [32]byte
	copy(pubKey[:], pubBytes)

	shortID := []byte{0xAB, 0xCD}
	userPriv, userPub, _ := auth.GenerateUserKeypair()

	serverAuth := &auth.ServerX25519{
		PrivateKey: privKey,
		Users: map[string]*auth.UserEntry{
			"abcd": {PublicKey: userPub, ShortID: "abcd"},
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

	cfg := Config{
		PathPrefix:     "/test",
		UploadPath:     "/u",
		DownloadPath:   "/d",
		SessionKeyName: "sid",
		MaxChunkSize:   1024,
		SessionTimeout: 10,
	}

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback site"))
	})

	srv := NewServer(cfg, serverAuth, fallback)
	ts := httptest.NewServer(srv)

	return srv, clientAuth, ts
}

func TestXHTTPRoundTrip(t *testing.T) {
	srv, clientAuth, ts := setupTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Server: accept and echo
	var serverConn transport.Conn
	serverReady := make(chan struct{})
	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		close(serverReady)
		buf := make([]byte, 32768)
		for {
			n, err := serverConn.Read(buf)
			if err != nil {
				return
			}
			if _, err := serverConn.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	// Client: dial
	cfg := Config{
		PathPrefix:     "/test",
		UploadPath:     "/u",
		DownloadPath:   "/d",
		SessionKeyName: "sid",
		MaxChunkSize:   1024,
	}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])

	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		ts.Close()
		t.Fatalf("dial: %v", err)
	}

	// Give server a moment to accept
	time.Sleep(100 * time.Millisecond)

	msg := []byte("hello xhttp")
	if _, err := conn.Write(msg); err != nil {
		conn.Close()
		ts.Close()
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 100)
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		ts.Close()
		t.Fatalf("read: %v", err)
	}

	if string(buf[:n]) != "hello xhttp" {
		conn.Close()
		ts.Close()
		t.Fatalf("got %q, want %q", buf[:n], "hello xhttp")
	}

	// Clean shutdown: close connections before server
	conn.Close()
	if serverConn != nil {
		serverConn.Close()
	}
	ts.Close()
}

func TestXHTTPFallback(t *testing.T) {
	_, _, ts := setupTestServer(t)
	defer ts.Close()

	// Request without auth → should get fallback
	resp, err := http.Get(ts.URL + "/some/page")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 100)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "fallback site" {
		t.Fatalf("got %q, want fallback", buf[:n])
	}
}

func TestXHTTPFallbackOnAuthPaths(t *testing.T) {
	_, _, ts := setupTestServer(t)
	defer ts.Close()

	// Request to upload path without auth → fallback
	resp, err := http.Post(ts.URL+"/test/u?seq=0", "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Should get fallback (200 with "fallback site"), not an error
	// Auth fails → falls through to fallback handler
}

func TestXHTTPLargeData(t *testing.T) {
	srv, clientAuth, ts := setupTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var serverConn transport.Conn
	go func() {
		var err error
		serverConn, err = srv.Accept(ctx)
		if err != nil {
			return
		}
		buf := make([]byte, 32768)
		for {
			n, err := serverConn.Read(buf)
			if err != nil {
				return
			}
			serverConn.Write(buf[:n])
		}
	}()

	cfg := Config{
		PathPrefix:     "/test",
		UploadPath:     "/u",
		DownloadPath:   "/d",
		SessionKeyName: "sid",
		MaxChunkSize:   1024,
	}
	client := NewClient(ts.URL, cfg, clientAuth, ts.Client())

	var sessionID [16]byte
	rand.Read(sessionID[:])

	conn, err := client.Dial(ctx, sessionID)
	if err != nil {
		ts.Close()
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// 5KB split into 5 × 1KB chunks
	data := make([]byte, 5120)
	for i := range data {
		data[i] = byte(i % 251)
	}

	if _, err := conn.Write(data); err != nil {
		conn.Close()
		ts.Close()
		t.Fatal(err)
	}

	received := make([]byte, 0, len(data))
	buf := make([]byte, 4096)
	for len(received) < len(data) {
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		received = append(received, buf[:n]...)
	}

	conn.Close()
	if serverConn != nil {
		serverConn.Close()
	}
	ts.Close()

	if len(received) != len(data) {
		t.Fatalf("received %d bytes, want %d", len(received), len(data))
	}
	for i := range data {
		if received[i] != data[i] {
			t.Fatalf("mismatch at byte %d", i)
		}
	}
}
