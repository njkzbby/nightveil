// Package middleware defines composable processing stages
// that wrap transport connections to transform data in flight.
package middleware

import (
	"github.com/nightveil/nv/internal/transport"
)

// Middleware wraps a transport.Conn, transforming data on Read/Write.
type Middleware interface {
	Wrap(conn transport.Conn) transport.Conn
}

// Chain composes multiple middlewares.
// Applied left-to-right on Write, right-to-left on Read.
type Chain []Middleware

// Wrap applies all middlewares in the chain to the given connection.
func (c Chain) Wrap(conn transport.Conn) transport.Conn {
	for _, m := range c {
		conn = m.Wrap(conn)
	}
	return conn
}
