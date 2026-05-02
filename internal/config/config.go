// Package config persists the user's xml_path + taxonomy_name across runs in a
// JSON file inside os.UserConfigDir().
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	dirName  = "dcallocate"
	fileName = "config.json"
)

// Config is the persisted user configuration.
type Config struct {
	XMLPath      string `json:"xml_path"`
	TaxonomyName string `json:"taxonomy_name"`
}

// DefaultDir returns the default directory for config storage:
//   - Linux:   $XDG_CONFIG_HOME/dcallocate or ~/.config/dcallocate
//   - macOS:   ~/Library/Application Support/dcallocate
//   - Windows: %AppData%\dcallocate
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(base, dirName), nil
}

// Path returns the full path to the config file inside dir.
func Path(dir string) string {
	return filepath.Join(dir, fileName)
}

// Load reads the config file from dir. If the file does not exist, returns
// (Config{}, nil); the caller decides whether that's an error.
func Load(dir string) (Config, error) {
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return c, nil
}

// Save writes the config to dir, creating the directory if needed.
func Save(c Config, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(Path(dir), b, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
