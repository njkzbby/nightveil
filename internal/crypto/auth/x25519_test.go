package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/curve25519"
)

func generateTestKeypair(t *testing.T) ([32]byte, [32]byte) {
	t.Helper()
	var priv [32]byte
	rand.Read(priv[:])
	pub, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
	var pubKey [32]byte
	copy(pubKey[:], pub)
	return priv, pubKey
}

func setupTestAuth(t *testing.T) (*ClientX25519, *ServerX25519, []byte) {
	t.Helper()
	serverPriv, serverPub := generateTestKeypair(t)
	userPriv, userPub := generateTestKeypair(t)
	shortID := []byte{0x03, 0x93, 0x2b, 0x8e}

	client := &ClientX25519{
		ServerPublicKey: serverPub,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         shortID,
	}

	server := &ServerX25519{
		PrivateKey: serverPriv,
		Users: map[string]*UserEntry{
			"03932b8e": {PublicKey: userPub, ShortID: "03932b8e", Name: "test"},
		},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	return client, server, shortID
}

func TestRoundTrip(t *testing.T) {
	client, server, _ := setupTestAuth(t)

	var sessionID [16]byte
	rand.Read(sessionID[:])

	token, err := client.GenerateToken(sessionID)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	tokenB64 := base64.RawURLEncoding.EncodeToString(token)
	req := httptest.NewRequest("POST", "/upload", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: tokenB64})

	gotID, err := server.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if gotID != sessionID {
		t.Fatalf("session ID mismatch")
	}
}

func TestWrongServerKey(t *testing.T) {
	client, _, _ := setupTestAuth(t)

	wrongPriv, _ := generateTestKeypair(t)
	server := &ServerX25519{
		PrivateKey: wrongPriv,
		Users: map[string]*UserEntry{
			"03932b8e": {PublicKey: client.UserPublicKey, ShortID: "03932b8e"},
		},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	var sessionID [16]byte
	rand.Read(sessionID[:])
	token, _ := client.GenerateToken(sessionID)

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatalf("expected ErrAuthFailed, got %v", err)
	}
}

func TestWrongUserKey(t *testing.T) {
	_, server, _ := setupTestAuth(t)

	// Different user keypair
	_, serverPub := generateTestKeypair(t)
	attackerPriv, attackerPub := generateTestKeypair(t)

	attacker := &ClientX25519{
		ServerPublicKey: serverPub,
		UserPrivateKey:  attackerPriv,
		UserPublicKey:   attackerPub,
		ShortID:         []byte{0x03, 0x93, 0x2b, 0x8e}, // same shortID!
	}

	var sessionID [16]byte
	rand.Read(sessionID[:])
	token, _ := attacker.GenerateToken(sessionID)

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatal("expected ErrAuthFailed for wrong user key")
	}
}

func TestUnknownShortID(t *testing.T) {
	client, server, _ := setupTestAuth(t)
	client.ShortID = []byte{0xFF, 0xFF} // not registered

	var sessionID [16]byte
	rand.Read(sessionID[:])
	token, _ := client.GenerateToken(sessionID)

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatal("expected ErrAuthFailed for unknown shortID")
	}
}

func TestNoToken(t *testing.T) {
	_, server, _ := setupTestAuth(t)
	req := httptest.NewRequest("GET", "/", nil)
	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatal("expected ErrAuthFailed")
	}
}

func TestGarbageToken(t *testing.T) {
	_, server, _ := setupTestAuth(t)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: "dGhpcyBpcyBnYXJiYWdl"})
	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatal("expected ErrAuthFailed")
	}
}

func TestGenerateUserKeypair(t *testing.T) {
	priv, pub, err := GenerateUserKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if priv == [32]byte{} || pub == [32]byte{} {
		t.Fatal("empty keypair")
	}
	// Verify pub derives from priv
	derived, _ := DerivePublicKey(priv)
	if derived != pub {
		t.Fatal("pub doesn't match derived from priv")
	}
}

func TestDecodeKey(t *testing.T) {
	var original [32]byte
	rand.Read(original[:])
	encoded := base64.RawStdEncoding.EncodeToString(original[:])
	decoded, err := DecodeKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Fatal("mismatch")
	}
}

func TestDecodeKeyWrongLength(t *testing.T) {
	_, err := DecodeKey(base64.RawStdEncoding.EncodeToString([]byte("short")))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMultipleUsers(t *testing.T) {
	serverPriv, serverPub := generateTestKeypair(t)

	// Create 3 users
	users := make(map[string]*UserEntry)
	clients := make([]*ClientX25519, 3)

	for i := 0; i < 3; i++ {
		userPriv, userPub := generateTestKeypair(t)
		sid := []byte{byte(i + 1)}
		sidHex := fmt.Sprintf("%02x", i+1)

		users[sidHex] = &UserEntry{PublicKey: userPub, ShortID: sidHex, Name: fmt.Sprintf("user%d", i)}
		clients[i] = &ClientX25519{
			ServerPublicKey: serverPub,
			UserPrivateKey:  userPriv,
			UserPublicKey:   userPub,
			ShortID:         sid,
		}
	}

	server := &ServerX25519{
		PrivateKey:  serverPriv,
		Users:       users,
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	// Each user should authenticate independently
	for i, client := range clients {
		var sessionID [16]byte
		rand.Read(sessionID[:])

		token, err := client.GenerateToken(sessionID)
		if err != nil {
			t.Fatalf("user %d generate: %v", i, err)
		}

		req := httptest.NewRequest("POST", "/", nil)
		req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

		gotID, err := server.Validate(context.Background(), req)
		if err != nil {
			t.Fatalf("user %d validate: %v", i, err)
		}
		if gotID != sessionID {
			t.Fatalf("user %d session mismatch", i)
		}
	}
}

func TestConcurrent(t *testing.T) {
	client, server, _ := setupTestAuth(t)

	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		go func() {
			var sid [16]byte
			rand.Read(sid[:])
			token, err := client.GenerateToken(sid)
			if err != nil {
				errs <- err
				return
			}
			req := httptest.NewRequest("POST", "/", nil)
			req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})
			gotSID, err := server.Validate(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			if gotSID != sid {
				errs <- fmt.Errorf("mismatch")
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < 50; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
