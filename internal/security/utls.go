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

// UTLSConfig for creating the HTTP client.
type UTLSConfig struct {
	ServerName  string
	Fingerprint string
	SkipVerify  bool
}

// NewUTLSHTTPClient creates an http.Client that uses uTLS with Chrome fingerprint.
// Uses HTTP/2 with tuned timeouts for stable long-lived streaming.
func NewUTLSHTTPClient(cfg UTLSConfig) *http.Client {
	helloID := ResolveHelloID(cfg.Fingerprint)

	dialer := &net.Dialer{Timeout: 30 * time.Second}

	transport := &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			tcpConn, err := dialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, fmt.Errorf("dial %s: %w", addr, err)
			}

			sni := cfg.ServerName
			if sni == "" {
				sni, _, _ = net.SplitHostPort(addr)
			}

			tlsConn := utls.UClient(tcpConn, &utls.Config{
				ServerName:         sni,
				InsecureSkipVerify: cfg.SkipVerify,
			}, helloID)

			if err := tlsConn.HandshakeContext(ctx); err != nil {
				tcpConn.Close()
				return nil, fmt.Errorf("utls handshake: %w", err)
			}

			return tlsConn, nil
		},
		// Aggressive ping schedule — the goal is to recover fast from brief
		// network stalls (DPI shaping, router blips, ISP flaps) that manifest
		// as the user-visible "messages hang, then all arrive at once" pattern.
		// Previous 30s/15s meant up to 45 seconds of silent stall before the
		// conn was killed and the client reconnected. 5s/5s caps that at 10s,
		// which is still well above legitimate RTT jitter but way below what
		// a human notices as "internet froze".
		ReadIdleTimeout: 5 * time.Second, // send ping after 5s idle
		PingTimeout:     5 * time.Second, // wait 5s for ping response
		// Don't timeout streaming writes
		WriteByteTimeout: 0,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   0, // no global timeout for streaming
	}
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
