// Package api defines the internal management API used by CLI, web UI, and future frontends.
// The API is exposed as an HTTP/JSON server on a local-only port.
package api

import "time"

// Status represents the current state of the tunnel.
type Status struct {
	Connected     bool          `json:"connected"`
	ServerAddr    string        `json:"server_addr"`
	Transport     string        `json:"transport"`
	Uptime        time.Duration `json:"uptime_ms"`
	BytesSent     int64         `json:"bytes_sent"`
	BytesRecv     int64         `json:"bytes_recv"`
	ActiveConns   int           `json:"active_conns"`
	Throttled     bool          `json:"throttled"`
	CurrentMode   string        `json:"current_mode"` // "stealth", "speed", "resilient"
	ParallelConns int           `json:"parallel_conns"`
}

// ServerStatus represents server-side monitoring info.
type ServerStatus struct {
	Listening      bool   `json:"listening"`
	ActiveSessions int    `json:"active_sessions"`
	TotalClients   int    `json:"total_clients"`
	BytesSent      int64  `json:"bytes_sent"`
	BytesRecv      int64  `json:"bytes_recv"`
	AuthFailures   int64  `json:"auth_failures"`
}

// ClientAPI is the management interface for the client process.
type ClientAPI interface {
	// Status returns current tunnel state.
	GetStatus() *Status

	// Connect initiates the tunnel. Non-blocking.
	Connect() error

	// Disconnect tears down the tunnel.
	Disconnect() error

	// Reconnect forces a reconnection (new CDN edge IP, new session).
	Reconnect() error

	// SetMode changes the operating mode at runtime.
	SetMode(mode string) error
}

// ServerAPI is the management interface for the server process.
type ServerAPI interface {
	// Status returns current server state.
	GetStatus() *ServerStatus

	// ListClients returns connected client info.
	ListClients() []ClientInfo

	// AddShortID adds a new authorized client shortID.
	AddShortID(shortID string) error

	// RemoveShortID revokes a client shortID.
	RemoveShortID(shortID string) error
}

// ClientInfo describes a connected client (server-side view).
type ClientInfo struct {
	ShortID    string    `json:"short_id"`
	RemoteAddr string    `json:"remote_addr"`
	ConnectedAt time.Time `json:"connected_at"`
	BytesSent  int64     `json:"bytes_sent"`
	BytesRecv  int64     `json:"bytes_recv"`
}
