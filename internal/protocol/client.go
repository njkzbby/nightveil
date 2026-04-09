package protocol

import (
	"context"
	"fmt"

	"github.com/njkzbby/nightveil/internal/transport"
)

// Client implements ClientProtocol.
type Client struct{}

func NewClient() *Client { return &Client{} }

// Handshake sends CONNECT/UDP with target address and waits for ACK.
func (c *Client) Handshake(ctx context.Context, conn transport.Conn, req *Request) error {
	// Send command frame (CmdConnect or CmdUDP)
	cmd := req.Command
	if cmd == 0 {
		cmd = CmdConnect
	}
	payload := EncodeConnectPayload(req.Host, req.Port)
	if err := WriteFrame(conn, &Frame{Type: cmd, Payload: payload}); err != nil {
		return fmt.Errorf("send command: %w", err)
	}

	// Read ACK
	ack, err := ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("read ack: %w", err)
	}
	if ack.Type != CmdACK {
		return fmt.Errorf("expected ACK, got type 0x%02x", ack.Type)
	}
	if len(ack.Payload) < 1 || Status(ack.Payload[0]) != StatusOK {
		status := StatusRefused
		if len(ack.Payload) >= 1 {
			status = Status(ack.Payload[0])
		}
		return fmt.Errorf("server refused connection: status 0x%02x", status)
	}

	return nil
}
