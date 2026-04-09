package security

import (
	"io"
	"net"
	"testing"
	"time"
)

func TestPeekConnBasic(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	go func() {
		right.Write([]byte("hello world"))
	}()

	pc := NewPeekConn(left)

	// Peek first 5 bytes
	peeked, err := pc.Peek(5)
	if err != nil {
		t.Fatal(err)
	}
	if string(peeked) != "hello" {
		t.Fatalf("peek: got %q", peeked)
	}

	// Read should return peeked bytes first, then the rest
	buf := make([]byte, 20)
	n, _ := pc.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Fatalf("first read: got %q", buf[:n])
	}

	n, _ = pc.Read(buf)
	if string(buf[:n]) != " world" {
		t.Fatalf("second read: got %q", buf[:n])
	}
}

func TestPeekConnFullRead(t *testing.T) {
	left, right := net.Pipe()

	msg := "complete message"
	go func() {
		right.Write([]byte(msg))
		right.Close()
	}()

	pc := NewPeekConn(left)
	pc.Peek(5)

	// ReadAll should get everything
	all, _ := io.ReadAll(pc)
	if string(all) != msg {
		t.Fatalf("readall: got %q, want %q", all, msg)
	}
}

func TestRealityListenerAuthPass(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// Auth test: first byte 0xAA = authenticated
	authTest := func(conn net.Conn) (net.Conn, bool) {
		pc := NewPeekConn(conn)
		data, _ := pc.Peek(1)
		if len(data) > 0 && data[0] == 0xAA {
			return pc, true
		}
		return pc, false
	}

	// Dest server (for unauthenticated)
	destLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer destLn.Close()
	go func() {
		for {
			c, err := destLn.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("real-site"))
			c.Close()
		}
	}()

	rl := NewRealityListener(ln, RealityConfig{
		Dest:    destLn.Addr().String(),
		Timeout: 5 * time.Second,
	}, authTest)
	defer rl.Close()

	// Authenticated client
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := rl.Accept()
		accepted <- c
	}()

	// Connect as authenticated client
	conn, _ := net.Dial("tcp", ln.Addr().String())
	conn.Write([]byte{0xAA, 'h', 'e', 'l', 'l', 'o'})

	select {
	case c := <-accepted:
		buf := make([]byte, 10)
		n, _ := c.Read(buf)
		// Should get 0xAA + hello (peeked byte + rest)
		if n < 1 || buf[0] != 0xAA {
			t.Fatalf("auth conn first byte: got 0x%02x", buf[0])
		}
		c.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for authenticated connection")
	}
	conn.Close()
}

func TestRealityListenerForwardProbe(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")

	authTest := func(conn net.Conn) (net.Conn, bool) {
		pc := NewPeekConn(conn)
		data, _ := pc.Peek(1)
		if len(data) > 0 && data[0] == 0xAA {
			return pc, true
		}
		return pc, false
	}

	// Dest server — echoes back what it receives
	destLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer destLn.Close()

	destGot := make(chan string, 1)
	go func() {
		for {
			c, err := destLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				// Read until we get the full request (ends with \r\n\r\n)
				var all []byte
				buf := make([]byte, 4096)
				for {
					c.SetReadDeadline(time.Now().Add(2 * time.Second))
					n, err := c.Read(buf)
					if n > 0 {
						all = append(all, buf[:n]...)
					}
					// Check if we have full HTTP request
					if len(all) > 4 && string(all[len(all)-4:]) == "\r\n\r\n" {
						break
					}
					if err != nil {
						break
					}
				}
				if len(all) > 0 {
					destGot <- string(all)
				}
				c.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
			}()
		}
	}()

	rl := NewRealityListener(ln, RealityConfig{
		Dest:    destLn.Addr().String(),
		Timeout: 5 * time.Second,
	}, authTest)
	defer rl.Close()

	go func() {
		rl.Accept() // blocks waiting for auth connection
	}()

	// Connect as probe (first byte != 0xAA)
	conn, _ := net.Dial("tcp", ln.Addr().String())
	conn.Write([]byte("GET / HTTP/1.1\r\nHost: google.com\r\n\r\n"))

	// Read response from dest (forwarded through our server)
	buf := make([]byte, 100)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	conn.Read(buf) // don't care about result, just wait
	conn.Close()

	select {
	case got := <-destGot:
		if got != "GET / HTTP/1.1\r\nHost: google.com\r\n\r\n" {
			t.Fatalf("dest received: %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("probe was not forwarded to dest")
	}
}

func TestSNIExtract(t *testing.T) {
	// Minimal TLS ClientHello with SNI=example.com
	// This is a simplified hand-crafted ClientHello
	hello := buildMinimalClientHello("example.com")
	sni := SNIExtract(hello)
	if sni != "example.com" {
		t.Fatalf("SNI: got %q, want example.com", sni)
	}
}

func TestSNIExtractNoSNI(t *testing.T) {
	// Not a TLS record
	sni := SNIExtract([]byte("GET / HTTP/1.1\r\n"))
	if sni != "" {
		t.Fatalf("expected empty SNI, got %q", sni)
	}
}

func TestSNIExtractShort(t *testing.T) {
	sni := SNIExtract([]byte{0x16, 0x03})
	if sni != "" {
		t.Fatalf("expected empty SNI for short data, got %q", sni)
	}
}

func TestFormatRealityDest(t *testing.T) {
	tests := []struct {
		in, out string
	}{
		{"google.com", "google.com:443"},
		{"google.com:443", "google.com:443"},
		{"1.2.3.4:8443", "1.2.3.4:8443"},
		{"", ""},
	}
	for _, tt := range tests {
		got := FormatRealityDest(tt.in)
		if got != tt.out {
			t.Errorf("FormatRealityDest(%q) = %q, want %q", tt.in, got, tt.out)
		}
	}
}

// buildMinimalClientHello constructs a minimal TLS 1.2 ClientHello with SNI.
func buildMinimalClientHello(sni string) []byte {
	// SNI extension
	sniBytes := []byte(sni)
	sniExt := []byte{
		0x00, 0x00, // extension type: SNI
		0x00, byte(len(sniBytes) + 5), // extension length
		0x00, byte(len(sniBytes) + 3), // SNI list length
		0x00,                    // host name type
		0x00, byte(len(sniBytes)), // host name length
	}
	sniExt = append(sniExt, sniBytes...)

	// Extensions total
	extLen := len(sniExt)
	extensions := []byte{byte(extLen >> 8), byte(extLen)}
	extensions = append(extensions, sniExt...)

	// ClientHello body
	body := []byte{
		0x03, 0x03, // version TLS 1.2
	}
	// Random (32 bytes)
	body = append(body, make([]byte, 32)...)
	// Session ID length = 0
	body = append(body, 0x00)
	// Cipher suites (2 bytes length + 2 bytes suite)
	body = append(body, 0x00, 0x02, 0x00, 0xFF)
	// Compression methods (1 byte length + 1 byte null)
	body = append(body, 0x01, 0x00)
	// Extensions
	body = append(body, extensions...)

	// Handshake header
	handshake := []byte{
		0x01, // ClientHello
		0x00, byte(len(body) >> 8), byte(len(body)),
	}
	handshake = append(handshake, body...)

	// TLS record
	record := []byte{
		0x16,       // ContentType: Handshake
		0x03, 0x01, // Version: TLS 1.0 (record layer)
		byte(len(handshake) >> 8), byte(len(handshake)),
	}
	record = append(record, handshake...)

	return record
}
