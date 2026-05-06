package dash

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/dashboard/panels"
	"github.com/laevitas/cli/internal/instruments"
	"github.com/laevitas/cli/internal/output"
)

// bookFlags holds optional currency-resolution flags for `dash book`.
// Empty values mean "literal mode" — the user passed an explicit
// instrument like BTCUSDT and we don't run the resolver.
var bookFlags struct {
	Margin string // canonical after normalisation: linear / inverse / ""
	Quote  string // strict quote filter: USDT / USDC / "" (cascade)
}

// `laevitas dash book <market> <symbol>` — multi-venue order book
// dashboard. Aggregates depth across every venue listing the symbol
// using a wildcard subscription, then renders the consolidated
// ladder + per-venue strip + cross-venue summary.
//
// Markets supported in v0.8.3: perpetuals, spot, futures, predictions.
// Options is rejected (no L2 data on the gateway). The kernel and
// FeedRouter handle the rest — this command is pure plumbing.

var bookSupportedMarkets = map[string]struct{}{
	"perpetuals":  {},
	"futures":     {},
	"spot":        {},
	"predictions": {},
}

var bookCmd = &cobra.Command{
	Use:   "book <market> <symbol-or-currency>",
	Short: "Multi-venue order book dashboard — aggregated ladder + per-venue strip",
	Long: "Multi-venue order book aggregated across every exchange listing the symbol.\n\n" +
		"Two ways to call it:\n\n" +
		"  Currency mode (recommended):\n" +
		"    laevitas dash book perpetuals BTC --margin linear\n" +
		"    Resolves BTC perp-linear to each venue's canonical contract\n" +
		"    (BTCUSDT on binance, BTC-USDT-SWAP on okx, etc.) via the\n" +
		"    instruments registry. Per-venue quote-currency preference:\n" +
		"    USDT → USDC → USD → first available. Override with --quote.\n\n" +
		"  Literal mode (legacy):\n" +
		"    laevitas dash book perpetuals BTCUSDT\n" +
		"    Subscribes to the wildcard `book.perpetuals.*.BTCUSDT` —\n" +
		"    only venues that name the contract exactly that way contribute.\n\n" +
		"Layout:\n" +
		"  • Aggregated ladder (left): consolidated depth, segmented bars\n" +
		"    coloured by venue contribution. Whale ▲ marker on dominant\n" +
		"    levels; flash glyphs ↑/↓ on level grow/shrink.\n" +
		"  • Venue strip (right): per-venue best bid/ask/spread/imbalance\n" +
		"    plus a CONSOLIDATED block summarising cross-venue best bid/ask,\n" +
		"    total liquidity, and weighted imbalance.\n\n" +
		"Markets supported: perpetuals, spot, futures, predictions.\n" +
		"Options is not supported (no L2 data on the streaming gateway).\n\n" +
		"Keys:\n" +
		"  +/-                 cycle price grouping\n" +
		"  d                   cycle depth tier (10 → 20 → 50 → 100)\n" +
		"  c                   recenter on spread\n" +
		"  v                   venue toggle picker\n" +
		"  p                   pause\n" +
		"  ?  h  H             keybinding overlay\n" +
		"  q  Q  ctrl+c        quit",
	Example: "  laevitas dash book perpetuals BTC --margin linear\n" +
		"  laevitas dash book perpetuals BTC --margin inverse\n" +
		"  laevitas dash book perpetuals ETH --margin linear --quote USDC\n" +
		"  laevitas dash book spot BTC\n" +
		"  laevitas dash book perpetuals BTCUSDT          # literal mode (legacy)",
	Args: cmdutil.NamedArgs("market", "symbol-or-currency"),
	RunE: runBook,
}

func init() {
	bookCmd.Flags().StringVar(&bookFlags.Margin, "margin", "", "Margin type for currency mode: linear or inverse (aliases: usdt/usdc/coin all accepted). Ignored for spot.")
	bookCmd.Flags().StringVar(&bookFlags.Quote, "quote", "", "Strict quote-currency filter for currency mode (e.g. USDT, USDC). Default: per-venue cascade USDT→USDC→USD.")
	Cmd.AddCommand(bookCmd)
}

// looksLikeCurrency returns true when arg is a bare currency code
// like "BTC" / "ETH" / "SOL" — short, all-letters, no separators.
// Used to pick between currency mode and literal mode without
// requiring a flag. A literal instrument like BTCUSDT, BTC-USDT-SWAP,
// or BTC-26JUN26 contains digits or hyphens and routes to literal mode.
func looksLikeCurrency(s string) bool {
	if s == "" || len(s) > 6 {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func runBook(cmd *cobra.Command, args []string) error {
	// Normalise the market token at the entry point so internal
	// code only ever sees the canonical form. Accepts perp,
	// perpetual, perpetuals, swap, fut, futures, etc. — see
	// internal/api/markets.go for the full alias table.
	market, ok := api.NormalizeMarket(args[0])
	if !ok {
		return fmt.Errorf(
			"unknown market %q. Use one of: perpetuals, futures, spot, predictions (aliases like perp/swap/fut also work)",
			args[0],
		)
	}
	if _, ok := bookSupportedMarkets[market]; !ok {
		return fmt.Errorf(
			"unsupported market %q. Supported: perpetuals, futures, spot, predictions (options has no L2 data)",
			market,
		)
	}
	arg := args[1]
	if arg == "" {
		return fmt.Errorf("symbol or currency is required (e.g. BTCUSDT, BTC, ETH)")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("no API key configured. Set LAEVITAS_API_KEY or run `laevitas config init`")
	}

	// Decide currency vs literal mode. Currency mode triggers when
	// the arg looks like a bare currency code (BTC, ETH) OR when
	// either resolver flag was set (in which case BTCUSDT-shaped
	// args also flow through the resolver, treating the prefix as
	// the base currency — but that's an edge case; primary path is
	// "user typed BTC").
	useResolver := looksLikeCurrency(arg) || bookFlags.Margin != "" || bookFlags.Quote != ""

	bookCfg := panels.BookConfig{
		Market:    market,
		DepthTier: 10,
	}

	if useResolver {
		// Currency mode — resolve per-venue contracts via the
		// instruments registry, build explicit channels, hand the
		// list to the panel.
		base := strings.ToUpper(arg)

		var margin string
		if bookFlags.Margin != "" {
			canonical, ok := api.NormalizeMargin(bookFlags.Margin)
			if !ok {
				return fmt.Errorf("unknown --margin %q. Valid: linear, inverse (aliases: usdt/usdc/coin)", bookFlags.Margin)
			}
			margin = canonical
		}
		// Spot has no margin type — silently ignore the flag rather
		// than reject it, so a user who types `--margin linear`
		// out of habit on a spot dashboard isn't punished.
		if market == "spot" {
			margin = ""
		}

		client, _ := cmdutil.MustClient()
		contracts, err := instruments.Resolve(client, instruments.Query{
			BaseCurrency: base,
			Market:       market,
			Margin:       margin,
			PreferQuote:  strings.ToUpper(bookFlags.Quote),
		})
		if err != nil {
			return fmt.Errorf("resolving %s %s: %w", base, market, err)
		}
		if len(contracts) == 0 {
			return fmt.Errorf("no venues list %s %s%s — try a different --margin or --quote", base, market, marginSuffix(margin))
		}

		channels := make([]string, 0, len(contracts))
		venues := make([]string, 0, len(contracts))
		for _, c := range contracts {
			channels = append(channels, fmt.Sprintf("book.%s.%s.%s", market, c.Exchange, c.InstrumentName))
			venues = append(venues, c.Exchange)
		}

		// Friendly pair label for the StatsLine header. Format:
		// "BTC perp-linear" / "BTC perp-inverse" / "BTC spot".
		bookCfg.PairLabel = pairLabelFor(base, market, margin)
		bookCfg.ResolvedChannels = channels
		bookCfg.ResolvedVenues = venues
	} else {
		// Literal mode (legacy) — pass the symbol through; panel
		// builds the wildcard `book.<market>.*.<symbol>` itself.
		// We still query the instruments registry by exact name so
		// the "waiting on …" footer only mentions venues that
		// actually list this contract. Without this lookup the
		// footer would tell the user we're waiting on coinbase /
		// kraken when those venues simply don't list a USDT perp,
		// regardless of symbol. Lookup is best-effort: failures
		// fall back to the curated palette.
		bookCfg.Instrument = arg
		bookCfg.PairLabel = arg

		client, _ := cmdutil.MustClient()
		venues := lookupVenuesForInstrument(client, market, arg)
		if len(venues) > 0 {
			bookCfg.ResolvedVenues = venues
		}
	}

	panel := panels.NewBookPanel(bookCfg)

	root := dashboard.NewRoot(dashboard.Config{
		// Empty Title tells the kernel to skip rendering its own
		// header — the book panel emits the shared ladder.HeaderLine
		// itself.
		Title:  "",
		Layout: dashboard.LayoutSingle,
		Panels: map[dashboard.PaneSlot]dashboard.Panel{
			dashboard.PaneMain: panel,
		},
		Selection: dashboard.Selection{
			Symbol: bookCfg.PairLabel,
		},
		APIKey:     cfg.APIKey,
		GatewayURL: "",
	})

	// Refuse cleanly on non-TTY before tea even tries to grab one.
	// Without this gate, tea.NewProgram surfaces the underlying
	// "open /dev/tty: no such device or address" — accurate but
	// not actionable for an agent piping stdout. Agents should be
	// reaching for `laevitas ws ...` instead, so we say so.
	if !output.IsTTY() {
		return fmt.Errorf(
			"dash is TTY-only and can't run when stdout is piped or redirected.\n" +
				"For scripts/agents, use `laevitas ws <market> book <exchange:instrument>` (NDJSON).",
		)
	}

	prog := tea.NewProgram(
		root,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := prog.Run(); err != nil {
		// Translate tea's terse /dev/tty error into something a
		// human or agent can act on. Other errors (program panic,
		// init failure) keep the original message.
		if strings.Contains(err.Error(), "/dev/tty") || strings.Contains(err.Error(), "no such device") {
			return fmt.Errorf(
				"dash is TTY-only and can't open a terminal in this environment.\n" +
					"For scripts/agents, use `laevitas ws <market> book <exchange:instrument>` (NDJSON).",
			)
		}
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

// lookupVenuesForInstrument queries the instruments registry by
// exact instrument name to discover which venues list it. Used in
// literal mode (`dash book perpetuals BTCUSDT`) so the panel's
// "waiting on …" footer only mentions venues that actually list
// the contract — coinbase, kraken, and similar venues that don't
// list a USDT perp won't appear regardless of symbol.
//
// Best-effort: returns nil on any error so the panel falls back to
// the curated palette (the legacy behavior). We don't surface the
// error because the dashboard still works without this hint — it's
// a quality-of-life signal, not a hard dependency.
func lookupVenuesForInstrument(client *api.Client, market, instrument string) []string {
	if client == nil || instrument == "" {
		return nil
	}
	type entry struct {
		Exchange       string `json:"exchange"`
		InstrumentName string `json:"instrument_name"`
	}
	type envelope struct {
		Success bool    `json:"success"`
		Data    []entry `json:"data"`
	}
	var env envelope
	err := client.GetJSON(api.InstrumentsList, &api.RequestParams{
		MarketType:     api.MarketRESTToken(market),
		InstrumentName: instrument,
		Status:         "active",
	}, &env)
	if err != nil {
		return nil
	}
	// Filter to exact-name matches only — the registry uses partial
	// matching on instrument_name, so a query for BTCUSDT would
	// otherwise return BTCUSDT, BTCUSDT_240927, BTCUSDT-PERP-USDC,
	// etc. We only want the exact contract the user typed.
	target := strings.ToUpper(instrument)
	venues := make([]string, 0, len(env.Data))
	seen := make(map[string]struct{}, len(env.Data))
	for _, e := range env.Data {
		if strings.ToUpper(e.InstrumentName) != target {
			continue
		}
		if _, dup := seen[e.Exchange]; dup {
			continue
		}
		seen[e.Exchange] = struct{}{}
		venues = append(venues, e.Exchange)
	}
	return venues
}

// pairLabelFor renders the user-facing product label that goes in
// the StatsLine pair slot. Three shapes:
//
//	BTC perp-linear      (perpetuals + margin)
//	BTC spot             (spot, no margin)
//	BTC perpetuals       (margin missing — rare; resolver fallback)
//
// Keeps the label short — the StatsLine has limited space and the
// user already knows the symbol from typing it.
func pairLabelFor(base, market, margin string) string {
	switch market {
	case "spot":
		return base + " spot"
	case "perpetuals":
		if margin != "" {
			return base + " perp-" + margin
		}
		return base + " perpetuals"
	}
	return base + " " + market
}

// marginSuffix renders the `--margin` value as a parenthesised tag
// for "no venues found" error messages. Empty margin produces an
// empty string so the error reads cleanly for spot.
func marginSuffix(margin string) string {
	if margin == "" {
		return ""
	}
	return " (margin=" + margin + ")"
}
