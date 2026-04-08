// Package config handles YAML configuration loading and validation.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads a YAML file and unmarshals into dst.
func Load(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}
