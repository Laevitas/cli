//go:build js && wasm

package cmd

import (
	"encoding/json"

	"github.com/laevitas/cli/cmd/agent"
	configcmd "github.com/laevitas/cli/cmd/config"
	"github.com/laevitas/cli/cmd/update"
	"github.com/laevitas/cli/cmd/wallet"
	"github.com/laevitas/cli/internal/agentclient"
	"github.com/laevitas/cli/internal/browser"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/version"
	"github.com/spf13/cobra"
)

func runInteractive() error { return rootCmd.Help() }
func registerPlatformCommands() {
	rootCmd.Long = "Laevitas CLI in your browser. Market-data commands use the same Go implementation as the installed CLI.\nConnect your browser wallet with agent login, inspect agent balance, then use existing credits.\nCredentials stay in this tab. No private key, server shell or automatic payment is used.\nStreaming, TUI dashboards, stdio MCP, local files and payments are available in the installed CLI: https://cli.laevitas.ch"
	rootCmd.RemoveCommand(configcmd.Cmd, wallet.Cmd, update.Cmd, doctorCmd)
	agent.Cmd.Long = "Connect your Base or Solana browser wallet with agent login. Catalog and sample are free.\nUse existing credits for REST data, inspect balance and usage, or claim starter credits if eligible.\nAPI keys require Agent Enterprise. Quotes do not pay; complete purchases with the installed CLI."
	agent.Cmd.Example = "  laevitas agent sample\n  laevitas agent login\n  laevitas agent claim\n  laevitas agent balance"
	for _, command := range agent.Cmd.Commands() {
		switch command.Name() {
		case "login":
			command.Short = "Sign in with your Base or Solana browser wallet; no payment"
		case "pay", "mcp", "partner":
			agent.Cmd.RemoveCommand(command)
		}
	}
	rootCmd.AddCommand(&cobra.Command{Use: "doctor", Short: "Check the browser runtime, API and wallet session without spending credits", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		client, err := agentclient.New(cfg)
		if err != nil {
			return err
		}
		_, err = client.Catalog(cmd.Context())
		session, signedIn := browser.Current(cfg.BaseURL)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"success": err == nil, "data": map[string]any{"runtime": "browser WebAssembly", "version": version.Version, "apiOrigin": cfg.BaseURL, "apiReachable": err == nil, "walletVerified": signedIn, "wallet": session.Wallet, "storage": "tab memory", "automaticPayments": false}, "meta": map[string]any{"command": "laevitas doctor"}})
	}})
}
