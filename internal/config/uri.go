package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// URI format:
// nightveil://SERVER_PUBLIC_KEY@HOST:PORT?sid=SHORT_ID&path=PATH_PREFIX&up=UPLOAD_PATH&down=DOWNLOAD_PATH&skey=SESSION_KEY&chunk=MAX_CHUNK&fp=FINGERPRINT#REMARK
//
// Example:
// nightveil://SERVER_PUBLIC_KEY@HOST:PORT?sid=SHORT_ID&path=/prefix&up=/u/path&down=/d/path&skey=key&chunk=14336&fp=chrome#Remark

// GenerateURI creates a nightveil:// import link.
func GenerateURI(
	serverPubKey string,
	host string,
	port int,
	shortID string,
	pathPrefix string,
	uploadPath string,
	downloadPath string,
	sessionKeyName string,
	maxChunkSize int,
	fingerprint string,
	remark string,
) string {
	// Base64 URL-safe (no padding) for the key
	pubKey := strings.ReplaceAll(serverPubKey, "+", "-")
	pubKey = strings.ReplaceAll(pubKey, "/", "_")
	pubKey = strings.TrimRight(pubKey, "=")

	params := url.Values{}
	params.Set("sid", shortID)
	params.Set("path", pathPrefix)
	params.Set("up", uploadPath)
	params.Set("down", downloadPath)
	params.Set("skey", sessionKeyName)
	if maxChunkSize > 0 {
		params.Set("chunk", strconv.Itoa(maxChunkSize))
	}
	if fingerprint != "" {
		params.Set("fp", fingerprint)
	}
	// SNI defaults to host — only include if different (reality mode)
	// caller can add &sni=google.com manually

	fragment := ""
	if remark != "" {
		fragment = "#" + url.PathEscape(remark)
	}

	return fmt.Sprintf("nightveil://%s@%s:%d?%s%s",
		pubKey, host, port, params.Encode(), fragment)
}

// ParseURI parses a nightveil:// URI into a ClientConfig.
func ParseURI(uri string) (*ClientConfig, string, error) {
	// Replace scheme for url.Parse compatibility
	if !strings.HasPrefix(uri, "nightveil://") {
		return nil, "", fmt.Errorf("invalid scheme: expected nightveil://")
	}
	httpURI := "http://" + uri[len("nightveil://"):]

	u, err := url.Parse(httpURI)
	if err != nil {
		return nil, "", fmt.Errorf("parse URI: %w", err)
	}

	// Public key = username portion
	pubKeyRaw := u.User.Username()
	// Convert URL-safe base64 back to standard
	pubKey := strings.ReplaceAll(pubKeyRaw, "-", "+")
	pubKey = strings.ReplaceAll(pubKey, "_", "/")
	// Pad if needed
	switch len(pubKey) % 4 {
	case 2:
		pubKey += "=="
	case 3:
		pubKey += "="
	}

	// Validate key
	keyBytes, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil || len(keyBytes) != 32 {
		// Try RawStdEncoding
		pubKey = strings.TrimRight(pubKey, "=")
		keyBytes, err = base64.RawStdEncoding.DecodeString(pubKey)
		if err != nil || len(keyBytes) != 32 {
			return nil, "", fmt.Errorf("invalid public key")
		}
	}

	// Host:Port
	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		portStr = "443"
	}

	// Query params
	q := u.Query()
	shortID := q.Get("sid")
	pathPrefix := q.Get("path")
	uploadPath := q.Get("up")
	downloadPath := q.Get("down")
	sessionKeyName := q.Get("skey")
	fingerprint := q.Get("fp")

	maxChunkSize := 14336
	if v := q.Get("chunk"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			maxChunkSize = n
		}
	}

	// Remark from fragment
	remark := ""
	if u.Fragment != "" {
		remark, _ = url.PathUnescape(u.Fragment)
	}

	// Set defaults
	if pathPrefix == "" {
		pathPrefix = "/api"
	}
	if uploadPath == "" {
		uploadPath = "/u"
	}
	if downloadPath == "" {
		downloadPath = "/d"
	}
	if sessionKeyName == "" {
		sessionKeyName = "sid"
	}
	sni := q.Get("sni")
	skipVerify := q.Get("skip") == "1" || sni != ""

	// User private key (per-user auth)
	userPrivKeyB64 := q.Get("upk")
	if userPrivKeyB64 != "" {
		// Convert URL-safe base64 back
		userPrivKeyB64 = strings.ReplaceAll(userPrivKeyB64, "-", "+")
		userPrivKeyB64 = strings.ReplaceAll(userPrivKeyB64, "_", "/")
	}

	if fingerprint == "" {
		fingerprint = "chrome"
	}

	cfg := &ClientConfig{
		Client: ClientSettings{
			Inbound: InboundConfig{
				Type:   "socks5",
				Listen: "127.0.0.1:10809",
			},
			Server: ServerConnConfig{
				Address: host + ":" + portStr,
			},
			Auth: ClientAuthConfig{
				ServerPublicKey: base64.RawStdEncoding.EncodeToString(keyBytes),
				UserPrivateKey:  userPrivKeyB64,
				ShortID:         shortID,
			},
			Transport: TransportConfig{
				Type:           "xhttp",
				PathPrefix:     pathPrefix,
				UploadPath:     uploadPath,
				DownloadPath:   downloadPath,
				SessionKeyName: sessionKeyName,
				MaxChunkSize:   maxChunkSize,
			},
			TLS: ClientTLSConfig{
				Fingerprint: fingerprint,
				SNI:         sni,
				SkipVerify:  skipVerify,
			},
			Middleware: []MiddlewareConfig{
				{Type: "padding", MinBytes: 64, MaxBytes: 256},
			},
			AntiThrottle: AntiThrottleConfig{
				Enabled:              true,
				DetectRTTSpikeMs:     500,
				DetectThroughputDrop: 70,
				Response:             "both",
			},
		},
	}

	return cfg, remark, nil
}
