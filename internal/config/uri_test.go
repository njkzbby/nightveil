package config

import (
	"strings"
	"testing"
)

func TestGenerateURI(t *testing.T) {
	uri := GenerateURI(
		"dGVzdHB1YmxpY2tleXRoYXRpczMyYnl0ZXNs",
		"example.com", 443,
		"aabb1122",
		"/test", "/u/up", "/d/dn",
		"skey",
		14336,
		"chrome",
		"Test Server",
		nil,
	)

	if !strings.HasPrefix(uri, "nightveil://") {
		t.Fatalf("bad prefix: %s", uri)
	}
	if !strings.Contains(uri, "@example.com:443") {
		t.Fatalf("missing host: %s", uri)
	}
	if !strings.Contains(uri, "sid=aabb1122") {
		t.Fatalf("missing sid: %s", uri)
	}
	if !strings.Contains(uri, "Test") {
		t.Fatalf("missing remark: %s", uri)
	}
}

func TestParseURI(t *testing.T) {
	uri := GenerateURI(
		"MJqcB1wHvED6O0q4mTYydwXVcUhmSuP5/hNBQVWMTCA",
		"example.com", 443,
		"abcd1234",
		"/test", "/u/up", "/d/dn",
		"mysid",
		12000,
		"firefox",
		"My Server",
		nil,
	)

	cfg, remark, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("parse: %v\nURI: %s", err, uri)
	}

	if remark != "My Server" {
		t.Errorf("remark: %q", remark)
	}
	if cfg.Client.Server.Address != "example.com:443" {
		t.Errorf("server: %q", cfg.Client.Server.Address)
	}
	if cfg.Client.Auth.ShortID != "abcd1234" {
		t.Errorf("shortID: %q", cfg.Client.Auth.ShortID)
	}
	if cfg.Client.Transport.PathPrefix != "/test" {
		t.Errorf("path: %q", cfg.Client.Transport.PathPrefix)
	}
	if cfg.Client.Transport.UploadPath != "/u/up" {
		t.Errorf("upload: %q", cfg.Client.Transport.UploadPath)
	}
	if cfg.Client.Transport.DownloadPath != "/d/dn" {
		t.Errorf("download: %q", cfg.Client.Transport.DownloadPath)
	}
	if cfg.Client.Transport.SessionKeyName != "mysid" {
		t.Errorf("skey: %q", cfg.Client.Transport.SessionKeyName)
	}
	if cfg.Client.Transport.MaxChunkSize != 12000 {
		t.Errorf("chunk: %d", cfg.Client.Transport.MaxChunkSize)
	}
	if cfg.Client.TLS.Fingerprint != "firefox" {
		t.Errorf("fp: %q", cfg.Client.TLS.Fingerprint)
	}
	if !cfg.Client.AntiThrottle.Enabled {
		t.Error("anti-throttle should be enabled by default")
	}
}

func TestParseURIDefaults(t *testing.T) {
	// Minimal URI — only key, host, shortID
	uri := "nightveil://MJqcB1wHvED6O0q4mTYydwXVcUhmSuP5_hNBQVWMTCA@example.com:443?sid=ab"

	cfg, _, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Client.Transport.PathPrefix != "/api" {
		t.Errorf("default path: %q", cfg.Client.Transport.PathPrefix)
	}
	if cfg.Client.TLS.Fingerprint != "chrome" {
		t.Errorf("default fp: %q", cfg.Client.TLS.Fingerprint)
	}
	if cfg.Client.Inbound.Listen != "127.0.0.1:10809" {
		t.Errorf("default listen: %q", cfg.Client.Inbound.Listen)
	}
}

func TestParseURIBadScheme(t *testing.T) {
	_, _, err := ParseURI("vless://something@host:443")
	if err == nil {
		t.Fatal("expected error for wrong scheme")
	}
}

func TestParseURIBadKey(t *testing.T) {
	_, _, err := ParseURI("nightveil://badkey@host:443?sid=ab")
	if err == nil {
		t.Fatal("expected error for bad key")
	}
}

func TestRoundTripURI(t *testing.T) {
	// Generate → Parse → verify fields match
	uri := GenerateURI(
		"MJqcB1wHvED6O0q4mTYydwXVcUhmSuP5/hNBQVWMTCA",
		"10.0.0.1", 8443,
		"deadbeef",
		"/path1", "/u/a", "/d/b",
		"sk",
		9000,
		"safari",
		"Test",
		nil,
	)

	cfg, remark, err := ParseURI(uri)
	if err != nil {
		t.Fatalf("round-trip parse: %v", err)
	}

	if remark != "Test" {
		t.Errorf("remark: %q", remark)
	}
	if cfg.Client.Server.Address != "10.0.0.1:8443" {
		t.Errorf("addr: %q", cfg.Client.Server.Address)
	}
	if cfg.Client.Auth.ShortID != "deadbeef" {
		t.Errorf("sid: %q", cfg.Client.Auth.ShortID)
	}
	if cfg.Client.Transport.MaxChunkSize != 9000 {
		t.Errorf("chunk: %d", cfg.Client.Transport.MaxChunkSize)
	}
}

func TestGenerateURIWithExtraParams(t *testing.T) {
	uri := GenerateURI(
		"dGVzdGtleQ",
		"example.com", 443,
		"aabb",
		"/p", "/u/x", "/d/y",
		"s",
		14336,
		"chrome",
		"My Server",
		map[string]string{"upk": "userkey123", "skip": "1"},
	)

	// Fragment (#Remark) must be LAST — not before upk/skip
	if !strings.HasSuffix(uri, "#My%20Server") {
		t.Fatalf("fragment not at end: %s", uri)
	}
	if !strings.Contains(uri, "upk=userkey123") {
		t.Fatalf("missing upk: %s", uri)
	}
	if !strings.Contains(uri, "skip=1") {
		t.Fatalf("missing skip: %s", uri)
	}
	// upk must be before # (in query, not fragment)
	hashIdx := strings.Index(uri, "#")
	upkIdx := strings.Index(uri, "upk=")
	if upkIdx > hashIdx {
		t.Fatalf("upk is after # (in fragment, should be in query): %s", uri)
	}
}

func TestGenerateURIParsesWithUpk(t *testing.T) {
	uri := GenerateURI(
		"MJqcB1wHvED6O0q4mTYydwXVcUhmSuP5/hNBQVWMTCA",
		"example.com", 443,
		"abcd",
		"/test", "/u/up", "/d/dn",
		"key",
		14336,
		"chrome",
		"Tolya",
		map[string]string{"upk": "myprivatekey123", "skip": "1"},
	)

	cfg, remark, err := ParseURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	// Remark must be clean — no &upk=... appended
	if remark != "Tolya" {
		t.Fatalf("remark should be 'Tolya', got '%s'", remark)
	}
	if cfg.Client.Auth.UserPrivateKey != "myprivatekey123" {
		t.Fatalf("upk: got '%s'", cfg.Client.Auth.UserPrivateKey)
	}
}
