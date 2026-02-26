package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	internalConfig "github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/x402"
)

// Cmd is the top-level "config" command.
var Cmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration",
	Long:  "Configure API key, default exchange, output format, and base URL.",
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup — configure your API key and defaults",
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("🔧 LAEVITAS CLI Setup")
		fmt.Println()

		// Load existing config
		cfg, _ := internalConfig.Load()

		// API Key
		fmt.Print("API Key")
		if cfg.APIKey != "" {
			fmt.Printf(" [current: %s]", internalConfig.MaskKey(cfg.APIKey))
		}
		fmt.Print(": ")
		key, _ := reader.ReadString('\n')
		key = strings.TrimSpace(key)
		if key != "" {
			cfg.APIKey = key
		}

		// Default exchange
		fmt.Printf("Default exchange (deribit/binance) [%s]: ", cfg.Exchange)
		ex, _ := reader.ReadString('\n')
		ex = strings.TrimSpace(ex)
		if ex != "" {
			cfg.Exchange = ex
		}

		// Output format
		fmt.Printf("Default output format (auto/json/table/csv) [%s]: ", cfg.Output)
		out, _ := reader.ReadString('\n')
		out = strings.TrimSpace(out)
		if out != "" {
			cfg.Output = out
		}

		// Wallet key (x402 payments)
		fmt.Print("Wallet key (EVM private key for x402 payments)")
		if cfg.WalletKey != "" {
			fmt.Printf(" [current: %s]", internalConfig.MaskKey(cfg.WalletKey))
		}
		fmt.Print(" (optional): ")
		wk, _ := reader.ReadString('\n')
		wk = strings.TrimSpace(wk)
		if wk != "" {
			cfg.WalletKey = wk
		}

		// Auth type
		currentAuth := cfg.Auth
		if currentAuth == "" {
			currentAuth = "auto"
		}
		fmt.Printf("Auth type (auto/api-key/x402) [%s]: ", currentAuth)
		auth, _ := reader.ReadString('\n')
		auth = strings.TrimSpace(auth)
		if auth != "" {
			cfg.Auth = auth
		}

		// Base URL
		fmt.Printf("API base URL [%s]: ", cfg.BaseURL)
		url, _ := reader.ReadString('\n')
		url = strings.TrimSpace(url)
		if url != "" {
			cfg.BaseURL = url
		}

		if err := internalConfig.Save(cfg); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		fmt.Println()
		output.Successf("Configuration saved to ~/.config/laevitas/config.json")

		// Verify API key
		if cfg.APIKey != "" {
			fmt.Print("Verifying API key... ")
			client := api.NewClient(cfg)
			_, err := client.Get(api.Health, nil)
			if err != nil {
				fmt.Println("✗")
				output.Warnf("API key verification failed: %v", err)
			} else {
				fmt.Println("✓")
				output.Successf("API key is valid")
			}
		}

		return nil
	},
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Display current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := internalConfig.Load()
		if err != nil {
			return err
		}

		keyDisplay := "(not set)"
		if cfg.APIKey != "" {
			keyDisplay = internalConfig.MaskKey(cfg.APIKey)
		}

		authDisplay := cfg.Auth
		if authDisplay == "" {
			authDisplay = "auto"
		}

		fmt.Printf("API Key:    %s\n", keyDisplay)
		fmt.Printf("Base URL:   %s\n", cfg.BaseURL)
		fmt.Printf("Exchange:   %s\n", cfg.Exchange)
		fmt.Printf("Output:     %s\n", cfg.Output)
		fmt.Printf("Auth:       %s\n", authDisplay)

		// x402 payment info
		if cfg.WalletKey != "" {
			pc, err := x402.NewPaymentClient(cfg.WalletKey)
			if err != nil {
				fmt.Printf("Wallet:     (invalid key)\n")
			} else {
				fmt.Printf("Wallet:     %s\n", pc.Address())
			}
			token := internalConfig.LoadCreditToken()
			if token != "" {
				fmt.Printf("x402 Token: %s...%s\n", token[:10], token[len(token)-6:])
			} else {
				fmt.Printf("x402 Token: (none)\n")
			}
		}

		return nil
	},
}

var setCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value (api_key, exchange, output, base_url, wallet_key, auth)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := internalConfig.Load()
		if err != nil {
			return err
		}

		key, value := args[0], args[1]

		switch strings.ToLower(key) {
		case "api_key", "apikey", "key":
			cfg.APIKey = value
		case "exchange":
			cfg.Exchange = value
		case "output":
			cfg.Output = value
		case "base_url", "baseurl", "url":
			cfg.BaseURL = value
		case "wallet_key", "walletkey", "wallet":
			cfg.WalletKey = value
		case "auth", "auth_type":
			switch strings.ToLower(value) {
			case "auto", "api-key", "apikey", "x402", "wallet":
				if value == "apikey" {
					value = "api-key"
				}
				if value == "wallet" {
					value = "x402"
				}
				cfg.Auth = value
			default:
				return fmt.Errorf("invalid auth type: %s (valid: auto, api-key, x402)", value)
			}
		default:
			return fmt.Errorf("unknown config key: %s (valid: api_key, exchange, output, base_url, wallet_key, auth)", key)
		}

		if err := internalConfig.Save(cfg); err != nil {
			return err
		}

		// Reset shared client when auth-related config changes in REPL mode
		switch strings.ToLower(key) {
		case "api_key", "apikey", "key", "wallet_key", "walletkey", "wallet", "auth", "auth_type":
			cmdutil.SharedClient = nil
		}

		output.Successf("Set %s", key)
		return nil
	},
}

var unsetCmd = &cobra.Command{
	Use:   "unset <key>",
	Short: "Clear a config value (api_key, wallet_key)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := internalConfig.Load()
		if err != nil {
			return err
		}

		key := args[0]

		switch strings.ToLower(key) {
		case "api_key", "apikey", "key":
			cfg.APIKey = ""
		case "wallet_key", "walletkey", "wallet":
			cfg.WalletKey = ""
			internalConfig.ClearCreditToken()
		default:
			return fmt.Errorf("unknown config key: %s (valid: api_key, wallet_key)", key)
		}

		if err := internalConfig.Save(cfg); err != nil {
			return err
		}

		// Reset shared client so next command picks up the change
		cmdutil.SharedClient = nil

		output.Successf("Cleared %s", key)
		return nil
	},
}

var pathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	Run: func(cmd *cobra.Command, args []string) {
		home, _ := os.UserHomeDir()
		fmt.Printf("%s/.config/laevitas/config.json\n", home)
	},
}

func init() {
	Cmd.AddCommand(initCmd)
	Cmd.AddCommand(showCmd)
	Cmd.AddCommand(setCmd)
	Cmd.AddCommand(unsetCmd)
	Cmd.AddCommand(pathCmd)
}
