package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// config holds the optional on-disk defaults for gitling. All fields are
// optional; the zero value (empty string) means "not set" and falls through
// to the built-in default.
type config struct {
	Since  string `json:"since"`
	Color  string `json:"color"`
	Bucket string `json:"bucket"`
}

// configPath resolves the config file location, in priority order:
//  1. an explicit --config flag value (path, passed in),
//  2. the GITLING_CONFIG environment variable,
//  3. $XDG_CONFIG_HOME/gitling/config.json,
//  4. ~/.config/gitling/config.json.
func configPath(flagPath string) (string, error) {
	if flagPath != "" {
		return flagPath, nil
	}
	if p := os.Getenv("GITLING_CONFIG"); p != "" {
		return p, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "gitling", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "gitling", "config.json"), nil
}

// loadConfig reads and parses the config file at path. A missing file is not
// an error — it yields a zero-value config, since the config is entirely
// optional. A malformed file is a clear error.
func loadConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config{}, nil
		}
		return config{}, fmt.Errorf("reading config %s: %w", path, err)
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return config{}, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return c, nil
}
