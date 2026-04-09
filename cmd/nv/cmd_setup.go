package main

import (
	"bufio"
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
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/njkzbby/nightveil/internal/config"
	"golang.org/x/crypto/curve25519"
)

func runSetup() {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	configDir := fs.String("dir", "/etc/nightveil", "config directory")
	fs.Parse(os.Args[1:])

	configPath := *configDir + "/server.yaml"
	certPath := *configDir + "/cert.pem"
	keyPath := *configDir + "/key.pem"
	linkPath := *configDir + "/import.txt"

	// Check if already configured
	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("")
		fmt.Println("  Server already configured.")
		showImportLink(linkPath)
		fmt.Println("  To reconfigure, delete", configPath, "and run setup again.")
		return
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("")
	fmt.Println("  =========================================")
	fmt.Println("   Nightveil Server Setup")
	fmt.Println("  =========================================")
	fmt.Println("")

	// Detect IP
	detectedIP := detectPublicIP()
	fmt.Printf("  Detecting public IP... %s\n\n", detectedIP)

	// [1/4] IP
	serverIP := prompt(reader, "  [1/4] Server IP", detectedIP)

	// [2/4] Port
	portStr := prompt(reader, "  [2/4] Port", "443")
	port := 443
	fmt.Sscanf(portStr, "%d", &port)

	// [3/4] REALITY
	fmt.Println("")
	fmt.Println("  [3/4] REALITY mode — server impersonates a real website.")
	fmt.Println("        Probes see google.com (or your choice).")
	fmt.Println("        Leave empty to skip REALITY.")
	realityDest := prompt(reader, "        Target site", "google.com:443")

	// [4/4] Name
	serverName := prompt(reader, "  [4/4] Server name", "Nightveil")

	fmt.Println("")
	fmt.Println("  Generating keys and certificates...")

	os.MkdirAll(*configDir, 0700)

	// Generate server keypair
	privKey := make([]byte, 32)
	rand.Read(privKey)
	pubKey, _ := curve25519.X25519(privKey, curve25519.Basepoint)
	privB64 := base64.RawStdEncoding.EncodeToString(privKey)
	pubB64 := base64.RawStdEncoding.EncodeToString(pubKey)

	// Generate user keypair
	userPrivKey := make([]byte, 32)
	rand.Read(userPrivKey)
	userPubKey, _ := curve25519.X25519(userPrivKey, curve25519.Basepoint)
	userPrivB64 := base64.RawStdEncoding.EncodeToString(userPrivKey)
	userPubB64 := base64.RawStdEncoding.EncodeToString(userPubKey)

	// Generate identifiers
	sidBytes := make([]byte, 4)
	rand.Read(sidBytes)
	shortID := hex.EncodeToString(sidBytes)
	pathPrefix := "/" + randAlpha(6)
	uploadPath := "/u/" + randAlpha(3)
	downloadPath := "/d/" + randAlpha(3)
	sessionKey := randAlpha(5)

	// Generate TLS cert
	generateSelfSignedCert(certPath, keyPath, serverIP)

	// REALITY config
	destLine := ""
	if realityDest != "" {
		if !strings.Contains(realityDest, ":") {
			realityDest += ":443"
		}
		destLine = fmt.Sprintf("\n    dest: \"%s\"", realityDest)
	}

	// Write config
	yaml := fmt.Sprintf(`server:
  listen: "0.0.0.0:%d"
  server_ip: "%s"
  tls:
    cert_file: "%s"
    key_file: "%s"%s
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
`, port, serverIP, certPath, keyPath, destLine, privB64, shortID, userPubB64, serverName)

	os.WriteFile(configPath, []byte(yaml), 0600)

	// Generate import link
	upkSafe := strings.ReplaceAll(userPrivB64, "+", "-")
	upkSafe = strings.ReplaceAll(upkSafe, "/", "_")

	extra := map[string]string{"upk": upkSafe, "skip": "1"}
	importLink := config.GenerateURI(
		pubB64, serverIP, port, shortID,
		pathPrefix, uploadPath, downloadPath,
		sessionKey, 14336, "chrome", serverName,
		extra,
	)

	os.WriteFile(linkPath, []byte(importLink+"\n"), 0644)

	// Save link per user
	linksDir := *configDir + "/links"
	os.MkdirAll(linksDir, 0700)
	os.WriteFile(linksDir+"/"+shortID+".txt", []byte(serverName+"\n"+importLink+"\n"), 0600)

	fmt.Println("")
	fmt.Println("  ✓ Server configured!")
	fmt.Println("")
	fmt.Println("  ════════════════════════════════════════════")
	fmt.Println("  Import link (copy and send to users):")
	fmt.Println("")
	fmt.Println(" ", importLink)
	fmt.Println("")
	fmt.Println("  ════════════════════════════════════════════")
	fmt.Println("")
	fmt.Println("  Next steps:")
	fmt.Println("    docker compose up -d       # start server")
	fmt.Println("    nv adduser \"Friend\"         # add more users")
	fmt.Println("    nv users                    # list users")
	fmt.Println("    nv link                     # show import link")
	fmt.Println("")
}

func prompt(reader *bufio.Reader, label, defaultVal string) string {
	if defaultVal != "" {
		fmt.Printf("%s [%s]: ", label, defaultVal)
	} else {
		fmt.Printf("%s: ", label)
	}
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}

func detectPublicIP() string {
	// Same approach as 3x-ui: try multiple APIs with 3s timeout
	services := []string{
		"https://api4.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://4.ident.me",
	}

	client := &http.Client{Timeout: 3 * time.Second}
	for _, url := range services {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		body := make([]byte, 64)
		n, _ := resp.Body.Read(body)
		resp.Body.Close()
		ip := strings.TrimSpace(string(body[:n]))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}

	// Fallback to interface detection
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			ip := ipnet.IP.String()
			if !strings.HasPrefix(ip, "10.") && !strings.HasPrefix(ip, "172.") && !strings.HasPrefix(ip, "192.168.") {
				return ip
			}
		}
	}

	return "YOUR_IP"
}

func generateSelfSignedCert(certPath, keyPath, ip string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Nightveil"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
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
