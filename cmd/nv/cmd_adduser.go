package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/njkzbby/nightveil/internal/config"
	"github.com/njkzbby/nightveil/internal/crypto/auth"
	"golang.org/x/crypto/curve25519"
)

func runAdduser() {
	fs := flag.NewFlagSet("adduser", flag.ExitOnError)
	configPath := fs.String("config", "", "server config path")
	fs.Parse(os.Args[1:])

	// Name is the first non-flag argument
	name := "User"
	if fs.NArg() > 0 {
		name = fs.Arg(0)
	}

	// Find config
	paths := []string{*configPath, "/etc/nightveil/server.yaml", "/opt/nightveil/server.yaml", "server.yaml"}
	var cfg config.ServerConfig
	var cfgPath string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if err := config.Load(p, &cfg); err == nil {
			cfgPath = p
			break
		}
	}
	if cfgPath == "" {
		fmt.Fprintln(os.Stderr, "  Cannot find server.yaml. Run 'nv setup' first or use -config.")
		os.Exit(1)
	}

	s := cfg.Server

	// Derive server public key
	privKey, err := auth.DecodeKey(s.Auth.PrivateKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Invalid private key in %s: %v\n", cfgPath, err)
		os.Exit(1)
	}
	pubKey, _ := auth.DerivePublicKey(privKey)
	pubB64 := base64.RawStdEncoding.EncodeToString(pubKey[:])

	// Get server IP and port from config
	serverIP := s.ServerIP
	if serverIP == "" {
		serverIP = os.Getenv("NV_IP")
	}
	if serverIP == "" {
		serverIP = "YOUR_SERVER_IP"
	}
	port := 443
	if s.Listen != "" {
		fmt.Sscanf(s.Listen, "0.0.0.0:%d", &port)
	}

	// Generate user keypair
	userPriv := make([]byte, 32)
	rand.Read(userPriv)
	userPub, _ := curve25519.X25519(userPriv, curve25519.Basepoint)
	userPrivB64 := base64.RawStdEncoding.EncodeToString(userPriv)
	userPubB64 := base64.RawStdEncoding.EncodeToString(userPub)

	// Generate identifiers
	sidBytes := make([]byte, 4)
	rand.Read(sidBytes)
	shortID := hex.EncodeToString(sidBytes)
	pathPrefix := "/" + randomAlphaNum(6)
	uploadPath := "/u/" + randomAlphaNum(3)
	downloadPath := "/d/" + randomAlphaNum(3)
	sessionKey := randomAlphaNum(5)

	// Append user to config file
	userEntry := fmt.Sprintf("      - short_id: \"%s\"\n        public_key: \"%s\"\n        name: \"%s\"\n",
		shortID, userPubB64, name)

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Cannot read %s: %v\n", cfgPath, err)
		os.Exit(1)
	}

	content := string(data)
	// Find last user entry and append after it
	lastIdx := strings.LastIndex(content, "        name: ")
	if lastIdx >= 0 {
		// Find end of line
		eol := strings.Index(content[lastIdx:], "\n")
		if eol >= 0 {
			insertAt := lastIdx + eol + 1
			content = content[:insertAt] + userEntry + content[insertAt:]
		}
	}

	os.WriteFile(cfgPath, []byte(content), 0600)

	// Generate import link
	upkSafe := strings.ReplaceAll(userPrivB64, "+", "-")
	upkSafe = strings.ReplaceAll(upkSafe, "/", "_")

	importLink := config.GenerateURI(
		pubB64, serverIP, port, shortID,
		pathPrefix, uploadPath, downloadPath,
		sessionKey, 14336, "chrome", name,
		map[string]string{"upk": upkSafe, "skip": "1"},
	)

	fmt.Println("")
	fmt.Printf("  ✓ User \"%s\" added to %s\n", name, cfgPath)
	fmt.Println("")
	fmt.Println("  Import link (send to this user):")
	fmt.Println(" ", importLink)
	fmt.Println("")
	fmt.Println("  Restart server to apply: systemctl restart nightveil")
	fmt.Println("  Or: docker compose restart")
	fmt.Println("")
}
