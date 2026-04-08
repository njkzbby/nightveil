package config

// ServerConfig is the top-level server configuration.
type ServerConfig struct {
	Server ServerSettings `yaml:"server"`
}

type ServerSettings struct {
	Listen     string          `yaml:"listen"`
	TLS        TLSConfig       `yaml:"tls"`
	Auth       AuthConfig      `yaml:"auth"`
	Transport  TransportConfig `yaml:"transport"`
	Middleware []MiddlewareConfig `yaml:"middleware"`
	Fallback   FallbackConfig  `yaml:"fallback"`
	API        APIConfig       `yaml:"api"`
}

type APIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"` // e.g. "127.0.0.1:9090" — local only!
	Secret  string `yaml:"secret"` // bearer token for API auth
}

type TLSConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type AuthConfig struct {
	PrivateKey   string   `yaml:"private_key"`
	PublicKey    string   `yaml:"public_key"`     // derived on server, set on client
	ShortIDs     []string `yaml:"short_ids"`
	MaxTimeDiff  int64    `yaml:"max_time_diff"`
}

type TransportConfig struct {
	Type              string `yaml:"type"`
	PathPrefix        string `yaml:"path_prefix"`
	UploadPath        string `yaml:"upload_path"`
	DownloadPath      string `yaml:"download_path"`
	SessionKeyName    string `yaml:"session_key_name"`
	MaxChunkSize      int    `yaml:"max_chunk_size"`
	SessionTimeout    int    `yaml:"session_timeout"`
	MaxParallelUploads int   `yaml:"max_parallel_uploads"`
}

type MiddlewareConfig struct {
	Type     string `yaml:"type"`
	MinBytes int    `yaml:"min_bytes"`
	MaxBytes int    `yaml:"max_bytes"`
}

type FallbackConfig struct {
	Mode     string `yaml:"mode"`
	Upstream string `yaml:"upstream"`
	Root     string `yaml:"root"`
}
