// Package auth re-exports authentication for external consumers (sing-box plugin).
package auth

import "github.com/njkzbby/nightveil/internal/crypto/auth"

type ClientX25519 = auth.ClientX25519
type ServerX25519 = auth.ServerX25519

type UserEntry = auth.UserEntry

var (
	DecodeKey            = auth.DecodeKey
	DerivePublicKey      = auth.DerivePublicKey
	GenerateUserKeypair  = auth.GenerateUserKeypair
	ErrAuthFailed        = auth.ErrAuthFailed
)
