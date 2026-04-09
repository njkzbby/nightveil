package config

// ClientConfig is the top-level client configuration.
type ClientConfig struct {
	Client ClientSettings `yaml:"client"`
}

type ClientSettings struct {
	Inbound      InboundConfig      `yaml:"inbound"`
	Server       ServerConnConfig   `yaml:"server"`
	Auth         ClientAuthConfig   `yaml:"auth"`
	Transport    TransportConfig    `yaml:"transport"`
	TLS          ClientTLSConfig    `yaml:"tls"`
	Middleware   []MiddlewareConfig `yaml:"middleware"`
	AntiThrottle AntiThrottleConfig `yaml:"anti_throttle"`
	API          APIConfig          `yaml:"api"`
}

type InboundConfig struct {
	Type   string `yaml:"type"`
	Listen string `yaml:"listen"`
}

type ServerConnConfig struct {
	Address string `yaml:"address"`
}

type ClientAuthConfig struct {
	ServerPublicKey string `yaml:"server_public_key"`
	UserPrivateKey  string `yaml:"user_private_key,omitempty"` // per-user key
	ShortID         string `yaml:"short_id"`
}

type ClientTLSConfig struct {
	Fingerprint string `yaml:"fingerprint"`
	SNI         string `yaml:"sni"`          // custom SNI (reality-mode: "google.com")
	SkipVerify  bool   `yaml:"skip_verify"`  // true for self-signed / reality
}

type AntiThrottleConfig struct {
	Enabled              bool   `yaml:"enabled"`
	DetectRTTSpikeMs     int    `yaml:"detect_rtt_spike_ms"`
	DetectThroughputDrop int    `yaml:"detect_throughput_drop_pct"`
	Response             string `yaml:"response"`
}

type IntervalRange struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}
