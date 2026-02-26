package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultBaseURL  = "https://apiv2.laevitas.ch"
	DefaultExchange = "deribit"
	DefaultOutput   = "auto" // auto = table if TTY, json if piped
	DefaultLimit    = 100

	configDirName  = "laevitas"
	configFileName = "config.json"
)

// Auth type constants for choosing default authentication method.
const (
	AuthTypeAuto   = "auto"   // API key if set, otherwise x402 wallet
	AuthTypeAPIKey = "api-key" // Always use API key
	AuthTypeX402   = "x402"   // Always use x402 wallet payment
)

// Config holds all CLI configuration.
type Config struct {
	APIKey    string `json:"api_key,omitempty"`
	BaseURL   string `json:"base_url,omitempty"`
	Exchange  string `json:"exchange,omitempty"`
	Output    string `json:"output,omitempty"`
	WalletKey string `json:"wallet_key,omitempty"` // EVM private key for x402 payments
	Auth      string `json:"auth,omitempty"`       // "auto", "api-key", or "x402"
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
		BaseURL: DefaultBaseURL,
		Output:  DefaultOutput,
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
	if v := os.Getenv("LAEVITAS_WALLET_KEY"); v != "" {
		cfg.WalletKey = v
	}
	if v := os.Getenv("LAEVITAS_AUTH"); v != "" {
		cfg.Auth = v
	}

	return cfg, nil
}

// ─── Credit token storage (x402) ────────────────────────────────────────────

const creditTokenFile = "x402-token"

// LoadCreditToken reads the cached x402 credit token from disk.
func LoadCreditToken() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, creditTokenFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveCreditToken writes the x402 credit token to disk.
func SaveCreditToken(token string) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, creditTokenFile), []byte(token), 0600)
}

// ClearCreditToken removes the cached x402 credit token.
func ClearCreditToken() {
	dir, err := configDir()
	if err != nil {
		return
	}
	os.Remove(filepath.Join(dir, creditTokenFile))
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
