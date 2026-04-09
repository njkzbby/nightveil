package security

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

func TestResolveHelloID(t *testing.T) {
	tests := []struct {
		input string
		want  utls.ClientHelloID
	}{
		{"chrome", utls.HelloChrome_Auto},
		{"Chrome", utls.HelloChrome_Auto},
		{"CHROME", utls.HelloChrome_Auto},
		{"firefox", utls.HelloFirefox_Auto},
		{"safari", utls.HelloSafari_Auto},
		{"edge", utls.HelloEdge_Auto},
		{"random", utls.HelloRandomized},
		{"", utls.HelloChrome_Auto},          // default
		{"unknown", utls.HelloChrome_Auto},   // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ResolveHelloID(tt.input)
			if got != tt.want {
				t.Errorf("ResolveHelloID(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewServerTLSConfigMissingCert(t *testing.T) {
	_, err := NewServerTLSConfig("/nonexistent/cert.pem", "/nonexistent/key.pem")
	if err == nil {
		t.Fatal("expected error for missing cert files")
	}
}

func TestNewUTLSHTTPClientNotNil(t *testing.T) {
	client := NewUTLSHTTPClient(UTLSConfig{ServerName: "example.com", Fingerprint: "chrome"})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Transport == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestNewUTLSHTTPClientFingerprints(t *testing.T) {
	for _, fp := range []string{"chrome", "firefox", "safari", "random"} {
		client := NewUTLSHTTPClient(UTLSConfig{ServerName: "test.com", Fingerprint: fp})
		if client == nil {
			t.Fatalf("nil client for fingerprint %q", fp)
		}
	}
}
