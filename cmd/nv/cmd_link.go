package main

import (
	"fmt"
	"os"
	"strings"
)

func runLink() {
	paths := []string{
		"/etc/nightveil/import.txt",
		"/opt/nightveil/import.txt",
		"import.txt",
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			fmt.Println("")
			fmt.Println("  Import link:")
			fmt.Println(" ", strings.TrimSpace(string(data)))
			fmt.Println("")
			return
		}
	}

	fmt.Fprintln(os.Stderr, "  No import link found. Run 'nv setup' first.")
}
