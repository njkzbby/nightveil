// Package security implements REALITY-style TLS camouflage.
//
// REALITY mode: the server listens on :443 and for every incoming TLS
// connection, attempts to authenticate the client via the first few bytes.
// - Authenticated clients: TLS terminates locally, XHTTP tunnel established
// - Unauthenticated clients (censors, probes): the entire TCP connection is
//   transparently forwarded to a real "dest" server (e.g. google.com:443).
//   The probe sees the real site's certificate, content, and behavior.
//
// This is simpler than Xray's REALITY (which intercepts the TLS handshake
// and swaps certificates). Our approach is a TCP-level proxy that requires
// no modification to the TLS layer.
package security

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

// RealityConfig configures the REALITY camouflage.
type RealityConfig struct {
	Dest    string        // forward destination, e.g. "google.com:443"
	Timeout time.Duration // connection timeout for dest
}

// RealityListener wraps a net.Listener and transparently forwards
// unauthenticated connections to the real destination server.
type RealityListener struct {
	inner    net.Listener
	config   RealityConfig
	authTest func(conn net.Conn) (authed net.Conn, isAuth bool)
}

// NewRealityListener creates a listener with REALITY camouflage.
// authTest peeks at the connection and returns:
//   - (wrappedConn, true) if the client is authenticated (our client)
//   - (wrappedConn, false) if not (censor probe — forward to dest)
//
// authTest MUST NOT consume bytes that it doesn't put back.
// Use PeekConn for this.
func NewRealityListener(inner net.Listener, cfg RealityConfig, authTest func(net.Conn) (net.Conn, bool)) *RealityListener {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &RealityListener{
		inner:    inner,
		config:   cfg,
		authTest: authTest,
	}
}

// Accept returns only authenticated connections.
// Unauthenticated ones are silently forwarded to dest.
func (rl *RealityListener) Accept() (net.Conn, error) {
	for {
		conn, err := rl.inner.Accept()
		if err != nil {
			return nil, err
		}

		authedConn, isAuth := rl.authTest(conn)
		if isAuth {
			return authedConn, nil
		}

		// Not authenticated — forward to real server
		go rl.forwardToDest(authedConn)
	}
}

// Addr returns the listener's address.
func (rl *RealityListener) Addr() net.Addr {
	return rl.inner.Addr()
}

// Close closes the listener.
func (rl *RealityListener) Close() error {
	return rl.inner.Close()
}

// forwardToDest transparently proxies the entire TCP connection
// to the real destination. The probe sees real google.com.
func (rl *RealityListener) forwardToDest(clientConn net.Conn) {
	defer clientConn.Close()

	destConn, err := net.DialTimeout("tcp", rl.config.Dest, rl.config.Timeout)
	if err != nil {
		log.Printf("[reality] dial dest %s: %v", rl.config.Dest, err)
		return
	}
	defer destConn.Close()

	// Bidirectional forward — censor sees real TLS handshake
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(destConn, clientConn)
	}()

	go func() {
		defer wg.Done()
		io.Copy(clientConn, destConn)
	}()

	wg.Wait()
}

// --- PeekConn: peek at first bytes without consuming them ---

// PeekConn wraps a net.Conn and allows peeking at the first N bytes.
type PeekConn struct {
	net.Conn
	peeked []byte
	read   int
}

// NewPeekConn wraps a connection for peeking.
func NewPeekConn(conn net.Conn) *PeekConn {
	return &PeekConn{Conn: conn}
}

// Peek reads up to n bytes without consuming them.
// Subsequent Read() calls will return these bytes first.
func (pc *PeekConn) Peek(n int) ([]byte, error) {
	if len(pc.peeked) >= n {
		return pc.peeked[:n], nil
	}

	buf := make([]byte, n)
	// Set a deadline for peeking — don't wait forever
	pc.Conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	total := 0
	for total < n {
		nr, err := pc.Conn.Read(buf[total:])
		total += nr
		if err != nil {
			pc.peeked = buf[:total]
			pc.Conn.SetReadDeadline(time.Time{}) // clear deadline
			return pc.peeked, err
		}
	}
	pc.Conn.SetReadDeadline(time.Time{})
	pc.peeked = buf[:total]
	return pc.peeked, nil
}

// Read implements net.Conn — returns peeked bytes first, then reads normally.
func (pc *PeekConn) Read(p []byte) (int, error) {
	if pc.read < len(pc.peeked) {
		n := copy(p, pc.peeked[pc.read:])
		pc.read += n
		return n, nil
	}
	return pc.Conn.Read(p)
}

// --- HTTP-based auth detection (for XHTTP mode) ---

// DetectNightveilAuth checks if the incoming HTTP request contains
// our authentication token. Used by REALITY to distinguish our clients
// from probes.
//
// For XHTTP over TLS: after TLS handshake, the first HTTP request
// either contains our auth cookie or it doesn't.
// - Has auth → our client → handle normally
// - No auth → probe → could serve fallback OR forward to dest
//
// This is called from the HTTP handler level, not TCP level.
// For TCP-level REALITY (before TLS), use RealityListener with
// a custom authTest that peeks at the ClientHello.
func DetectNightveilAuth(ctx context.Context) {
	// Placeholder — actual detection happens in XHTTP server's ServeHTTP
	// via auth.Validate(). The fallback handler serves the real site.
	// This function exists for documentation purposes.
}

// SNIExtract extracts SNI from a TLS ClientHello message.
// Returns empty string if not a valid ClientHello.
func SNIExtract(data []byte) string {
	// Minimum ClientHello: record header(5) + handshake header(4) + version(2) + random(32) + session_id_len(1)
	if len(data) < 44 {
		return ""
	}
	// TLS record: ContentType(1)=0x16, Version(2), Length(2)
	if data[0] != 0x16 {
		return ""
	}
	// Handshake: Type(1)=0x01 (ClientHello)
	if data[5] != 0x01 {
		return ""
	}

	// Skip to extensions
	pos := 5 + 4 // record header + handshake header
	pos += 2     // client version
	pos += 32    // random

	if pos >= len(data) {
		return ""
	}
	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	if pos+2 > len(data) {
		return ""
	}
	cipherSuitesLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherSuitesLen

	if pos+1 > len(data) {
		return ""
	}
	compMethodsLen := int(data[pos])
	pos += 1 + compMethodsLen

	if pos+2 > len(data) {
		return ""
	}
	extensionsLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	end := pos + extensionsLen
	if end > len(data) {
		end = len(data)
	}

	// Walk extensions looking for SNI (type 0x0000)
	for pos+4 <= end {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4

		if extType == 0 && pos+extLen <= end {
			// SNI extension
			sniData := data[pos : pos+extLen]
			if len(sniData) < 5 {
				return ""
			}
			// SNI list length (2), type (1)=0x00 (hostname), name length (2)
			nameLen := int(sniData[3])<<8 | int(sniData[4])
			if 5+nameLen <= len(sniData) {
				return string(sniData[5 : 5+nameLen])
			}
		}
		pos += extLen
	}

	return ""
}

// FormatRealityDest formats a REALITY destination from config.
func FormatRealityDest(dest string) string {
	if dest == "" {
		return ""
	}
	// Add :443 if no port specified
	_, _, err := net.SplitHostPort(dest)
	if err != nil {
		return fmt.Sprintf("%s:443", dest)
	}
	return dest
}
