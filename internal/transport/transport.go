// Package transport defines the interfaces for pluggable tunnel transports.
package transport

import (
	"context"
	"io"
	"net"
)

// Conn represents a bidirectional tunnel connection.
// It abstracts away whether the underlying mechanism is split HTTP,
// WebSocket, QUIC, or a raw TCP stream.
type Conn interface {
	io.ReadWriteCloser
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}

// ClientTransport dials outbound tunnel connections (client-side).
type ClientTransport interface {
	Dial(ctx context.Context, sessionID [16]byte) (Conn, error)
	Close() error
}

// ServerTransport accepts inbound tunnel connections (server-side).
type ServerTransport interface {
	Accept(ctx context.Context) (Conn, error)
	Close() error
}
