package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/laevitas/cli/internal/agentclient"
	"github.com/laevitas/cli/internal/config"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{Use: "agent", Short: "Wallet sign-in, product catalog, credits and Agent Enterprise", Long: `Use the same wallet for prepaid data and agent subscriptions.
Catalog and sample are free. Sign-in does not authorize payment.
Quote first, then pay with an explicit --max-usdc limit. JSON output is suitable for agents.
API keys require an active Agent Enterprise plan. Use agent mcp as a local MCP connection.`}

func client() (*agentclient.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	return agentclient.New(cfg)
}
func run(fn func(context.Context, *agentclient.Client, []string) (map[string]any, error)) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		c, err := client()
		if err != nil {
			return commandError(cmd, err)
		}
		data, err := fn(cmd.Context(), c, args)
		if err != nil {
			return commandError(cmd, err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"success": true, "data": data, "meta": map[string]any{"command": cmd.CommandPath()}})
	}
}
func commandError(cmd *cobra.Command, err error) error {
	_ = json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"success": false, "error": map[string]any{"code": "AGENT_REQUEST_FAILED", "message": err.Error()}, "meta": map[string]any{"command": cmd.CommandPath()}})
	return err
}
func init() {
	reads := []struct {
		name, description string
		call              func(context.Context, *agentclient.Client) (map[string]any, error)
	}{
		{"catalog", "Discover agent products and current prices", func(ctx context.Context, c *agentclient.Client) (map[string]any, error) { return c.Catalog(ctx) }},
		{"sample", "Fetch free synthetic data", func(ctx context.Context, c *agentclient.Client) (map[string]any, error) { return c.Sample(ctx) }},
		{"login", "Sign in with the configured Base wallet; no payment", func(ctx context.Context, c *agentclient.Client) (map[string]any, error) { return c.Login(ctx) }},
		{"account", "Inspect your wallet identity and paid entitlement", func(ctx context.Context, c *agentclient.Client) (map[string]any, error) { return c.Account(ctx) }},
		{"balance", "Read your current credit balance without spending", func(ctx context.Context, c *agentclient.Client) (map[string]any, error) { return c.Balance(ctx) }},
		{"logout", "Revoke the management session on this device", func(ctx context.Context, c *agentclient.Client) (map[string]any, error) { return c.Logout(ctx) }},
	}
	for _, read := range reads {
		Cmd.AddCommand(&cobra.Command{Use: read.name, Short: read.description, Args: cobra.NoArgs, RunE: run(func(ctx context.Context, c *agentclient.Client, _ []string) (map[string]any, error) {
			return read.call(ctx, c)
		})})
	}
	var before string
	usage := &cobra.Command{Use: "usage", Short: "Read credit purchases, usage and refunds", Args: cobra.NoArgs, RunE: run(func(ctx context.Context, c *agentclient.Client, _ []string) (map[string]any, error) {
		return c.Usage(ctx, before)
	})}
	usage.Flags().StringVar(&before, "before", "", "Pagination cursor from the previous response")
	Cmd.AddCommand(usage)
	var cycle, requestID string
	quote := &cobra.Command{Use: "quote PRODUCT", Short: "Create a purchase quote; does not pay", Args: cobra.ExactArgs(1), RunE: run(func(ctx context.Context, c *agentclient.Client, args []string) (map[string]any, error) {
		return c.Quote(ctx, args[0], cycle, requestID)
	})}
	quote.Flags().StringVar(&cycle, "cycle", "MONTHLY", "MONTHLY, QUARTERLY, BIYEARLY or YEARLY (Enterprise)")
	quote.Flags().StringVar(&requestID, "request-id", "", "Stable UUID for retrying an interrupted quote")
	Cmd.AddCommand(quote)
	var maxUSDC string
	pay := &cobra.Command{Use: "pay CHECKOUT_ID", Short: "Pay a saved quote within an explicit USDC limit", Args: cobra.ExactArgs(1), RunE: run(func(ctx context.Context, c *agentclient.Client, args []string) (map[string]any, error) {
		return c.Pay(ctx, args[0], maxUSDC)
	})}
	pay.Flags().StringVar(&maxUSDC, "max-usdc", "", "Maximum USDC you authorize for this purchase (required)")

	Cmd.AddCommand(pay)
	Cmd.AddCommand(&cobra.Command{Use: "status CHECKOUT_ID", Short: "Check payment and activation status without paying again", Args: cobra.ExactArgs(1), RunE: run(func(ctx context.Context, c *agentclient.Client, args []string) (map[string]any, error) {
		return c.Status(ctx, args[0])
	})})
	keys := &cobra.Command{Use: "keys", Short: "Manage Enterprise API keys"}
	keys.AddCommand(&cobra.Command{Use: "list", Short: "List key labels, expiries and revocations", Args: cobra.NoArgs, RunE: run(func(ctx context.Context, c *agentclient.Client, _ []string) (map[string]any, error) {
		return c.Keys(ctx)
	})})
	var label string
	create := &cobra.Command{Use: "create", Short: "Create an Enterprise API key (shown once; keep it private)", Args: cobra.NoArgs, RunE: run(func(ctx context.Context, c *agentclient.Client, _ []string) (map[string]any, error) {
		return c.CreateKey(ctx, label)
	})}
	create.Flags().StringVar(&label, "label", "", "Name for this key (required)")
	_ = create.MarkFlagRequired("label")
	keys.AddCommand(create)
	keys.AddCommand(&cobra.Command{Use: "revoke KEY_ID", Short: "Immediately revoke an API key", Args: cobra.ExactArgs(1), RunE: run(func(ctx context.Context, c *agentclient.Client, args []string) (map[string]any, error) {
		return c.RevokeKey(ctx, args[0])
	})})
	Cmd.AddCommand(keys)
	Cmd.AddCommand(&cobra.Command{Use: "mcp", Short: "Connect an MCP client over stdio using purchased credits or an Enterprise key", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := client()
		if err != nil {
			return commandError(cmd, err)
		}
		return c.MCP(cmd.Context(), os.Stdin, cmd.OutOrStdout())
	}})
	Cmd.Example = fmt.Sprint("  laevitas agent catalog\n  laevitas agent sample\n  laevitas agent login\n  laevitas agent balance\n  laevitas agent quote credits-100\n  laevitas agent pay CHECKOUT_ID --max-usdc 0.10\n  laevitas agent status CHECKOUT_ID")
}
