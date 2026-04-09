// Package quictun implements a QUIC-based tunnel transport with
// Salamander-style obfuscation that makes packets unrecognizable as QUIC.
package quictun

import (
	"crypto/rand"
	"net"
	"time"

	"golang.org/x/crypto/blake2b"
)

const (
	saltSize = 8  // random salt per packet
	keySize  = 32 // BLAKE2b-256 output
)

// ObfuscatedPacketConn wraps a net.PacketConn with XOR obfuscation.
// Each packet: [8B salt][XOR-obfuscated payload]
// Key per packet: BLAKE2b-256(PSK || salt)
type ObfuscatedPacketConn struct {
	inner net.PacketConn
	psk   []byte // pre-shared key
}

// NewObfuscatedPacketConn wraps a PacketConn with Salamander-style obfuscation.
func NewObfuscatedPacketConn(inner net.PacketConn, psk []byte) *ObfuscatedPacketConn {
	return &ObfuscatedPacketConn{inner: inner, psk: psk}
}

// ReadFrom reads and deobfuscates a packet.
func (c *ObfuscatedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buf := make([]byte, len(p)+saltSize)
	n, addr, err := c.inner.ReadFrom(buf)
	if err != nil {
		return 0, addr, err
	}
	if n < saltSize {
		return 0, addr, nil // too short, skip
	}

	salt := buf[:saltSize]
	ciphertext := buf[saltSize:n]

	// Derive key: BLAKE2b-256(PSK || salt)
	key := derivePacketKey(c.psk, salt)

	// XOR decrypt
	plaintext := xorBytes(ciphertext, key)
	copy(p, plaintext)

	return len(plaintext), addr, nil
}

// WriteTo obfuscates and writes a packet.
func (c *ObfuscatedPacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	// Random salt
	salt := make([]byte, saltSize)
	rand.Read(salt)

	// Derive key
	key := derivePacketKey(c.psk, salt)

	// XOR encrypt
	ciphertext := xorBytes(p, key)

	// Wire: salt + ciphertext
	packet := make([]byte, saltSize+len(ciphertext))
	copy(packet[:saltSize], salt)
	copy(packet[saltSize:], ciphertext)

	_, err := c.inner.WriteTo(packet, addr)
	return len(p), err
}

func (c *ObfuscatedPacketConn) Close() error                       { return c.inner.Close() }
func (c *ObfuscatedPacketConn) LocalAddr() net.Addr                { return c.inner.LocalAddr() }
func (c *ObfuscatedPacketConn) SetDeadline(t time.Time) error      { return c.inner.SetDeadline(t) }
func (c *ObfuscatedPacketConn) SetReadDeadline(t time.Time) error  { return c.inner.SetReadDeadline(t) }
func (c *ObfuscatedPacketConn) SetWriteDeadline(t time.Time) error { return c.inner.SetWriteDeadline(t) }

// derivePacketKey computes BLAKE2b-256(PSK || salt).
func derivePacketKey(psk, salt []byte) []byte {
	input := make([]byte, len(psk)+len(salt))
	copy(input, psk)
	copy(input[len(psk):], salt)

	hash := blake2b.Sum256(input)
	return hash[:]
}

// xorBytes XORs data with key (repeating key if shorter).
func xorBytes(data, key []byte) []byte {
	result := make([]byte, len(data))
	for i := range data {
		result[i] = data[i] ^ key[i%len(key)]
	}
	return result
}
