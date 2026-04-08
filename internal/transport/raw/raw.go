// Package raw provides a simple TCP transport for testing.
// Not for production — no encryption, no obfuscation.
package raw

import (
	"context"
	"fmt"
	"net"

	"github.com/nightveil/nv/internal/transport"
)

// connWrapper wraps net.Conn to implement transport.Conn.
type connWrapper struct {
	net.Conn
}

// --- Client ---

type Client struct {
	ServerAddr string
}

func (c *Client) Dial(ctx context.Context, sessionID [16]byte) (transport.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("raw dial %s: %w", c.ServerAddr, err)
	}
	return &connWrapper{conn}, nil
}

func (c *Client) Close() error { return nil }

// --- Server ---

type Server struct {
	Listener net.Listener
}

func NewServer(addr string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("raw listen %s: %w", addr, err)
	}
	return &Server{Listener: ln}, nil
}

func (s *Server) Accept(ctx context.Context) (transport.Conn, error) {
	conn, err := s.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &connWrapper{conn}, nil
}

func (s *Server) Close() error {
	return s.Listener.Close()
}
