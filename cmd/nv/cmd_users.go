package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/nightveil/nv/internal/config"
)

func runUsers() {
	fs := flag.NewFlagSet("users", flag.ExitOnError)
	configPath := fs.String("config", "server.yaml", "path to server config")
	fs.Parse(os.Args[1:])

	// Try common paths
	paths := []string{
		*configPath,
		"/etc/nightveil/server.yaml",
		"/opt/nightveil/server.yaml",
		"server.yaml",
	}

	var cfg config.ServerConfig
	var loaded bool
	for _, p := range paths {
		if err := config.Load(p, &cfg); err == nil {
			loaded = true
			fmt.Printf("  Config: %s\n\n", p)
			break
		}
	}

	if !loaded {
		fmt.Fprintf(os.Stderr, "  Cannot find server.yaml. Use -config path/to/server.yaml\n")
		os.Exit(1)
	}

	s := cfg.Server

	// Per-user keys
	if len(s.Auth.Users) > 0 {
		fmt.Printf("  Users (%d):\n\n", len(s.Auth.Users))
		fmt.Printf("  %-4s  %-12s  %-44s  %s\n", "#", "Short ID", "Public Key", "Name")
		fmt.Printf("  %-4s  %-12s  %-44s  %s\n", "---", "--------", "----------", "----")
		for i, u := range s.Auth.Users {
			pk := u.PublicKey
			if len(pk) > 40 {
				pk = pk[:40] + "..."
			}
			fmt.Printf("  %-4d  %-12s  %-44s  %s\n", i+1, u.ShortID, pk, u.Name)
		}
	}

	// Legacy shortIDs
	if len(s.Auth.ShortIDs) > 0 {
		fmt.Printf("\n  Legacy users (shortID only, no per-user key):\n")
		for i, id := range s.Auth.ShortIDs {
			fmt.Printf("  %d) %s\n", i+1, id)
		}
	}

	if len(s.Auth.Users) == 0 && len(s.Auth.ShortIDs) == 0 {
		fmt.Println("  No users configured.")
	}

	fmt.Println()
}
