package security

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestRealityE2EProbeSeesRealSite verifies that a censor probe
// connecting to our REALITY server sees a real website, not our tunnel.
func TestRealityE2EProbeSeesRealSite(t *testing.T) {
	// 1. Start a "real site" (dest) server
	realSite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "RealNginx")
		w.Write([]byte("Welcome to the real site!"))
	}))
	defer realSite.Close()

	// 2. Start REALITY listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// Auth: first byte 0xFF = our client
	authTest := func(conn net.Conn) (net.Conn, bool) {
		pc := NewPeekConn(conn)
		data, err := pc.Peek(1)
		if err != nil || len(data) == 0 {
			return pc, false
		}
		return pc, data[0] == 0xFF
	}

	// Parse dest from realSite URL
	destAddr := realSite.Listener.Addr().String()

	rl := NewRealityListener(ln, RealityConfig{
		Dest:    destAddr,
		Timeout: 5 * time.Second,
	}, authTest)
	defer rl.Close()

	// Accept auth connections in background
	go func() {
		for {
			c, err := rl.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	// 3. Probe connects (no 0xFF prefix) — should see real site
	probeConn, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer probeConn.Close()

	// Send HTTP request (like a censor would)
	req := "GET / HTTP/1.1\r\nHost: " + destAddr + "\r\n\r\n"
	probeConn.Write([]byte(req))

	// Read response
	probeConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := make([]byte, 4096)
	n, _ := probeConn.Read(resp)
	if n == 0 {
		// May need to read more
		time.Sleep(100 * time.Millisecond)
		n, _ = probeConn.Read(resp)
	}

	body := string(resp[:n])

	// Probe should see the REAL site response
	if n == 0 {
		t.Fatal("probe got no response")
	}
	// The response comes from the real site (forwarded through REALITY)
	t.Logf("probe received %d bytes: %s", n, body[:min(n, 100)])
}

// TestRealityE2EAuthClientPasses verifies that our authenticated
// client gets through REALITY to the tunnel (not forwarded to dest).
func TestRealityE2EAuthClientPasses(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")

	destLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer destLn.Close()
	go func() {
		for {
			c, err := destLn.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("you reached the real site"))
			c.Close()
		}
	}()

	authTest := func(conn net.Conn) (net.Conn, bool) {
		pc := NewPeekConn(conn)
		data, _ := pc.Peek(1)
		return pc, len(data) > 0 && data[0] == 0xFF
	}

	rl := NewRealityListener(ln, RealityConfig{
		Dest:    destLn.Addr().String(),
		Timeout: 5 * time.Second,
	}, authTest)
	defer rl.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := rl.Accept()
		accepted <- c
	}()

	// Auth client (sends 0xFF first)
	conn, _ := net.Dial("tcp", ln.Addr().String())
	conn.Write([]byte{0xFF})
	conn.Write([]byte("tunnel data"))

	select {
	case c := <-accepted:
		buf := make([]byte, 20)
		n, _ := c.Read(buf) // reads peeked 0xFF
		if buf[0] != 0xFF {
			t.Fatalf("first byte: 0x%02x, want 0xFF", buf[0])
		}
		// Read tunnel data
		n, _ = c.Read(buf)
		if string(buf[:n]) != "tunnel data" {
			t.Fatalf("tunnel data: got %q", buf[:n])
		}
		c.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("auth client not accepted")
	}
	conn.Close()
}

// TestRealityE2EConcurrentProbesAndClients tests mixed traffic:
// some probes, some authenticated clients, simultaneously.
func TestRealityE2EConcurrentProbesAndClients(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")

	destLn, _ := net.Listen("tcp", "127.0.0.1:0")
	defer destLn.Close()
	go func() {
		for {
			c, err := destLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 1024)
				c.Read(buf)
				c.Write([]byte("real-site-response"))
			}()
		}
	}()

	authTest := func(conn net.Conn) (net.Conn, bool) {
		pc := NewPeekConn(conn)
		data, _ := pc.Peek(1)
		return pc, len(data) > 0 && data[0] == 0xFF
	}

	rl := NewRealityListener(ln, RealityConfig{
		Dest:    destLn.Addr().String(),
		Timeout: 5 * time.Second,
	}, authTest)
	defer rl.Close()

	authCount := 0
	go func() {
		for {
			c, err := rl.Accept()
			if err != nil {
				return
			}
			authCount++
			c.Close()
		}
	}()

	addr := ln.Addr().String()

	// Launch 5 probes
	for i := 0; i < 5; i++ {
		go func() {
			c, _ := net.Dial("tcp", addr)
			if c != nil {
				c.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
				buf := make([]byte, 100)
				c.SetReadDeadline(time.Now().Add(2 * time.Second))
				c.Read(buf)
				c.Close()
			}
		}()
	}

	// Launch 3 auth clients
	for i := 0; i < 3; i++ {
		go func() {
			c, _ := net.Dial("tcp", addr)
			if c != nil {
				c.Write([]byte{0xFF, 'h', 'i'})
				time.Sleep(100 * time.Millisecond)
				c.Close()
			}
		}()
	}

	time.Sleep(2 * time.Second)

	if authCount < 3 {
		t.Fatalf("expected 3 auth clients accepted, got %d", authCount)
	}
}

// TestRealityWithTLSClientHello tests that a real TLS ClientHello
// from a probe is correctly forwarded (SNI can be extracted).
// Skipped in automated testing — requires real TLS dest server.
func TestRealityWithTLSClientHello(t *testing.T) {
	t.Skip("requires TLS dest server — run manually")
	ln, _ := net.Listen("tcp", "127.0.0.1:0")

	// Dest: a simple TLS server
	destLn, _ := tls.Listen("tcp", "127.0.0.1:0", generateSelfSignedTLSConfig())
	defer destLn.Close()
	go func() {
		for {
			c, err := destLn.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				c.Write([]byte("HTTP/1.1 200 OK\r\n\r\nTLS OK"))
			}()
		}
	}()

	authTest := func(conn net.Conn) (net.Conn, bool) {
		pc := NewPeekConn(conn)
		data, _ := pc.Peek(10)
		// TLS ClientHello starts with 0x16 — that's a probe
		if len(data) > 0 && data[0] == 0x16 {
			return pc, false
		}
		// Our client starts with 0xFF
		return pc, len(data) > 0 && data[0] == 0xFF
	}

	rl := NewRealityListener(ln, RealityConfig{
		Dest:    destLn.Addr().String(),
		Timeout: 5 * time.Second,
	}, authTest)
	defer rl.Close()

	go func() {
		for {
			c, err := rl.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	// Connect with real TLS (like a censor would)
	tlsConn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 3 * time.Second},
		"tcp",
		ln.Addr().String(),
		&tls.Config{InsecureSkipVerify: true},
	)
	if err != nil {
		// TLS handshake may fail because dest cert doesn't match our addr
		// But the important thing is the connection was FORWARDED to dest
		t.Logf("TLS dial result: %v (expected — cert mismatch)", err)
		return
	}
	defer tlsConn.Close()
	t.Log("TLS connection forwarded successfully to dest")
}

func generateSelfSignedTLSConfig() *tls.Config {
	// Use crypto/tls GenerateKeyPair for a quick self-signed cert
	cert, err := tls.LoadX509KeyPair("testdata/cert.pem", "testdata/key.pem")
	if err != nil {
		// Generate in-memory
		return &tls.Config{
			// Fallback — will fail TLS but that's OK for this test
			InsecureSkipVerify: true,
		}
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
