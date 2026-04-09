package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runLink() {
	// Check links directory
	linksDirs := []string{
		"/etc/nightveil/links",
		"/opt/nightveil/links",
		"links",
	}

	for _, dir := range linksDirs {
		files, err := filepath.Glob(dir + "/*.txt")
		if err != nil || len(files) == 0 {
			continue
		}

		fmt.Println("")
		fmt.Printf("  Import links (%d users):\n", len(files))
		fmt.Println("")

		for _, f := range files {
			data, _ := os.ReadFile(f)
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			name := filepath.Base(f)
			link := ""
			if len(lines) >= 2 {
				name = lines[0]
				link = lines[1]
			} else if len(lines) == 1 {
				link = lines[0]
			}
			fmt.Printf("  %s:\n  %s\n\n", name, link)
		}
		return
	}

	// Fallback: old import.txt
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

	fmt.Fprintln(os.Stderr, "  No import links found. Run 'nv setup' first.")
}
