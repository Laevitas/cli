package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	DefaultBaseURL  = "https://apiv2.laevitas.ch"
	DefaultExchange = "deribit"
	DefaultOutput   = "auto" // auto = table if TTY, json if piped
	DefaultLimit    = 100

	configDirName  = "laevitas"
	configFileName = "config.json"
)

// Config holds all CLI configuration.
type Config struct {
	APIKey   string `json:"api_key,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Exchange string `json:"exchange,omitempty"`
	Output   string `json:"output,omitempty"`
}

// configDir returns ~/.config/laevitas/
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", configDirName), nil
}

// configPath returns the full path to config.json.
func configPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// Load reads config from disk, falling back to defaults.
// Environment variables override file values:
//
//	LAEVITAS_API_KEY, LAEVITAS_BASE_URL, LAEVITAS_EXCHANGE, LAEVITAS_OUTPUT
func Load() (*Config, error) {
	cfg := &Config{
		BaseURL:  DefaultBaseURL,
		Exchange: DefaultExchange,
		Output:   DefaultOutput,
	}

	// Read file if it exists
	path, err := configPath()
	if err == nil {
		data, readErr := os.ReadFile(path)
		if readErr == nil {
			_ = json.Unmarshal(data, cfg)
		}
	}

	// Env overrides
	if v := os.Getenv("LAEVITAS_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("LAEVITAS_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("LAEVITAS_EXCHANGE"); v != "" {
		cfg.Exchange = v
	}
	if v := os.Getenv("LAEVITAS_OUTPUT"); v != "" {
		cfg.Output = v
	}

	return cfg, nil
}

// Save writes config to disk.
func Save(cfg *Config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, configFileName)
	return os.WriteFile(path, data, 0600)
}

// MaskKey returns a masked version of the API key for display.
func MaskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
