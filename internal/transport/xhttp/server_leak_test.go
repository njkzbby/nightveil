package xhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"github.com/njkzbby/nightveil/internal/session"
	"golang.org/x/crypto/curve25519"
)

// makeAuthPair creates a matching server/client auth pair for tests that
// need to push real authenticated requests through the http handler.
func makeAuthPair(t *testing.T) (*auth.ServerX25519, *auth.ClientX25519) {
	t.Helper()
	var srvPriv [32]byte
	rand.Read(srvPriv[:])
	srvPubBytes, _ := curve25519.X25519(srvPriv[:], curve25519.Basepoint)
	var srvPub [32]byte
	copy(srvPub[:], srvPubBytes)

	userPriv, userPub, err := auth.GenerateUserKeypair()
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	srv := &auth.ServerX25519{
		PrivateKey: srvPriv,
		Users: map[string]*auth.UserEntry{
			"aabb": {PublicKey: userPub, ShortID: "aabb"},
		},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}
	cli := &auth.ClientX25519{
		ServerPublicKey: srvPub,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         []byte{0xAA, 0xBB},
	}
	return srv, cli
}

// TestServerNoMapLeakAfter1kSessions verifies that after many short-lived
// sessions cycle through the server, no per-session state is retained
// beyond the session's own GC. Previously the accepted/hasUpload/hasDownload
// maps grew unbounded.
func TestServerNoMapLeakAfter1kSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("session churn test skipped in -short")
	}

	serverAuth, clientAuth := makeAuthPair(t)
	cfg := Config{
		MaxChunkSize:   4096,
		SessionTimeout: 1, // very short TTL so unconnected sessions die fast
	}
	srv := NewServer(cfg, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Drain Accept channel so the server doesn't backpressure.
	go func() {
		for {
			conn, err := srv.Accept(ctx)
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	const N = 500
	for i := 0; i < N; i++ {
		var sid [16]byte
		rand.Read(sid[:])
		token, err := clientAuth.GenerateToken(sid)
		if err != nil {
			t.Fatalf("token: %v", err)
		}

		// Make one POST upload chunk so a session is created.
		req, _ := http.NewRequest("POST",
			fmt.Sprintf("%s%s?seq=0&sid=%s", ts.URL, cfg.fullUploadPath(), base64.RawURLEncoding.EncodeToString(sid[:])),
			bytes.NewReader([]byte("ping")),
		)
		req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	// All sessions are POST-only (no GET) → they never become "connected",
	// so the manager's TTL cleanup loop will eventually evict them.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if srv.sessions.Count() == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if c := srv.sessions.Count(); c != 0 {
		t.Fatalf("session manager still holds %d sessions after TTL cleanup (want 0)", c)
	}

	g := runtime.NumGoroutine()
	if g > 200 {
		t.Logf("warning: %d goroutines after %d sessions (smell)", g, N)
	}
}

// TestSessionFlagsAtomicCAS verifies the new per-session atomic flag
// semantics: only the first CAS wins, subsequent ones are no-ops.
func TestSessionFlagsAtomicCAS(t *testing.T) {
	s := session.NewSession([16]byte{1})
	defer s.Close()

	if s.Accepted.Load() {
		t.Fatal("Accepted should start false")
	}
	if !s.Accepted.CompareAndSwap(false, true) {
		t.Fatal("first CAS should succeed")
	}
	if s.Accepted.CompareAndSwap(false, true) {
		t.Fatal("second CAS should fail")
	}
	if !s.Accepted.Load() {
		t.Fatal("after CAS, value should be true")
	}

	s.HasUpload.Store(true)
	s.HasDownload.Store(true)
	if !s.HasUpload.Load() || !s.HasDownload.Load() {
		t.Fatal("flags not stored")
	}
}

// TestTryEmitOnceOnly drives many concurrent tryEmit calls and verifies the
// splitConn appears on the accept channel exactly once.
func TestTryEmitOnceOnly(t *testing.T) {
	serverAuth, _ := makeAuthPair(t)
	srv := NewServer(Config{}, serverAuth, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	sess := srv.sessions.GetOrCreate([16]byte{7})
	sess.HasDownload.Store(true)

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			srv.tryEmit(sess, "test")
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}

	count := 0
drain:
	for {
		select {
		case <-srv.acceptCh:
			count++
		default:
			break drain
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 emission, got %d", count)
	}
}
