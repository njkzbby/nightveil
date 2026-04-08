package protocol

import (
	"context"
	"fmt"

	"github.com/nightveil/nv/internal/transport"
)

// Server implements ServerProtocol.
type Server struct{}

func NewServer() *Server { return &Server{} }

// HandleConnection reads CONNECT, parses target, sends ACK.
// After this, the conn is ready for bidirectional relay.
func (s *Server) HandleConnection(ctx context.Context, conn transport.Conn) (*Request, error) {
	f, err := ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}

	if f.Type != CmdConnect {
		return nil, fmt.Errorf("expected CONNECT, got type 0x%02x", f.Type)
	}

	host, port, err := DecodeConnectPayload(f.Payload)
	if err != nil {
		// Send refused ACK
		WriteFrame(conn, &Frame{Type: CmdACK, Payload: []byte{byte(StatusRefused)}})
		return nil, fmt.Errorf("decode connect: %w", err)
	}

	return &Request{
		Command: CmdConnect,
		Host:    host,
		Port:    port,
	}, nil
}

// SendACK sends a success ACK to the client.
func SendACK(conn transport.Conn, status Status) error {
	return WriteFrame(conn, &Frame{Type: CmdACK, Payload: []byte{byte(status)}})
}
