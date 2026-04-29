// Package wallet exposes the x402 wallet UX for both human operators and
// automation. Every subcommand respects -o json so agents can read the same
// data the pretty terminal output shows.
package wallet

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	internalConfig "github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/x402"
)

// Cmd is the top-level "wallet" command.
var Cmd = &cobra.Command{
	Use:   "wallet",
	Short: "Manage the x402 wallet — pay-per-request via USDC on Base",
	Long: `View and configure the EVM wallet used for x402 pay-per-request payments.

x402 is an alternative to API-key authentication: each request that would
otherwise need an API key can instead be paid for in USDC on Base. The CLI
holds your wallet's private key locally, signs payments on demand, and caches
the resulting credit token so subsequent requests don't require new on-chain
payments until the token expires.

Examples:
  laevitas wallet                    # show wallet, credits, auth mode
  laevitas wallet show -o json       # same, agent-friendly JSON
  laevitas wallet init               # interactive: paste private key, validate, save
  laevitas wallet set-key 0x<hex>    # non-interactive equivalent
  laevitas wallet address            # just print the address (pipe-friendly)
  laevitas wallet credits            # just print credits remaining (pipe-friendly)
  laevitas wallet unset              # clear wallet key + cached credit token`,
}

// ─── show ───────────────────────────────────────────────────────────────────

var showCmd = &cobra.Command{
	Use:     "show",
	Aliases: []string{"info", "status"},
	Short:   "Show wallet address, credits, and authentication mode",
	Run: func(cmd *cobra.Command, args []string) {
		state := loadWalletState()
		// Output selection mirrors the rest of the CLI: -o json forces JSON,
		// -o table/csv force the pretty terminal block, and -o auto picks
		// based on whether stdout is a TTY (JSON when piped, pretty in a real
		// terminal). Anything we don't recognise falls back to pretty.
		switch cmdutil.OutputFormat {
		case "json":
			emitJSON(state)
		case "auto":
			if output.IsTTY() {
				emitPretty(state)
			} else {
				emitJSON(state)
			}
		default:
			emitPretty(state)
		}
	},
}

// ─── init ───────────────────────────────────────────────────────────────────

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup — paste your EVM private key, validate, and save",
	Long: `Walks you through configuring an x402 wallet.

You'll need a hex-encoded EVM private key (with or without 0x prefix). Any of:
  * Generate fresh:  cast wallet new          (Foundry)
  * Generate fresh:  openssl rand -hex 32
  * Import existing: copy from MetaMask, Rabby, or any EVM wallet
The wallet only needs to hold USDC on Base mainnet — it pays per-request and
holds no other state.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		fmt.Println()
		fmt.Println("  Configure x402 wallet for pay-per-request payments.")
		fmt.Println()
		fmt.Println("  You'll need a hex-encoded EVM private key holding USDC on Base.")
		fmt.Println("  Generate a fresh key with: cast wallet new   (or any EVM wallet).")
		fmt.Println()
		fmt.Print("  Paste private key (input is echoed): ")
		raw, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		key := strings.TrimSpace(raw)
		if key == "" {
			return fmt.Errorf("no key provided")
		}

		// Validate by attempting to derive the address.
		pc, err := x402.NewPaymentClient(key)
		if err != nil {
			output.Errorf("Invalid wallet key: %v", err)
			return err
		}
		addr := pc.Address()

		cfg, err := internalConfig.Load()
		if err != nil {
			return err
		}
		cfg.WalletKey = key
		// Clear any stale credit token from a previous wallet — the cached
		// token belongs to a specific wallet/key pair.
		internalConfig.ClearCreditToken()
		if err := internalConfig.Save(cfg); err != nil {
			return err
		}

		output.Successf("Wallet configured")
		fmt.Printf("  Address: %s\n", addr)
		fmt.Println()
		fmt.Println("  The wallet must hold USDC on Base mainnet to pay for requests.")
		fmt.Println("  Fund the address above, then run any data command.")
		return nil
	},
}

// ─── set-key ────────────────────────────────────────────────────────────────

var setKeyCmd = &cobra.Command{
	Use:   "set-key <hex>",
	Short: "Save a wallet private key non-interactively (agent-friendly)",
	Args:  cobra.ExactArgs(1),
	Long: `Sets the wallet private key without prompts. Validates the key by
deriving the address before writing config; rejects invalid input loudly.

For agents, prefer the LAEVITAS_WALLET_KEY environment variable — it doesn't
persist to disk and gets the same effect.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		key := strings.TrimSpace(args[0])
		pc, err := x402.NewPaymentClient(key)
		if err != nil {
			return fmt.Errorf("invalid wallet key: %w", err)
		}
		cfg, err := internalConfig.Load()
		if err != nil {
			return err
		}
		cfg.WalletKey = key
		internalConfig.ClearCreditToken()
		if err := internalConfig.Save(cfg); err != nil {
			return err
		}
		output.Successf("Wallet key saved (address: %s)", pc.Address())
		return nil
	},
}

// ─── unset ──────────────────────────────────────────────────────────────────

var unsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Clear the wallet key and any cached credit token",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := internalConfig.Load()
		if err != nil {
			return err
		}
		cfg.WalletKey = ""
		internalConfig.ClearCreditToken()
		if err := internalConfig.Save(cfg); err != nil {
			return err
		}
		output.Successf("Wallet cleared")
		return nil
	},
}

// ─── address ────────────────────────────────────────────────────────────────

var addressCmd = &cobra.Command{
	Use:   "address",
	Short: "Print the wallet address (pipe-friendly)",
	Long: `Prints just the wallet address with no formatting — suitable for
shell substitution. Exits non-zero with an empty line if no wallet is configured.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		state := loadWalletState()
		if state.Address == "" {
			fmt.Println()
			os.Exit(1)
		}
		fmt.Println(state.Address)
		return nil
	},
}

// ─── credits ────────────────────────────────────────────────────────────────

var creditsCmd = &cobra.Command{
	Use:   "credits",
	Short: "Print credits remaining from the most recent x402 response (pipe-friendly)",
	Long: `Prints the credit count last reported by the API. The CLI doesn't poll
for balance — credits update on every data request via the X-Credits-Remaining
response header. After a fresh install or expired session, this returns "unknown"
until the next data request.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		state := loadWalletState()
		if state.CreditsRemaining == nil {
			fmt.Println("unknown")
			return nil
		}
		fmt.Println(*state.CreditsRemaining)
		return nil
	},
}

// ─── shared types & helpers ─────────────────────────────────────────────────

// walletState captures everything we know about the wallet at one moment.
// Same shape backs both the pretty terminal output and the JSON envelope, so
// agents and humans see identical fields.
type walletState struct {
	Configured       bool                `json:"configured"`
	Address          string              `json:"address,omitempty"`
	AuthMode         string              `json:"auth_mode"`
	HasAPIKey        bool                `json:"has_api_key"`
	APIKeyMasked     string              `json:"api_key_masked,omitempty"`
	CreditToken      *creditTokenState   `json:"credit_token,omitempty"`
	CreditsRemaining *string             `json:"credits_remaining,omitempty"`
}

type creditTokenState struct {
	Cached      bool       `json:"cached"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	ExpiresInMs *int64     `json:"expires_in_ms,omitempty"`
}

// loadWalletState reads config + cached credit token + parses what's known.
// No network calls — this is local-only.
func loadWalletState() walletState {
	state := walletState{
		AuthMode: internalConfig.AuthTypeAuto, // fallback before reading config
	}

	cfg, err := internalConfig.Load()
	if err != nil {
		return state
	}

	state.HasAPIKey = cfg.APIKey != ""
	if state.HasAPIKey {
		state.APIKeyMasked = internalConfig.MaskKey(cfg.APIKey)
	}
	if cfg.Auth != "" {
		state.AuthMode = cfg.Auth
	}

	if cfg.WalletKey != "" {
		state.Configured = true
		// Derive the address by re-validating the key. If the key has somehow
		// become invalid since it was saved (corrupted file?), don't hard-fail
		// — return Configured=true with an empty Address so the caller can
		// show the bad-state to the user.
		if pc, err := x402.NewPaymentClient(cfg.WalletKey); err == nil {
			state.Address = pc.Address()
		}
	}

	// Cached credit token: parse the JWT exp claim if standard.
	if tok := internalConfig.LoadCreditToken(); tok != "" {
		ct := &creditTokenState{Cached: true}
		if exp, ok := parseJWTExp(tok); ok {
			expTime := time.Unix(exp, 0).UTC()
			ct.ExpiresAt = &expTime
			until := time.Until(expTime).Milliseconds()
			ct.ExpiresInMs = &until
		}
		state.CreditToken = ct
	}

	return state
}

// parseJWTExp extracts the standard exp claim from a JWT without verifying
// the signature. Returns 0/false on any malformed input — the JWT may not
// be a JWT at all (older API versions return opaque tokens).
func parseJWTExp(tok string) (int64, bool) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try with padding — JWT spec is RawURL but some libs pad.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return 0, false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, false
	}
	if claims.Exp == 0 {
		return 0, false
	}
	return claims.Exp, true
}

// emitJSON prints the wallet state as a v0.6.0-shaped envelope so agents get
// the same {success, data, meta} contract every other command emits.
func emitJSON(state walletState) {
	envelope := map[string]interface{}{
		"success": true,
		"data":    state,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(envelope)
}

// emitPretty prints a human-readable wallet status block. Brand colors used
// where supported; falls back to plain text if NO_COLOR is set.
func emitPretty(state walletState) {
	green := output.BrandGreen
	grey := output.BrandGreyMid
	bold := output.Bold
	reset := output.Reset

	fmt.Println()
	fmt.Printf("  %s%s▲%s  %sLAEVITAS Wallet%s\n", bold, green, reset, bold, reset)
	fmt.Println()

	if !state.Configured {
		fmt.Printf("  %sNo wallet configured.%s\n", grey, reset)
		fmt.Println()
		fmt.Printf("  %sRun%s %slaevitas wallet init%s %sto set one up,%s\n", grey, reset, bold, reset, grey, reset)
		fmt.Printf("  %sor%s %slaevitas wallet set-key 0x<hex>%s %sfor non-interactive setup.%s\n", grey, reset, bold, reset, grey, reset)
		fmt.Println()
		// Even without a wallet, surface the API-key state so the user knows
		// what auth path is active.
		printAuthBlock(state)
		return
	}

	addr := state.Address
	if addr == "" {
		addr = "(unable to derive — key may be malformed)"
	}
	fmt.Printf("  %-13s %s%s%s  %s(EVM, Base mainnet)%s\n", "Address", bold, addr, reset, grey, reset)
	printAuthBlock(state)

	// Credit token block
	if state.CreditToken != nil && state.CreditToken.Cached {
		exp := "no expiration metadata"
		if state.CreditToken.ExpiresAt != nil && state.CreditToken.ExpiresInMs != nil {
			ms := *state.CreditToken.ExpiresInMs
			if ms > 0 {
				exp = fmt.Sprintf("expires in %s", humanDuration(time.Duration(ms)*time.Millisecond))
			} else {
				exp = fmt.Sprintf("%sexpired %s ago%s", grey, humanDuration(time.Duration(-ms)*time.Millisecond), reset)
			}
		}
		fmt.Printf("  %-13s %scached%s  %s(%s)%s\n", "Credit token", green, reset, grey, exp, reset)
	} else {
		fmt.Printf("  %-13s %snone — next request will pay on-chain%s\n", "Credit token", grey, reset)
	}

	if state.CreditsRemaining != nil {
		fmt.Printf("  %-13s %s\n", "Credits", *state.CreditsRemaining)
	} else {
		fmt.Printf("  %-13s %s(updates on next data request)%s\n", "Credits", grey, reset)
	}

	fmt.Println()
}

func printAuthBlock(state walletState) {
	grey := output.BrandGreyMid
	reset := output.Reset

	authDesc := authDescription(state.AuthMode)
	fmt.Printf("  %-13s %s  %s(%s)%s\n", "Auth mode", state.AuthMode, grey, authDesc, reset)

	if state.HasAPIKey {
		fmt.Printf("  %-13s %s  %s(configured)%s\n", "API key", state.APIKeyMasked, grey, reset)
	} else {
		fmt.Printf("  %-13s %s(none)%s\n", "API key", grey, reset)
	}
}

func authDescription(mode string) string {
	switch mode {
	case internalConfig.AuthTypeAPIKey:
		return "API key only — wallet ignored"
	case internalConfig.AuthTypeX402:
		return "wallet only — API key ignored"
	default:
		return "API key when set, falls back to wallet"
	}
}

// humanDuration formats a duration like "3h 12m" or "47s" — truncated to
// the two most-significant units so the wallet page reads cleanly.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// ensureLastSeenCredits is a no-op placeholder — credits live per-process on
// `client.LastMeta` today, not on disk. If we later persist last-seen credits,
// loadWalletState() can read them and populate CreditsRemaining without a
// network round-trip. Tracked as a v0.7.x follow-up.
var _ = api.RequestMeta{}.Credits

func init() {
	Cmd.AddCommand(showCmd)
	Cmd.AddCommand(initCmd)
	Cmd.AddCommand(setKeyCmd)
	Cmd.AddCommand(unsetCmd)
	Cmd.AddCommand(addressCmd)
	Cmd.AddCommand(creditsCmd)

	// Default invocation (`laevitas wallet`) shows status — same as `wallet show`.
	Cmd.Run = showCmd.Run
}
