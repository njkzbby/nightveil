// Package singbox implements a sing-box Outbound adapter for Nightveil.
// This allows using Nightveil as a protocol in sing-box based clients
// (Nekobox, SFI, v2rayN with sing-box core, etc.)
//
// Usage in sing-box config:
//
//	{
//	  "outbounds": [{
//	    "type": "nightveil",
//	    "tag": "nv-proxy",
//	    "server": "your-domain.com",
//	    "server_port": 443,
//	    "server_public_key": "base64...",
//	    "short_id": "abcdef01",
//	    "path_prefix": "/x7k2m9",
//	    "upload_path": "/u/p3q",
//	    "download_path": "/d/r8w",
//	    "tls": { "enabled": true, "server_name": "your-domain.com" }
//	  }]
//	}
package singbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"github.com/njkzbby/nightveil/internal/protocol"
	"github.com/njkzbby/nightveil/internal/transport/xhttp"

	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const TypeNightveil = "nightveil"

// Options for Nightveil outbound configuration in sing-box.
type Options struct {
	Server          string `json:"server"`
	ServerPort      uint16 `json:"server_port"`
	ServerPublicKey string `json:"server_public_key"`
	UserPrivateKey  string `json:"user_private_key,omitempty"`
	ShortID         string `json:"short_id"`
	PathPrefix      string `json:"path_prefix"`
	UploadPath      string `json:"upload_path"`
	DownloadPath    string `json:"download_path"`
	SessionKeyName  string `json:"session_key_name"`
	MaxChunkSize    int    `json:"max_chunk_size"`
	TLSFingerprint  string `json:"tls_fingerprint"`
}

// Outbound implements the sing-box adapter.Outbound interface.
type Outbound struct {
	tag        string
	options    Options
	clientAuth *auth.ClientX25519
	httpClient *http.Client
	proto      *protocol.Client
}

// NewOutbound creates a Nightveil outbound adapter for sing-box.
func NewOutbound(tag string, opts Options) (*Outbound, error) {
	serverPubKey, err := auth.DecodeKey(opts.ServerPublicKey)
	if err != nil {
		return nil, fmt.Errorf("invalid server_public_key: %w", err)
	}

	shortID, err := hex.DecodeString(opts.ShortID)
	if err != nil {
		return nil, fmt.Errorf("invalid short_id: %w", err)
	}

	var userPriv, userPub [32]byte
	if opts.UserPrivateKey != "" {
		userPriv, _ = auth.DecodeKey(opts.UserPrivateKey)
		userPub, _ = auth.DerivePublicKey(userPriv)
	} else {
		userPriv, userPub, _ = auth.GenerateUserKeypair()
	}

	clientAuth := &auth.ClientX25519{
		ServerPublicKey: serverPubKey,
		UserPrivateKey:  userPriv,
		UserPublicKey:   userPub,
		ShortID:         shortID,
	}

	// For now use default HTTP client. In production, integrate with
	// sing-box's TLS/dialer system for proper fingerprinting.
	httpClient := &http.Client{Timeout: 30 * time.Second}

	return &Outbound{
		tag:        tag,
		options:    opts,
		clientAuth: clientAuth,
		httpClient: httpClient,
		proto:      protocol.NewClient(),
	}, nil
}

// --- sing-box adapter.Outbound interface ---

func (o *Outbound) Type() string     { return TypeNightveil }
func (o *Outbound) Tag() string      { return o.tag }
func (o *Outbound) Network() []string { return []string{N.NetworkTCP} }
func (o *Outbound) Dependencies() []string { return nil }

// DialContext establishes a proxied TCP connection through the Nightveil tunnel.
// This is the core method that sing-box calls for each new connection.
func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	// Build XHTTP config from options
	cfg := xhttp.Config{
		PathPrefix:     o.options.PathPrefix,
		UploadPath:     o.options.UploadPath,
		DownloadPath:   o.options.DownloadPath,
		SessionKeyName: o.options.SessionKeyName,
		MaxChunkSize:   o.options.MaxChunkSize,
	}

	serverURL := fmt.Sprintf("https://%s:%d", o.options.Server, o.options.ServerPort)
	client := xhttp.NewClient(serverURL, cfg, o.clientAuth, o.httpClient)

	// Generate session ID
	var sessionID [16]byte
	rand.Read(sessionID[:])

	// Dial tunnel
	tunnelConn, err := client.Dial(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("nightveil dial: %w", err)
	}

	// Protocol handshake — send target address
	host := destination.AddrString()
	port := destination.Port
	req := &protocol.Request{
		Command: protocol.CmdConnect,
		Host:    host,
		Port:    port,
	}

	if err := o.proto.Handshake(ctx, tunnelConn, req); err != nil {
		tunnelConn.Close()
		return nil, fmt.Errorf("nightveil handshake: %w", err)
	}

	// Return the tunnel connection as a net.Conn
	return &nvConn{
		Conn:   tunnelConn,
		local:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)},
		remote: destination.TCPAddr(),
	}, nil
}

// ListenPacket is not supported yet (UDP relay is future work).
func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, fmt.Errorf("nightveil: UDP not supported yet")
}

// --- net.Conn wrapper ---

// nvConn wraps our transport.Conn to satisfy net.Conn with proper addresses.
type nvConn struct {
	Conn interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}
	local  net.Addr
	remote net.Addr
}

func (c *nvConn) Read(b []byte) (int, error)  { return c.Conn.Read(b) }
func (c *nvConn) Write(b []byte) (int, error) { return c.Conn.Write(b) }
func (c *nvConn) Close() error                { return c.Conn.Close() }
func (c *nvConn) LocalAddr() net.Addr          { return c.local }
func (c *nvConn) RemoteAddr() net.Addr         { return c.remote }

func (c *nvConn) SetDeadline(t time.Time) error      { return nil }
func (c *nvConn) SetReadDeadline(t time.Time) error   { return nil }
func (c *nvConn) SetWriteDeadline(t time.Time) error  { return nil }
