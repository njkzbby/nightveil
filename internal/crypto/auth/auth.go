// Package auth defines authentication interfaces for tunnel establishment.
package auth

import (
	"context"
	"errors"
	"net/http"
)

// ErrAuthFailed indicates the request did not pass authentication.
// The server should serve the fallback website.
var ErrAuthFailed = errors.New("authentication failed")

// ClientAuth generates authentication tokens for outbound requests.
type ClientAuth interface {
	GenerateToken(sessionID [16]byte) ([]byte, error)
}

// ServerAuth validates incoming requests.
type ServerAuth interface {
	Validate(ctx context.Context, r *http.Request) (sessionID [16]byte, err error)
}
