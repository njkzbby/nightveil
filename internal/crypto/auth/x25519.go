package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

// --- Client ---

// ClientX25519 implements ClientAuth using X25519 ECDH + HKDF + ChaCha20-Poly1305.
type ClientX25519 struct {
	ServerPublicKey [32]byte
	ShortID         []byte // 0-8 bytes
}

func (c *ClientX25519) GenerateToken(sessionID [16]byte) ([]byte, error) {
	// 1. Ephemeral X25519 keypair
	var ephPriv [32]byte
	if _, err := rand.Read(ephPriv[:]); err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}

	ephPub, err := curve25519.X25519(ephPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive ephemeral public: %w", err)
	}

	// 2. ECDH shared secret
	shared, err := curve25519.X25519(ephPriv[:], c.ServerPublicKey[:])
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}

	// 3. Timestamp (8 bytes, big-endian, Unix seconds)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(time.Now().Unix()))

	// 4. Derive keys via HKDF
	authKey, err := deriveKey(shared, c.ShortID, ts[:], "nv-auth")
	if err != nil {
		return nil, err
	}

	// 5. AEAD seal: encrypt sessionID
	aead, err := chacha20poly1305.New(authKey)
	if err != nil {
		return nil, fmt.Errorf("create aead: %w", err)
	}
	nonce := make([]byte, aead.NonceSize()) // zero nonce — single-use key
	ciphertext := aead.Seal(nil, nonce, sessionID[:], nil)

	// 6. Assemble token: ephPub || shortIDLen || shortID || timestamp || ciphertext
	tokenLen := 32 + 1 + len(c.ShortID) + 8 + len(ciphertext)
	token := make([]byte, 0, tokenLen)
	token = append(token, ephPub...)
	token = append(token, byte(len(c.ShortID)))
	token = append(token, c.ShortID...)
	token = append(token, ts[:]...)
	token = append(token, ciphertext...)

	return token, nil
}

// --- Server ---

// ServerX25519 implements ServerAuth using X25519 ECDH + HKDF + ChaCha20-Poly1305.
type ServerX25519 struct {
	PrivateKey  [32]byte
	ShortIDs    map[string]bool // allowed shortIDs
	MaxTimeDiff int64           // seconds
	TokenHeader string          // HTTP header or cookie name containing the token
}

func (s *ServerX25519) Validate(ctx context.Context, r *http.Request) (sessionID [16]byte, err error) {
	// Extract token from cookie or header
	tokenB64 := extractToken(r, s.TokenHeader)
	if tokenB64 == "" {
		return sessionID, ErrAuthFailed
	}

	tokenBytes, err := base64.RawURLEncoding.DecodeString(tokenB64)
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	// Parse token: ephPub(32) || shortIDLen(1) || shortID(0-8) || timestamp(8) || ciphertext(32)
	if len(tokenBytes) < 32+1+0+8+16 { // minimum: no shortID, 16B ciphertext tag only
		return sessionID, ErrAuthFailed
	}

	ephPub := tokenBytes[:32]
	shortIDLen := int(tokenBytes[32])
	if shortIDLen > 8 || len(tokenBytes) < 32+1+shortIDLen+8+32 {
		return sessionID, ErrAuthFailed
	}

	shortID := tokenBytes[33 : 33+shortIDLen]
	ts := tokenBytes[33+shortIDLen : 33+shortIDLen+8]
	ciphertext := tokenBytes[33+shortIDLen+8:]

	// Check shortID
	if len(s.ShortIDs) > 0 {
		shortIDHex := fmt.Sprintf("%x", shortID)
		if !s.ShortIDs[shortIDHex] {
			return sessionID, ErrAuthFailed
		}
	}

	// Check timestamp
	timestamp := int64(binary.BigEndian.Uint64(ts))
	now := time.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if diff > s.MaxTimeDiff {
		return sessionID, ErrAuthFailed
	}

	// ECDH shared secret
	shared, err := curve25519.X25519(s.PrivateKey[:], ephPub)
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	// Derive auth key
	authKey, err := deriveKey(shared, shortID, ts, "nv-auth")
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	// AEAD open
	aead, err := chacha20poly1305.New(authKey)
	if err != nil {
		return sessionID, ErrAuthFailed
	}
	nonce := make([]byte, aead.NonceSize())
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	if len(plaintext) != 16 {
		return sessionID, ErrAuthFailed
	}
	copy(sessionID[:], plaintext)

	return sessionID, nil
}

// --- Helpers ---

// deriveKey uses HKDF-SHA256 to derive a 32-byte key.
func deriveKey(shared, shortID, timestamp []byte, info string) ([]byte, error) {
	salt := make([]byte, 0, len(shortID)+len(timestamp))
	salt = append(salt, shortID...)
	salt = append(salt, timestamp...)

	hkdfReader := hkdf.New(sha256.New, shared, salt, []byte(info))
	key := make([]byte, 32)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("hkdf derive: %w", err)
	}
	return key, nil
}

// extractToken tries to find the auth token in cookies first, then headers.
func extractToken(r *http.Request, name string) string {
	if name == "" {
		name = "nv_token"
	}
	// Try cookie first
	if cookie, err := r.Cookie(name); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	// Try header
	if v := r.Header.Get(name); v != "" {
		return v
	}
	return ""
}

// --- Key helpers for config loading ---

// DecodeKey decodes a base64-encoded 32-byte key.
func DecodeKey(b64 string) ([32]byte, error) {
	var key [32]byte
	raw, err := base64.RawStdEncoding.DecodeString(b64)
	if err != nil {
		return key, fmt.Errorf("decode key: %w", err)
	}
	if len(raw) != 32 {
		return key, errors.New("key must be 32 bytes")
	}
	copy(key[:], raw)
	return key, nil
}

// DerivePublicKey derives X25519 public key from private key.
func DerivePublicKey(private [32]byte) ([32]byte, error) {
	var pub [32]byte
	result, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return pub, err
	}
	copy(pub[:], result)
	return pub, nil
}
