package quictun

import (
	"bytes"
	"net"
	"testing"
)

func TestXorBytes(t *testing.T) {
	data := []byte("hello world")
	key := []byte("secret key with extra length")

	encrypted := xorBytes(data, key)
	if bytes.Equal(encrypted, data) {
		t.Fatal("encrypted should differ from plaintext")
	}

	decrypted := xorBytes(encrypted, key)
	if !bytes.Equal(decrypted, data) {
		t.Fatalf("decrypted: %q, want %q", decrypted, data)
	}
}

func TestXorBytesShortKey(t *testing.T) {
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	key := []byte("ab") // 2 bytes — will repeat

	encrypted := xorBytes(data, key)
	decrypted := xorBytes(encrypted, key)

	if !bytes.Equal(decrypted, data) {
		t.Fatal("round-trip failed with short key")
	}
}

func TestDerivePacketKey(t *testing.T) {
	psk := []byte("my-secret-psk")
	salt1 := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	salt2 := []byte{8, 7, 6, 5, 4, 3, 2, 1}

	key1 := derivePacketKey(psk, salt1)
	key2 := derivePacketKey(psk, salt2)

	if len(key1) != keySize {
		t.Fatalf("key length: %d, want %d", len(key1), keySize)
	}

	// Different salts → different keys
	if bytes.Equal(key1, key2) {
		t.Fatal("different salts should produce different keys")
	}

	// Same salt → same key (deterministic)
	key1b := derivePacketKey(psk, salt1)
	if !bytes.Equal(key1, key1b) {
		t.Fatal("same salt should produce same key")
	}
}

func TestObfuscatedPacketConnRoundTrip(t *testing.T) {
	// Create two UDP sockets on localhost
	addr1, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	addr2, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")

	conn1, err := net.ListenUDP("udp", addr1)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()

	conn2, err := net.ListenUDP("udp", addr2)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	psk := []byte("test-psk-key")
	obfs1 := NewObfuscatedPacketConn(conn1, psk)
	obfs2 := NewObfuscatedPacketConn(conn2, psk)

	// Send from obfs1 to obfs2
	msg := []byte("hello obfuscated")
	_, err = obfs1.WriteTo(msg, conn2.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}

	// Read on obfs2
	buf := make([]byte, 1500)
	n, _, err := obfs2.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("got %q, want %q", buf[:n], msg)
	}
}

func TestObfuscatedPacketConnLargePacket(t *testing.T) {
	addr1, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	addr2, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")

	conn1, _ := net.ListenUDP("udp", addr1)
	defer conn1.Close()
	conn2, _ := net.ListenUDP("udp", addr2)
	defer conn2.Close()

	psk := []byte("large-test")
	obfs1 := NewObfuscatedPacketConn(conn1, psk)
	obfs2 := NewObfuscatedPacketConn(conn2, psk)

	// 1200 bytes — typical QUIC packet
	msg := make([]byte, 1200)
	for i := range msg {
		msg[i] = byte(i % 251)
	}

	obfs1.WriteTo(msg, conn2.LocalAddr())

	buf := make([]byte, 1500)
	n, _, err := obfs2.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatal("large packet round-trip failed")
	}
}

func TestObfuscatedWireFormatNotPlaintext(t *testing.T) {
	addr1, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	addr2, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")

	conn1, _ := net.ListenUDP("udp", addr1)
	defer conn1.Close()
	conn2raw, _ := net.ListenUDP("udp", addr2)
	defer conn2raw.Close()

	psk := []byte("secret")
	obfs1 := NewObfuscatedPacketConn(conn1, psk)

	msg := []byte("this is plaintext that should be obfuscated")
	obfs1.WriteTo(msg, conn2raw.LocalAddr())

	// Read RAW (without deobfuscation)
	buf := make([]byte, 1500)
	n, _, _ := conn2raw.ReadFromUDP(buf)

	// Raw wire data should NOT contain plaintext
	if bytes.Contains(buf[:n], msg) {
		t.Fatal("wire format contains plaintext — obfuscation failed!")
	}

	// Should be larger than original (salt + data)
	if n <= len(msg) {
		t.Fatalf("wire size %d should be > %d (original + salt)", n, len(msg))
	}
}
