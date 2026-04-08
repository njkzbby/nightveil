// Keygen generates X25519 keypair, shortID, and per-client unique parameters.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"

	"golang.org/x/crypto/curve25519"
)

func main() {
	// Generate X25519 keypair
	privateKey := make([]byte, 32)
	if _, err := rand.Read(privateKey); err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate private key: %v\n", err)
		os.Exit(1)
	}

	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to derive public key: %v\n", err)
		os.Exit(1)
	}

	// Generate shortID (8 random hex chars)
	shortIDBytes := make([]byte, 4)
	rand.Read(shortIDBytes)
	shortID := hex.EncodeToString(shortIDBytes)

	// Generate per-client unique parameters
	pathPrefix := "/" + randomAlphaNum(6)
	uploadPath := "/u/" + randomAlphaNum(3)
	downloadPath := "/d/" + randomAlphaNum(3)
	sessionKeyName := randomAlphaNum(5)

	fmt.Println("=== Nightveil Key Generation ===")
	fmt.Println()
	fmt.Println("# Server config:")
	fmt.Printf("  private_key: \"%s\"\n", base64.RawStdEncoding.EncodeToString(privateKey))
	fmt.Printf("  short_ids: [\"%s\"]\n", shortID)
	fmt.Println()
	fmt.Println("# Client config:")
	fmt.Printf("  server_public_key: \"%s\"\n", base64.RawStdEncoding.EncodeToString(publicKey))
	fmt.Printf("  short_id: \"%s\"\n", shortID)
	fmt.Println()
	fmt.Println("# Per-client unique parameters (client transport config):")
	fmt.Printf("  path_prefix: \"%s\"\n", pathPrefix)
	fmt.Printf("  upload_path: \"%s\"\n", uploadPath)
	fmt.Printf("  download_path: \"%s\"\n", downloadPath)
	fmt.Printf("  session_key_name: \"%s\"\n", sessionKeyName)
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
