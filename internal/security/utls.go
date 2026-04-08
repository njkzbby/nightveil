// Package security provides uTLS-based HTTP client for Chrome fingerprint mimicry.
package security

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// NewUTLSHTTPClient creates an http.Client that uses uTLS with the specified
// browser fingerprint for all TLS connections.
func NewUTLSHTTPClient(serverName string, fingerprint string) *http.Client {
	helloID := ResolveHelloID(fingerprint)

	dialer := &net.Dialer{Timeout: 30 * time.Second}

	transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			tcpConn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, fmt.Errorf("dial %s: %w", addr, err)
			}

			host := serverName
			if host == "" {
				host, _, _ = net.SplitHostPort(addr)
			}

			tlsConn := utls.UClient(tcpConn, &utls.Config{
				ServerName: host,
			}, helloID)

			if err := tlsConn.HandshakeContext(ctx); err != nil {
				tcpConn.Close()
				return nil, fmt.Errorf("utls handshake: %w", err)
			}

			return tlsConn, nil
		},
	}

	return &http.Client{Transport: transport}
}

// ResolveHelloID maps a fingerprint name to a uTLS ClientHelloID.
func ResolveHelloID(name string) utls.ClientHelloID {
	switch strings.ToLower(name) {
	case "chrome":
		return utls.HelloChrome_Auto
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari":
		return utls.HelloSafari_Auto
	case "edge":
		return utls.HelloEdge_Auto
	case "random":
		return utls.HelloRandomized
	default:
		return utls.HelloChrome_Auto
	}
}
