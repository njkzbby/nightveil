// Package protocol defines the proxy protocol: commands, framing, handshake.
package protocol

import (
	"context"
	"fmt"
	"net"

	"github.com/njkzbby/nightveil/internal/transport"
)

// Command types for the proxy protocol.
type Command byte

const (
	CmdConnect Command = 0x01
	CmdACK     Command = 0x02
	CmdData    Command = 0x03
	CmdUDP     Command = 0x04 // future
	CmdClose   Command = 0xFF
)

// Status codes for ACK response.
type Status byte

const (
	StatusOK          Status = 0x00
	StatusRefused     Status = 0x01
	StatusUnreachable Status = 0x02
)

// AddrType identifies the address format.
type AddrType byte

const (
	AddrIPv4   AddrType = 0x01
	AddrDomain AddrType = 0x03
	AddrIPv6   AddrType = 0x04
)

// Request represents a parsed proxy request from the client.
type Request struct {
	Command Command
	Host    string
	Port    uint16
}

// Address returns "host:port".
func (r *Request) Address() string {
	return net.JoinHostPort(r.Host, fmt.Sprintf("%d", r.Port))
}

// ClientProtocol handles the client side of the proxy protocol.
type ClientProtocol interface {
	Handshake(ctx context.Context, conn transport.Conn, req *Request) error
}

// ServerProtocol handles the server side of the proxy protocol.
type ServerProtocol interface {
	HandleConnection(ctx context.Context, conn transport.Conn) (*Request, error)
}
