package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
)

func generateTestKeypair(t *testing.T) ([32]byte, [32]byte) {
	t.Helper()
	var priv [32]byte
	rand.Read(priv[:])
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	var pubKey [32]byte
	copy(pubKey[:], pub)
	return priv, pubKey
}

func TestRoundTrip(t *testing.T) {
	privKey, pubKey := generateTestKeypair(t)
	shortID := []byte{0x03, 0x93, 0x2b, 0x8e}

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         shortID,
	}

	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    map[string]bool{"03932b8e": true},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	// Generate a session ID
	var sessionID [16]byte
	rand.Read(sessionID[:])

	// Client generates token
	token, err := client.GenerateToken(sessionID)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	// Create HTTP request with token in cookie
	tokenB64 := base64.RawURLEncoding.EncodeToString(token)
	req := httptest.NewRequest("POST", "/upload", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: tokenB64})

	// Server validates
	gotID, err := server.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if gotID != sessionID {
		t.Fatalf("session ID mismatch: got %x, want %x", gotID, sessionID)
	}
}

func TestRoundTripViaHeader(t *testing.T) {
	privKey, pubKey := generateTestKeypair(t)

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         []byte{0xAB},
	}

	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    map[string]bool{"ab": true},
		MaxTimeDiff: 120,
		TokenHeader: "X-Auth",
	}

	var sessionID [16]byte
	rand.Read(sessionID[:])

	token, err := client.GenerateToken(sessionID)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/stream", nil)
	req.Header.Set("X-Auth", base64.RawURLEncoding.EncodeToString(token))

	gotID, err := server.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("Validate via header: %v", err)
	}
	if gotID != sessionID {
		t.Fatal("session ID mismatch")
	}
}

func TestEmptyShortID(t *testing.T) {
	privKey, pubKey := generateTestKeypair(t)

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         nil, // empty
	}

	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    nil, // accept any
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	var sessionID [16]byte
	rand.Read(sessionID[:])

	token, _ := client.GenerateToken(sessionID)
	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

	gotID, err := server.Validate(context.Background(), req)
	if err != nil {
		t.Fatalf("Validate with empty shortID: %v", err)
	}
	if gotID != sessionID {
		t.Fatal("session ID mismatch")
	}
}

func TestWrongServerKey(t *testing.T) {
	_, pubKey := generateTestKeypair(t)
	wrongPriv, _ := generateTestKeypair(t) // different keypair

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         []byte{0x01},
	}

	server := &ServerX25519{
		PrivateKey:  wrongPriv, // WRONG key
		ShortIDs:    map[string]bool{"01": true},
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

func TestWrongShortID(t *testing.T) {
	privKey, pubKey := generateTestKeypair(t)

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         []byte{0x01},
	}

	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    map[string]bool{"ff": true}, // different shortID
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
		t.Fatalf("expected ErrAuthFailed for wrong shortID, got %v", err)
	}
}

func TestExpiredTimestamp(t *testing.T) {
	privKey, pubKey := generateTestKeypair(t)
	shortID := []byte{0x01}

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         shortID,
	}

	// Generate token
	var sessionID [16]byte
	rand.Read(sessionID[:])
	token, _ := client.GenerateToken(sessionID)

	// Tamper the timestamp: set it 200s in the past
	shortIDLen := int(token[32])
	tsOffset := 33 + shortIDLen
	oldTs := time.Now().Unix() - 200
	binary.BigEndian.PutUint64(token[tsOffset:tsOffset+8], uint64(oldTs))

	// This will fail because tampering breaks the AEAD (timestamp is in the salt)
	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    map[string]bool{"01": true},
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	req := httptest.NewRequest("POST", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatalf("expected ErrAuthFailed for tampered timestamp, got %v", err)
	}
}

func TestNoToken(t *testing.T) {
	privKey, _ := generateTestKeypair(t)

	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    nil,
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	req := httptest.NewRequest("GET", "/", nil)
	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatalf("expected ErrAuthFailed for missing token, got %v", err)
	}
}

func TestGarbageToken(t *testing.T) {
	privKey, _ := generateTestKeypair(t)

	server := &ServerX25519{
		PrivateKey:  privKey,
		ShortIDs:    nil,
		MaxTimeDiff: 120,
		TokenHeader: "nv_token",
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "nv_token", Value: "dGhpcyBpcyBnYXJiYWdl"})

	_, err := server.Validate(context.Background(), req)
	if err != ErrAuthFailed {
		t.Fatalf("expected ErrAuthFailed for garbage token, got %v", err)
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
		t.Fatal("key mismatch after decode")
	}
}

func TestDecodeKeyWrongLength(t *testing.T) {
	short := base64.RawStdEncoding.EncodeToString([]byte("too short"))
	_, err := DecodeKey(short)
	if err == nil {
		t.Fatal("expected error for wrong-length key")
	}
}

func BenchmarkGenerateToken(b *testing.B) {
	_, pubKey := func() ([32]byte, [32]byte) {
		var priv [32]byte
		rand.Read(priv[:])
		pub, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
		var pubKey [32]byte
		copy(pubKey[:], pub)
		return priv, pubKey
	}()

	client := &ClientX25519{
		ServerPublicKey: pubKey,
		ShortID:         []byte{0x01, 0x02, 0x03, 0x04},
	}
	var sid [16]byte

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := client.GenerateToken(sid)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidate(b *testing.B) {
	privKey, pubKey := func() ([32]byte, [32]byte) {
		var priv [32]byte
		rand.Read(priv[:])
		pub, _ := curve25519.X25519(priv[:], curve25519.Basepoint)
		var pubKey [32]byte
		copy(pubKey[:], pub)
		return priv, pubKey
	}()

	client := &ClientX25519{ServerPublicKey: pubKey, ShortID: []byte{0x01}}
	server := &ServerX25519{
		PrivateKey: privKey, ShortIDs: map[string]bool{"01": true},
		MaxTimeDiff: 120, TokenHeader: "nv_token",
	}

	var sid [16]byte
	tokens := make([]string, 100)
	for i := range tokens {
		token, _ := client.GenerateToken(sid)
		tokens[i] = base64.RawURLEncoding.EncodeToString(token)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("POST", "/", nil)
		req.AddCookie(&http.Cookie{Name: "nv_token", Value: tokens[i%len(tokens)]})
		_, err := server.Validate(context.Background(), req)
		if err != nil {
			b.Fatalf("iteration %d: %v", i, err)
		}
	}
}

func TestMultipleSessionsConcurrent(t *testing.T) {
	privKey, pubKey := generateTestKeypair(t)
	shortID := []byte{0xAA}

	client := &ClientX25519{ServerPublicKey: pubKey, ShortID: shortID}
	server := &ServerX25519{
		PrivateKey: privKey, ShortIDs: map[string]bool{"aa": true},
		MaxTimeDiff: 120, TokenHeader: "nv_token",
	}

	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		go func(i int) {
			var sid [16]byte
			rand.Read(sid[:])

			token, err := client.GenerateToken(sid)
			if err != nil {
				errs <- fmt.Errorf("gen %d: %w", i, err)
				return
			}

			req := httptest.NewRequest("POST", "/", nil)
			req.AddCookie(&http.Cookie{Name: "nv_token", Value: base64.RawURLEncoding.EncodeToString(token)})

			gotSID, err := server.Validate(context.Background(), req)
			if err != nil {
				errs <- fmt.Errorf("validate %d: %w", i, err)
				return
			}
			if gotSID != sid {
				errs <- fmt.Errorf("mismatch %d", i)
				return
			}
			errs <- nil
		}(i)
	}

	for i := 0; i < 50; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}
