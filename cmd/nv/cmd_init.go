package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/njkzbby/nightveil/internal/config"
	"golang.org/x/crypto/curve25519"
)

// runInit auto-generates everything needed to run a server.
// Designed to run inside Docker on first start.
func runInit() {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	port := fs.Int("port", 443, "listen port")
	name := fs.String("name", "Nightveil", "server display name")
	configDir := fs.String("dir", "/etc/nightveil", "config directory")
	fs.Parse(os.Args[1:])

	configPath := *configDir + "/server.yaml"
	certPath := *configDir + "/cert.pem"
	keyPath := *configDir + "/key.pem"
	linkPath := *configDir + "/import.txt"

	// Skip if already initialized
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Already initialized. Config exists at", configPath)
		showImportLink(linkPath)
		return
	}

	os.MkdirAll(*configDir, 0700)

	// Detect public IP
	serverIP := detectIP()
	fmt.Printf("  Server IP: %s\n", serverIP)

	// Generate X25519 keypair
	privKey := make([]byte, 32)
	rand.Read(privKey)
	pubKey, _ := curve25519.X25519(privKey, curve25519.Basepoint)
	privB64 := base64.RawStdEncoding.EncodeToString(privKey)
	pubB64 := base64.RawStdEncoding.EncodeToString(pubKey)

	// Generate shortID
	sidBytes := make([]byte, 4)
	rand.Read(sidBytes)
	shortID := hex.EncodeToString(sidBytes)

	// Generate per-user keypair
	userPrivKey := make([]byte, 32)
	rand.Read(userPrivKey)
	userPubKey, _ := curve25519.X25519(userPrivKey, curve25519.Basepoint)
	userPrivB64 := base64.RawStdEncoding.EncodeToString(userPrivKey)
	userPubB64 := base64.RawStdEncoding.EncodeToString(userPubKey)

	// Generate per-client params
	pathPrefix := "/" + randAlpha(6)
	uploadPath := "/u/" + randAlpha(3)
	downloadPath := "/d/" + randAlpha(3)
	sessionKey := randAlpha(5)

	// Generate self-signed TLS cert
	generateCert(certPath, keyPath, serverIP)

	// Write server config (with per-user key)
	yaml := fmt.Sprintf(`server:
  listen: "0.0.0.0:%d"
  tls:
    cert_file: "%s"
    key_file: "%s"
    # REALITY mode — uncomment to forward probes to a real site:
    # dest: "google.com:443"
  auth:
    private_key: "%s"
    max_time_diff: 120
    users:
      - short_id: "%s"
        public_key: "%s"
        name: "%s"
  transport:
    type: "xhttp"
    max_chunk_size: 14336
    session_timeout: 30
  middleware:
    - type: "padding"
      min_bytes: 64
      max_bytes: 256
  fallback:
    mode: "default"
`, *port, certPath, keyPath, privB64, shortID, userPubB64, *name)

	os.WriteFile(configPath, []byte(yaml), 0600)

	// Generate import link (with per-user private key)
	upkSafe := strings.ReplaceAll(userPrivB64, "+", "-")
	upkSafe = strings.ReplaceAll(upkSafe, "/", "_")

	importLink := config.GenerateURI(
		pubB64, serverIP, *port, shortID,
		pathPrefix, uploadPath, downloadPath,
		sessionKey, 14336, "chrome", *name,
		map[string]string{"upk": upkSafe, "skip": "1"},
	)

	// Save import link
	os.WriteFile(linkPath, []byte(importLink+"\n"), 0644)

	fmt.Println("")
	fmt.Println("  ========================================")
	fmt.Println("  Nightveil server initialized!")
	fmt.Println("  ========================================")
	fmt.Println("")
	fmt.Printf("  Port: %d\n", *port)
	fmt.Printf("  Users: 1\n")
	fmt.Println("")
	fmt.Println("  Import link (send to users):")
	fmt.Println(" ", importLink)
	fmt.Println("")
	fmt.Printf("  Public key (for adding users): %s\n", pubB64)
	fmt.Println("")
}

func showImportLink(path string) {
	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		fmt.Println("")
		fmt.Println("  Import link:")
		fmt.Println(" ", strings.TrimSpace(string(data)))
		fmt.Println("")
	}
}

func detectIP() string {
	// Priority 1: NV_IP environment variable (best for Docker)
	if ip := os.Getenv("NV_IP"); ip != "" {
		return ip
	}

	// Priority 2: Network interfaces (skip private/Docker IPs)
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ip := ipnet.IP.String()
			if !strings.HasPrefix(ip, "10.") && !strings.HasPrefix(ip, "172.") && !strings.HasPrefix(ip, "192.168.") {
				return ip
			}
		}
	}

	// Priority 3: Placeholder — user must replace in import link
	return "YOUR_SERVER_IP"
}

func generateCert(certPath, keyPath, ip string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Nightveil"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"nightveil.local"},
	}
	if parsedIP := net.ParseIP(ip); parsedIP != nil {
		template.IPAddresses = []net.IP{parsedIP}
	}

	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)

	certFile, _ := os.Create(certPath)
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certFile.Close()

	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyFile, _ := os.Create(keyPath)
	pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyFile.Close()
}

func randAlpha(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[idx.Int64()]
	}
	return string(b)
}
