package xhttp

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nightveil/nv/internal/crypto/auth"
	"golang.org/x/crypto/curve25519"
)

func TestServerFallbackOnRoot(t *testing.T) {
	_, _, ts := setupTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServerFallbackOnBadAuth(t *testing.T) {
	_, _, ts := setupTestServer(t)
	defer ts.Close()

	// POST with garbage auth cookie
	req, _ := http.NewRequest("POST", ts.URL+"/test/u?seq=0", strings.NewReader("data"))
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: "garbage"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Should get fallback, not error
	if resp.StatusCode != 200 {
		t.Fatalf("expected fallback 200, got %d", resp.StatusCode)
	}
}

func TestServerFallbackOnNoAuth(t *testing.T) {
	_, _, ts := setupTestServer(t)
	defer ts.Close()

	// GET without any auth
	resp, err := http.Get(ts.URL + "/test/d")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected fallback 200, got %d", resp.StatusCode)
	}
}

func TestServerAuthenticatedUpload(t *testing.T) {
	srv, clientAuth, ts := setupTestServer(t)
	defer ts.Close()
	_ = srv

	var sessionID [16]byte
	rand.Read(sessionID[:])

	token, err := clientAuth.GenerateToken(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	tokenB64 := base64.RawURLEncoding.EncodeToString(token)
	sidB64 := base64.RawURLEncoding.EncodeToString(sessionID[:])

	// POST upload with valid auth
	req, _ := http.NewRequest("POST", ts.URL+"/any/path?seq=0&sid="+sidB64, strings.NewReader("hello"))
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: tokenB64})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 for authenticated upload, got %d", resp.StatusCode)
	}
}

func TestServerMultiplePOSTs(t *testing.T) {
	srv, clientAuth, ts := setupTestServer(t)
	defer ts.Close()
	_ = srv

	var sessionID [16]byte
	rand.Read(sessionID[:])

	token, _ := clientAuth.GenerateToken(sessionID)
	tokenB64 := base64.RawURLEncoding.EncodeToString(token)
	sidB64 := base64.RawURLEncoding.EncodeToString(sessionID[:])

	// Send 5 POSTs with different seq numbers
	for seq := 0; seq < 5; seq++ {
		req, _ := http.NewRequest("POST", ts.URL+"/up?seq="+strings.Repeat("0", seq)+"&"+sidB64+"=x", strings.NewReader("chunk"))
		req.AddCookie(&http.Cookie{Name: "nv_token", Value: tokenB64})

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST seq %d: %v", seq, err)
		}
		resp.Body.Close()
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	cfg.defaults()

	if cfg.PathPrefix == "" {
		t.Error("PathPrefix should have default")
	}
	if cfg.UploadPath == "" {
		t.Error("UploadPath should have default")
	}
	if cfg.DownloadPath == "" {
		t.Error("DownloadPath should have default")
	}
	if cfg.SessionKeyName == "" {
		t.Error("SessionKeyName should have default")
	}
	if cfg.MaxChunkSize <= 0 {
		t.Error("MaxChunkSize should be positive")
	}
	if cfg.MaxChunkSize > 15000 {
		t.Error("MaxChunkSize should be under TSPU threshold")
	}
	if cfg.SessionTimeout <= 0 {
		t.Error("SessionTimeout should be positive")
	}
}

func setupTestServerWithNewKeys(t *testing.T) (*Server, *auth.ClientX25519, *httptest.Server) {
	t.Helper()

	var privKey [32]byte
	rand.Read(privKey[:])
	pubBytes, _ := curve25519.X25519(privKey[:], curve25519.Basepoint)
	var pubKey [32]byte
	copy(pubKey[:], pubBytes)

	shortID := []byte{0x12, 0x34}
	userPriv, userPub, _ := auth.GenerateUserKeypair()

	serverAuth := &auth.ServerX25519{
		PrivateKey: privKey,
		Users: map[string]*auth.UserEntry{
			"1234": {PublicKey: userPub, ShortID: "1234"},
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

	cfg := Config{MaxChunkSize: 1024, SessionTimeout: 5}

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	})

	srv := NewServer(cfg, serverAuth, fallback)
	ts := httptest.NewServer(srv)

	return srv, clientAuth, ts
}
