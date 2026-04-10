package config

// Default values for Nightveil configuration.
// All configurable via YAML — no hardcodes in business logic.
const (
	DefaultPort         = 443
	DefaultMaxChunkSize = 14336 // 14KB, under TSPU 15-20KB threshold
	DefaultFingerprint  = "chrome"
	DefaultTokenHeader  = "nv_token"
	DefaultSOCKSListen  = "127.0.0.1:10809"
	DefaultMaxTimeDiff  = 120 // seconds
	DefaultSessionTimeout = 30 // seconds

	// Protocol identifiers
	AuthInfoLabel = "nv-auth-v2"
	ProtocolName  = "nightveil"
	Version       = "0.2.0"
)
