// nv — Nightveil CLI
//
// Usage:
//
//	nv server  -config server.yaml
//	nv connect "nightveil://..."
//	nv connect -config client.yaml
//	nv keygen  -server host:443
//	nv keygen  -server host:443 -pubkey KEY -remark "Name"
//	nv status
//	nv version
package main

import (
	"fmt"
	"os"
)

const version = "0.2.0"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	// Shift args so subcommands see their own flags
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)

	switch command {
	case "server":
		runServer()
	case "connect":
		runConnect()
	case "setup":
		runSetup()
	case "adduser":
		runAdduser()
	case "link":
		runLink()
	case "keygen":
		runKeygen()
	case "init":
		runInit()
	case "users":
		runUsers()
	case "status":
		runStatus()
	case "version", "-v", "--version":
		fmt.Printf("nightveil v%s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		// Check if it's a nightveil:// URI
		if len(command) > 12 && command[:12] == "nightveil://" {
			os.Args = append([]string{os.Args[0], command}, os.Args[1:]...)
			runConnect()
		} else {
			fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", command)
			printUsage()
			os.Exit(1)
		}
	}
}

func printUsage() {
	fmt.Print(`Nightveil — Anti-censorship proxy protocol

Usage:
  nv <command> [options]

Commands:
  setup     Interactive server setup (recommended first step)
  adduser   Add a new user (reads config automatically)
  link      Show import link
  users     List registered users
  server    Start the Nightveil server
  connect   Connect to a server (via URI or config file)
  keygen    Generate keys and import links (advanced)
  init      Non-interactive init (for scripting)
  version   Show version

Examples:
  nv setup                                  # interactive setup
  nv adduser "Alice"                        # add user, prints import link
  nv link                                   # show import link
  nv users                                  # list users
  nv server -config server.yaml             # start server
  nv connect "nightveil://...#Name"         # connect via link

`)
}
