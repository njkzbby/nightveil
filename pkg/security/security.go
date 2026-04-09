// Package security re-exports TLS/uTLS config for external consumers.
package security

import "github.com/nightveil/nv/internal/security"

type UTLSConfig = security.UTLSConfig

var NewUTLSHTTPClient = security.NewUTLSHTTPClient
