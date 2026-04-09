// Package protocol re-exports the proxy protocol for external consumers.
package protocol

import "github.com/njkzbby/nightveil/internal/protocol"

type Client = protocol.Client
type Request = protocol.Request
type UDPRelay = protocol.UDPRelay
type UDPMessage = protocol.UDPMessage

const (
	CmdConnect = protocol.CmdConnect
	CmdUDP     = protocol.CmdUDP
)

var NewClient = protocol.NewClient
var NewUDPRelay = protocol.NewUDPRelay

// Re-export framing for handshake
var WriteFrame = protocol.WriteFrame
var ReadFrame = protocol.ReadFrame

type Frame = protocol.Frame
type Command = protocol.Command
type Status = protocol.Status

const (
	CmdACK    = protocol.CmdACK
	StatusOK  = protocol.StatusOK
)
