package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	internalConfig "github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
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

		fmt.Printf("API Key:   %s\n", keyDisplay)
		fmt.Printf("Base URL:  %s\n", cfg.BaseURL)
		fmt.Printf("Exchange:  %s\n", cfg.Exchange)
		fmt.Printf("Output:    %s\n", cfg.Output)

		return nil
	},
}

var setCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a config value (api_key, exchange, output, base_url)",
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
		default:
			return fmt.Errorf("unknown config key: %s (valid: api_key, exchange, output, base_url)", key)
		}

		if err := internalConfig.Save(cfg); err != nil {
			return err
		}

		output.Successf("Set %s", key)
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
	Cmd.AddCommand(pathCmd)
}
