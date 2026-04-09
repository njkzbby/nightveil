package config

// ServerConfig is the top-level server configuration.
type ServerConfig struct {
	Server ServerSettings `yaml:"server"`
}

type ServerSettings struct {
	Listen     string             `yaml:"listen"`
	TLS        TLSConfig          `yaml:"tls"`
	Auth       AuthConfig         `yaml:"auth"`
	Transport  TransportConfig    `yaml:"transport"`
	Middleware []MiddlewareConfig  `yaml:"middleware"`
	Fallback   FallbackConfig     `yaml:"fallback"`
	API        APIConfig          `yaml:"api"`
}

type APIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	Secret  string `yaml:"secret"`
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	// REALITY mode
	Dest     string `yaml:"dest"`     // e.g. "google.com:443" — forward TLS to this server
	SNI      string `yaml:"sni"`      // SNI for reality handshake
}

type AuthConfig struct {
	PrivateKey  string      `yaml:"private_key"`
	ShortIDs    []string    `yaml:"short_ids"`    // legacy: shortID-only auth
	Users       []UserConfig `yaml:"users"`        // per-user keys
	MaxTimeDiff int64       `yaml:"max_time_diff"`
}

// UserConfig represents a registered user with per-user key.
type UserConfig struct {
	ShortID   string `yaml:"short_id"`
	PublicKey string `yaml:"public_key"`
	Name      string `yaml:"name"`
}

type TransportConfig struct {
	Type           string `yaml:"type"`
	PathPrefix     string `yaml:"path_prefix"`
	UploadPath     string `yaml:"upload_path"`
	DownloadPath   string `yaml:"download_path"`
	SessionKeyName string `yaml:"session_key_name"`
	MaxChunkSize   int    `yaml:"max_chunk_size"`
	SessionTimeout int    `yaml:"session_timeout"`
}

type MiddlewareConfig struct {
	Type     string `yaml:"type"`
	MinBytes int    `yaml:"min_bytes"`
	MaxBytes int    `yaml:"max_bytes"`
}

type FallbackConfig struct {
	Mode     string `yaml:"mode"`     // "reverse_proxy", "static", "default"
	Upstream string `yaml:"upstream"` // for reverse_proxy mode
	Root     string `yaml:"root"`     // for static mode
}
