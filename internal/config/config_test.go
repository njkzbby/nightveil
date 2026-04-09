package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServerConfig(t *testing.T) {
	yaml := `
server:
  listen: "0.0.0.0:443"
  tls:
    cert_file: "/etc/cert.pem"
    key_file: "/etc/key.pem"
  auth:
    private_key: "dGVzdGtleQ"
    short_ids:
      - "abcd1234"
      - "ef567890"
    max_time_diff: 120
  transport:
    type: "xhttp"
    max_chunk_size: 14336
    session_timeout: 30
  middleware:
    - type: "padding"
      min_bytes: 64
      max_bytes: 256
  fallback:
    mode: "reverse_proxy"
    upstream: "http://127.0.0.1:8080"
  api:
    enabled: true
    listen: "127.0.0.1:9090"
    secret: "mysecret"
`
	path := filepath.Join(t.TempDir(), "server.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	var cfg ServerConfig
	if err := Load(path, &cfg); err != nil {
		t.Fatalf("Load: %v", err)
	}

	s := cfg.Server
	if s.Listen != "0.0.0.0:443" {
		t.Errorf("listen: got %q", s.Listen)
	}
	if s.TLS.CertFile != "/etc/cert.pem" {
		t.Errorf("cert: got %q", s.TLS.CertFile)
	}
	if s.Auth.PrivateKey != "dGVzdGtleQ" {
		t.Errorf("private_key: got %q", s.Auth.PrivateKey)
	}
	if len(s.Auth.ShortIDs) != 2 {
		t.Errorf("short_ids count: got %d", len(s.Auth.ShortIDs))
	}
	if s.Auth.MaxTimeDiff != 120 {
		t.Errorf("max_time_diff: got %d", s.Auth.MaxTimeDiff)
	}
	if s.Transport.Type != "xhttp" {
		t.Errorf("transport type: got %q", s.Transport.Type)
	}
	if s.Transport.MaxChunkSize != 14336 {
		t.Errorf("max_chunk_size: got %d", s.Transport.MaxChunkSize)
	}
	if len(s.Middleware) != 1 || s.Middleware[0].Type != "padding" {
		t.Errorf("middleware: got %v", s.Middleware)
	}
	if s.Middleware[0].MinBytes != 64 || s.Middleware[0].MaxBytes != 256 {
		t.Errorf("padding range: got %d-%d", s.Middleware[0].MinBytes, s.Middleware[0].MaxBytes)
	}
	if s.Fallback.Mode != "reverse_proxy" {
		t.Errorf("fallback mode: got %q", s.Fallback.Mode)
	}
	if !s.API.Enabled {
		t.Error("api should be enabled")
	}
	if s.API.Listen != "127.0.0.1:9090" {
		t.Errorf("api listen: got %q", s.API.Listen)
	}
}

func TestLoadClientConfig(t *testing.T) {
	yaml := `
client:
  inbound:
    type: "socks5"
    listen: "127.0.0.1:1080"
  server:
    address: "my-domain.com:443"
  auth:
    server_public_key: "cHVia2V5"
    short_id: "abcdef01"
  transport:
    type: "xhttp"
    path_prefix: "/x7k2m9"
    upload_path: "/u/p3q"
    download_path: "/d/r8w"
    session_key_name: "cf_tok"
    max_chunk_size: 14336
  tls:
    fingerprint: "chrome"
  middleware:
    - type: "padding"
      min_bytes: 64
      max_bytes: 256
  anti_throttle:
    enabled: true
    detect_rtt_spike_ms: 500
    detect_throughput_drop_pct: 70
    response: "multiply"
  api:
    enabled: false
`
	path := filepath.Join(t.TempDir(), "client.yaml")
	os.WriteFile(path, []byte(yaml), 0644)

	var cfg ClientConfig
	if err := Load(path, &cfg); err != nil {
		t.Fatal(err)
	}

	c := cfg.Client
	if c.Inbound.Type != "socks5" || c.Inbound.Listen != "127.0.0.1:1080" {
		t.Errorf("inbound: got %+v", c.Inbound)
	}
	if c.Server.Address != "my-domain.com:443" {
		t.Errorf("server: got %q", c.Server.Address)
	}
	if c.Auth.ShortID != "abcdef01" {
		t.Errorf("short_id: got %q", c.Auth.ShortID)
	}
	if c.Transport.PathPrefix != "/x7k2m9" {
		t.Errorf("path_prefix: got %q", c.Transport.PathPrefix)
	}
	if c.Transport.UploadPath != "/u/p3q" {
		t.Errorf("upload_path: got %q", c.Transport.UploadPath)
	}
	if c.Transport.SessionKeyName != "cf_tok" {
		t.Errorf("session_key_name: got %q", c.Transport.SessionKeyName)
	}
	if c.TLS.Fingerprint != "chrome" {
		t.Errorf("fingerprint: got %q", c.TLS.Fingerprint)
	}
	if !c.AntiThrottle.Enabled {
		t.Error("anti_throttle should be enabled")
	}
	if c.AntiThrottle.DetectRTTSpikeMs != 500 {
		t.Errorf("rtt spike: got %d", c.AntiThrottle.DetectRTTSpikeMs)
	}
}

func TestLoadMissingFile(t *testing.T) {
	var cfg ServerConfig
	err := Load("/nonexistent/path.yaml", &cfg)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(path, []byte("{{{{not yaml"), 0644)

	var cfg ServerConfig
	err := Load(path, &cfg)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
