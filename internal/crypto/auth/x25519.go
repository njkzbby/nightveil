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
	"strings"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
	"crypto/sha256"
)

// --- Client ---

// ClientX25519 implements ClientAuth with per-user keys.
// Each user has their own keypair. Server knows user's public key.
type ClientX25519 struct {
	ServerPublicKey [32]byte
	UserPrivateKey  [32]byte // per-user private key
	UserPublicKey   [32]byte // per-user public key (derived from private)
	ShortID         []byte   // 0-8 bytes, user identifier for fast lookup
}

func (c *ClientX25519) GenerateToken(sessionID [16]byte) ([]byte, error) {
	// 1. Ephemeral X25519 keypair (per-request, forward secrecy)
	var ephPriv [32]byte
	if _, err := rand.Read(ephPriv[:]); err != nil {
		return nil, fmt.Errorf("generate ephemeral key: %w", err)
	}

	ephPub, err := curve25519.X25519(ephPriv[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive ephemeral public: %w", err)
	}

	// 2. Double ECDH: ephemeral×server + user×server
	sharedEph, err := curve25519.X25519(ephPriv[:], c.ServerPublicKey[:])
	if err != nil {
		return nil, fmt.Errorf("ecdh ephemeral: %w", err)
	}
	sharedUser, err := curve25519.X25519(c.UserPrivateKey[:], c.ServerPublicKey[:])
	if err != nil {
		return nil, fmt.Errorf("ecdh user: %w", err)
	}

	// Combine both shared secrets
	combined := make([]byte, 64)
	copy(combined[:32], sharedEph)
	copy(combined[32:], sharedUser)

	// 3. Timestamp
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(time.Now().Unix()))

	// 4. Derive auth key from combined secret
	authKey, err := deriveKey(combined, c.ShortID, ts[:], "nv-auth-v2")
	if err != nil {
		return nil, err
	}

	// 5. AEAD seal: encrypt sessionID with random nonce
	aead, err := chacha20poly1305.New(authKey)
	if err != nil {
		return nil, fmt.Errorf("create aead: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, sessionID[:], nil)

	// 6. Token: ephPub || userPub || shortIDLen || shortID || timestamp || nonce || ciphertext
	tokenLen := 32 + 32 + 1 + len(c.ShortID) + 8 + len(nonce) + len(ciphertext)
	token := make([]byte, 0, tokenLen)
	token = append(token, ephPub...)
	token = append(token, c.UserPublicKey[:]...)
	token = append(token, byte(len(c.ShortID)))
	token = append(token, c.ShortID...)
	token = append(token, ts[:]...)
	token = append(token, nonce...)
	token = append(token, ciphertext...)

	return token, nil
}

// --- Server ---

// UserEntry represents a registered user on the server.
type UserEntry struct {
	PublicKey [32]byte
	ShortID   string
	Name      string
}

// ServerX25519 implements ServerAuth with per-user key validation.
type ServerX25519 struct {
	PrivateKey  [32]byte
	Users       map[string]*UserEntry // shortID hex → user entry
	MaxTimeDiff int64
	TokenHeader string
}

func (s *ServerX25519) Validate(ctx context.Context, r *http.Request) (sessionID [16]byte, err error) {
	tokenB64 := extractToken(r, s.TokenHeader)
	if tokenB64 == "" {
		return sessionID, ErrAuthFailed
	}

	tokenBytes, err := base64.RawURLEncoding.DecodeString(tokenB64)
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	// Parse token: ephPub(32) || userPub(32) || shortIDLen(1) || shortID(0-8) || timestamp(8) || nonce(12) || ciphertext
	nonceSize := 12 // ChaCha20-Poly1305 nonce size
	if len(tokenBytes) < 32+32+1+0+8+nonceSize+16 {
		return sessionID, ErrAuthFailed
	}

	ephPub := tokenBytes[:32]
	userPub := tokenBytes[32:64]
	shortIDLen := int(tokenBytes[64])
	if shortIDLen > 8 || len(tokenBytes) < 65+shortIDLen+8+nonceSize+32 {
		return sessionID, ErrAuthFailed
	}

	shortID := tokenBytes[65 : 65+shortIDLen]
	ts := tokenBytes[65+shortIDLen : 65+shortIDLen+8]
	nonce := tokenBytes[65+shortIDLen+8 : 65+shortIDLen+8+nonceSize]
	ciphertext := tokenBytes[65+shortIDLen+8+nonceSize:]

	// Lookup user by shortID
	shortIDHex := fmt.Sprintf("%x", shortID)
	user, exists := s.Users[shortIDHex]
	if !exists {
		return sessionID, ErrAuthFailed
	}

	// Verify userPub matches registered user (zero = legacy mode, accept any)
	var userPubKey [32]byte
	copy(userPubKey[:], userPub)
	if user.PublicKey != ([32]byte{}) && userPubKey != user.PublicKey {
		return sessionID, ErrAuthFailed
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

	// Double ECDH: server_priv×ephemeral + server_priv×user_pub
	sharedEph, err := curve25519.X25519(s.PrivateKey[:], ephPub)
	if err != nil {
		return sessionID, ErrAuthFailed
	}
	sharedUser, err := curve25519.X25519(s.PrivateKey[:], userPub)
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	combined := make([]byte, 64)
	copy(combined[:32], sharedEph)
	copy(combined[32:], sharedUser)

	// Derive auth key
	authKey, err := deriveKey(combined, shortID, ts, "nv-auth-v2")
	if err != nil {
		return sessionID, ErrAuthFailed
	}

	// AEAD open (nonce already parsed from token)
	aead, err := chacha20poly1305.New(authKey)
	if err != nil {
		return sessionID, ErrAuthFailed
	}
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

func extractToken(r *http.Request, name string) string {
	if name == "" {
		name = "nv_token"
	}
	if cookie, err := r.Cookie(name); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if v := r.Header.Get(name); v != "" {
		return v
	}
	return ""
}

// --- Key helpers ---

func DecodeKey(b64 string) ([32]byte, error) {
	var key [32]byte
	// Normalize: accept both standard and URL-safe base64
	b64 = strings.ReplaceAll(b64, "-", "+")
	b64 = strings.ReplaceAll(b64, "_", "/")
	b64 = strings.TrimRight(b64, "=")

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

func DerivePublicKey(private [32]byte) ([32]byte, error) {
	var pub [32]byte
	result, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return pub, err
	}
	copy(pub[:], result)
	return pub, nil
}

// GenerateUserKeypair creates a new X25519 keypair for a user.
func GenerateUserKeypair() (private [32]byte, public [32]byte, err error) {
	if _, err = rand.Read(private[:]); err != nil {
		return
	}
	pub, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return
	}
	copy(public[:], pub)
	return
}
