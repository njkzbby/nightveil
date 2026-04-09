package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/nightveil/nv/internal/config"
	"github.com/nightveil/nv/internal/crypto/auth"
	"golang.org/x/crypto/curve25519"
)

func runKeygen() {
	fs := flag.NewFlagSet("keygen", flag.ExitOnError)
	serverAddr := fs.String("server", "", "server address (host:port)")
	remark := fs.String("remark", "Nightveil", "display name")
	existingPubKey := fs.String("pubkey", "", "existing server public key (add user mode)")
	fs.Parse(os.Args[1:])

	// Per-client unique parameters
	shortIDBytes := make([]byte, 4)
	rand.Read(shortIDBytes)
	shortID := hex.EncodeToString(shortIDBytes)
	pathPrefix := "/" + randomAlphaNum(6)
	uploadPath := "/u/" + randomAlphaNum(3)
	downloadPath := "/d/" + randomAlphaNum(3)
	sessionKeyName := randomAlphaNum(5)

	// Always generate per-user keypair
	userPriv, userPub, _ := auth.GenerateUserKeypair()
	userPrivB64 := base64.RawStdEncoding.EncodeToString(userPriv[:])
	userPubB64 := base64.RawStdEncoding.EncodeToString(userPub[:])

	var serverPubB64 string

	if *existingPubKey != "" {
		// --- Add user to existing server ---
		serverPubB64 = *existingPubKey

		fmt.Println("=== Add User ===")
		fmt.Println()
		fmt.Println("Add to server.yaml users section:")
		fmt.Printf("  short_id:    \"%s\"\n", shortID)
		fmt.Printf("  public_key:  \"%s\"\n", userPubB64)
		fmt.Printf("  name:        \"%s\"\n", *remark)
	} else {
		// --- New server ---
		serverPriv := make([]byte, 32)
		rand.Read(serverPriv)
		serverPub, _ := curve25519.X25519(serverPriv, curve25519.Basepoint)
		serverPubB64 = base64.RawStdEncoding.EncodeToString(serverPub)
		serverPrivB64 := base64.RawStdEncoding.EncodeToString(serverPriv)

		fmt.Println("=== New Server + First User ===")
		fmt.Println()
		fmt.Println("Server config (server.yaml):")
		fmt.Printf("  private_key: \"%s\"\n", serverPrivB64)
		fmt.Printf("  users:\n")
		fmt.Printf("    - short_id:   \"%s\"\n", shortID)
		fmt.Printf("      public_key: \"%s\"\n", userPubB64)
		fmt.Printf("      name:       \"%s\"\n", *remark)
		fmt.Println()
		fmt.Printf("Server public key (for adding users later):\n  %s\n", serverPubB64)
	}

	fmt.Println()
	fmt.Println("User credentials:")
	fmt.Printf("  user_private_key: \"%s\"\n", userPrivB64)
	fmt.Printf("  short_id:         \"%s\"\n", shortID)

	// Import link
	if *serverAddr != "" {
		host, port := parseHostPort(*serverAddr)

		// Include user_private_key in URI as &upk=...
		uri := config.GenerateURI(
			serverPubB64, host, port, shortID,
			pathPrefix, uploadPath, downloadPath,
			sessionKeyName, 14336, "chrome", *remark,
		)
		uri += "&upk=" + base64UrlSafe(userPrivB64)

		fmt.Println()
		fmt.Println("Import link (send to this user):")
		fmt.Println(uri)
	} else {
		fmt.Println()
		fmt.Println("Add -server host:443 for import link")
	}
}

func runStatus() {
	fmt.Println("nightveil status: not connected")
	fmt.Println("(connect first with: nv connect \"nightveil://...\")")
}

func parseHostPort(addr string) (string, int) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host := addr[:i]
			port := 443
			fmt.Sscanf(addr[i+1:], "%d", &port)
			return host, port
		}
	}
	return addr, 443
}

func randomAlphaNum(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	var sb strings.Builder
	for i := 0; i < n; i++ {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		sb.WriteByte(charset[idx.Int64()])
	}
	return sb.String()
}

func base64UrlSafe(s string) string {
	s = strings.ReplaceAll(s, "+", "-")
	s = strings.ReplaceAll(s, "/", "_")
	return strings.TrimRight(s, "=")
}
