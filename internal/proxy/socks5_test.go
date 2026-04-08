package proxy

import (
	"net"
	"testing"
	"time"
)

func TestSOCKS5HandshakeDomain(t *testing.T) {
	// Create a pipe to simulate client<->server
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	errCh := make(chan error, 1)
	targetCh := make(chan *SOCKS5Target, 1)

	// Server side: perform handshake
	go func() {
		target, err := SOCKS5Handshake(serverConn)
		errCh <- err
		targetCh <- target
	}()

	// Client side: send SOCKS5 CONNECT for example.com:443
	// Auth negotiation: version=5, 1 method, no-auth
	clientConn.Write([]byte{0x05, 0x01, 0x00})

	// Read auth reply
	reply := make([]byte, 2)
	clientConn.Read(reply)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("bad auth reply: %x", reply)
	}

	// CONNECT request: version=5, cmd=connect, rsv=0, atyp=domain
	domain := "example.com"
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(domain))}
	req = append(req, []byte(domain)...)
	req = append(req, 0x01, 0xBB) // port 443 big-endian
	clientConn.Write(req)

	// Check result
	if err := <-errCh; err != nil {
		t.Fatalf("handshake error: %v", err)
	}
	target := <-targetCh
	if target.Host != "example.com" {
		t.Fatalf("host: got %q, want example.com", target.Host)
	}
	if target.Port != 443 {
		t.Fatalf("port: got %d, want 443", target.Port)
	}
}

func TestSOCKS5HandshakeIPv4(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	errCh := make(chan error, 1)
	targetCh := make(chan *SOCKS5Target, 1)

	go func() {
		target, err := SOCKS5Handshake(serverConn)
		errCh <- err
		targetCh <- target
	}()

	// Auth
	clientConn.Write([]byte{0x05, 0x01, 0x00})
	reply := make([]byte, 2)
	clientConn.Read(reply)

	// CONNECT to 1.2.3.4:8080
	req := []byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0x1F, 0x90}
	clientConn.Write(req)

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	target := <-targetCh
	if target.Host != "1.2.3.4" {
		t.Fatalf("host: got %q, want 1.2.3.4", target.Host)
	}
	if target.Port != 8080 {
		t.Fatalf("port: got %d, want 8080", target.Port)
	}
}

func TestSOCKS5HandshakeIPv6(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	errCh := make(chan error, 1)
	targetCh := make(chan *SOCKS5Target, 1)

	go func() {
		target, err := SOCKS5Handshake(serverConn)
		errCh <- err
		targetCh <- target
	}()

	// Auth
	clientConn.Write([]byte{0x05, 0x01, 0x00})
	reply := make([]byte, 2)
	clientConn.Read(reply)

	// CONNECT to [::1]:80 (atyp=4, 16 bytes IPv6, port)
	req := []byte{0x05, 0x01, 0x00, 0x04}
	ipv6 := make([]byte, 16)
	ipv6[15] = 1 // ::1
	req = append(req, ipv6...)
	req = append(req, 0x00, 0x50) // port 80
	clientConn.Write(req)

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	target := <-targetCh
	if target.Port != 80 {
		t.Fatalf("port: got %d, want 80", target.Port)
	}
}

func TestSOCKS5SendSuccess(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		SOCKS5SendSuccess(serverConn)
	}()

	reply := make([]byte, 10)
	n, err := clientConn.Read(reply)
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("reply length: got %d, want 10", n)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("bad reply: version=%d rep=%d", reply[0], reply[1])
	}
}

func TestSOCKS5BadVersion(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	errCh := make(chan error, 1)
	go func() {
		_, err := SOCKS5Handshake(serverConn)
		serverConn.Close()
		errCh <- err
	}()

	// Send SOCKS4 version — enough data for the 2-byte read
	clientConn.Write([]byte{0x04, 0x01, 0x00})
	clientConn.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error for SOCKS4 version")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}
