package dash

// `laevitas dash flow <currency> [market]` — perp flow dashboard.
//
// Two modes inside the same TUI:
//   - Screener: every venue's perp for the given currency, one row
//     per (venue × instrument). Cursor up/down navigates; Enter
//     drills.
//   - Detail: chart + multi-venue book + tape + liquidations for
//     the selected (venue, instrument) pair. Esc backs out to the
//     screener.
//
// Mode ownership lives in panels.FlowPanel; the cmd here is pure
// plumbing — parse args, build the panel tree, hand to the kernel.
//
// v0.10.0 ships perpetuals only. ParseFlowArgs guards the market
// list so a futures/options/spot invocation fails fast with a
// helpful message rather than rendering an empty detail composite.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/dashboard/panels"
	"github.com/laevitas/cli/internal/output"
)

var flowCmd = &cobra.Command{
	Use:   "flow <market> <currency>",
	Short: "Perp flow dashboard — screener of every venue's perp, drill into chart/book/tape/liquidations",
	Long: "Perp flow dashboard for the given currency.\n\n" +
		"Opens a screener listing every venue's perp for the currency,\n" +
		"one row per (venue × instrument). Move with ↑↓ / j k; press\n" +
		"Enter to drill into a row. Detail view shows:\n\n" +
		"  • Chart (top-left): 1m candles for the selected instrument.\n" +
		"  • Book (top-right): single-venue compact ladder.\n" +
		"  • Tape (bottom-left): live trade tape.\n" +
		"  • Liquidations (bottom-right): forced-close events.\n\n" +
		"Esc backs out of detail to the screener.\n\n" +
		"Markets supported in v0.10.0: perpetuals. Futures / options /\n" +
		"spot land in a later release.\n\n" +
		"Argument order matches the rest of the CLI: market first\n" +
		"(`dash book <market> <symbol>`, `ws <market> book <pair>`).\n\n" +
		"Keys:\n" +
		"  ↑ ↓ j k             move screener cursor\n" +
		"  enter               drill into row\n" +
		"  esc                 back out of detail\n" +
		"  p                   pause\n" +
		"  ?  h  H             keybinding overlay\n" +
		"  q  Q  ctrl+c        quit",
	Example: "  laevitas dash flow perpetuals BTC\n" +
		"  laevitas dash flow perpetuals ETH\n" +
		"  laevitas dash flow perp SOL",
	Args: cmdutil.NamedArgs("market", "currency"),
	RunE: runFlow,
}

func init() {
	Cmd.AddCommand(flowCmd)
}

func runFlow(cmd *cobra.Command, args []string) error {
	parsed, err := ParseFlowArgs(args)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("no API key configured. Set LAEVITAS_API_KEY or run `laevitas config init`")
	}

	// Refuse cleanly on non-TTY before tea even tries to grab one.
	// Mirrors `dash book`'s gate so agents see an actionable
	// "use ws instead" message rather than tea's /dev/tty error.
	if !output.IsTTY() {
		return fmt.Errorf(
			"dash is TTY-only and can't run when stdout is piped or redirected.\n" +
				"For scripts/agents, use `laevitas ws perpetuals trades <exchange:instrument>` (NDJSON).",
		)
	}

	// REST client for the screener's snapshot fetch + refresh.
	// MustClient handles auth resolution (api-key / x402) and
	// surfaces a clean error if nothing's configured.
	client, _ := cmdutil.MustClient()

	screener := panels.NewFlowScreenerPanel(client, parsed.Currency, parsed.Market)
	flow := panels.NewFlowPanel(screener)

	root := dashboard.NewRoot(dashboard.Config{
		Title:  fmt.Sprintf("flow — %s %s", parsed.Currency, parsed.Market),
		Layout: dashboard.LayoutSingle,
		Panels: map[dashboard.PaneSlot]dashboard.Panel{
			dashboard.PaneMain: flow,
		},
		Selection: dashboard.Selection{
			Currency: parsed.Currency,
			Market:   parsed.Market,
		},
		APIKey:     cfg.APIKey,
		GatewayURL: "",
	})

	prog := tea.NewProgram(
		root,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := prog.Run(); err != nil {
		// Translate tea's terse /dev/tty error into something a
		// human or agent can act on — same pattern as dash book.
		if strings.Contains(err.Error(), "/dev/tty") || strings.Contains(err.Error(), "no such device") {
			return fmt.Errorf(
				"dash is TTY-only and can't open a terminal in this environment.\n" +
					"For scripts/agents, use `laevitas ws perpetuals trades <exchange:instrument>` (NDJSON).",
			)
		}
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}
