package dash

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

var flowFlags struct {
	Sort string
	Asc  bool
}

var flowCmd = &cobra.Command{
	Use:   "flow <market> [currency]",
	Short: "Flow dashboard — screener plus chart/book/tape detail for perps, futures, and spot",
	Long: "Flow dashboard for a market, optionally narrowed by currency and/or exchange.\n\n" +
		"Opens a screener listing instruments for the selected scope. Move with\n" +
		"↑↓ / j k; press Enter to drill into a row. Detail view shows:\n\n" +
		"  • Chart (top-left): 1m candles for the selected instrument.\n" +
		"  • Book (top-right): single-venue compact ladder.\n" +
		"  • Tape (bottom-left): live trade tape.\n" +
		"  • Liquidations / large prints (bottom-right): market-specific flow.\n\n" +
		"Esc backs out of detail to the screener.\n\n" +
		"Markets: perpetuals, futures, spot.\n" +
		"Sort keys: volume, quote-volume, oi, funding, basis, dte, spread, last, instrument.\n\n" +
		"Keys:\n" +
		"  ↑ ↓ j k             move screener cursor\n" +
		"  enter               drill into row\n" +
		"  esc                 back out of detail\n" +
		"  p                   pause\n" +
		"  ?  h  H             keybinding overlay\n" +
		"  q  Q  ctrl+c        quit",
	Example: "  laevitas dash flow perpetuals BTC\n" +
		"  laevitas dash flow futures BTC --sort basis\n" +
		"  laevitas dash flow spot --exchange binance --sort quote-volume\n" +
		"  laevitas dash flow spot BTC --exchange binance --sort liquidity",
	Args: cobra.RangeArgs(1, 2),
	RunE: runFlow,
}

func init() {
	flowCmd.Flags().StringVar(&flowFlags.Sort, "sort", "volume", "sort key (volume, quote-volume, oi, funding, basis, dte, spread, last, instrument)")
	flowCmd.Flags().BoolVar(&flowFlags.Asc, "asc", false, "sort ascending instead of descending")
	Cmd.AddCommand(flowCmd)
}

func runFlow(cmd *cobra.Command, args []string) error {
	exchange := ""
	if cmdutil.ExchangeExplicit {
		exchange = cmdutil.Exchange
	}
	parsed, err := ParseFlowArgs(args, exchange, flowFlags.Sort, flowFlags.Asc)
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
		return fmt.Errorf("%s",
			"dash is TTY-only and can't run when stdout is piped or redirected.\n"+
				"For scripts/agents, use `laevitas ws "+parsed.Market+" trades <exchange:instrument>` (NDJSON).",
		)
	}

	// REST client for the screener's snapshot fetch + refresh.
	// MustClient handles auth resolution (api-key / x402) and
	// surfaces a clean error if nothing's configured.
	client, _ := cmdutil.MustClient()

	scope := panels.FlowScreenerScope{
		Currency: parsed.Currency,
		Exchange: parsed.Exchange,
		Market:   parsed.Market,
		Sort:     parsed.Sort,
		SortDesc: parsed.SortDesc,
	}
	screener := panels.NewFlowScreenerPanel(client, scope)
	flow := panels.NewFlowPanel(screener)

	root := dashboard.NewRoot(dashboard.Config{
		Title:  fmt.Sprintf("flow — %s %s", flowScopeLabel(parsed), parsed.Market),
		Layout: dashboard.LayoutSingle,
		Panels: map[dashboard.PaneSlot]dashboard.Panel{
			dashboard.PaneMain: flow,
		},
		Selection: dashboard.Selection{
			Currency: parsed.Currency,
			Market:   parsed.Market,
			Venue:    parsed.Exchange,
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
			return fmt.Errorf("%s",
				"dash is TTY-only and can't open a terminal in this environment.\n"+
					"For scripts/agents, use `laevitas ws "+parsed.Market+" trades <exchange:instrument>` (NDJSON).",
			)
		}
		return fmt.Errorf("dashboard: %w", err)
	}
	return nil
}

func flowScopeLabel(args FlowArgs) string {
	switch {
	case args.Currency != "" && args.Exchange != "":
		return args.Currency + "@" + args.Exchange
	case args.Currency != "":
		return args.Currency
	case args.Exchange != "":
		return args.Exchange
	default:
		return "all"
	}
}
