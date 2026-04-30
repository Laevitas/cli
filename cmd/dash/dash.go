// Package dash is the top-level `laevitas dash` command group — the user-
// facing surface for multi-pane dashboards built on
// internal/dashboard. Each subcommand wires a specific panel
// configuration into the kernel and starts the Bubble Tea program.
//
// v0.8.3 ships with one stub command (`dash demo`) that renders a
// single empty panel — exists purely to validate the kernel end-to-
// end. Real dashboards (book, chain, perps, vol) land in subsequent
// PRs once we agree on per-dashboard routing and visuals.
package dash

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// Cmd is the top-level `dash` command. Every dashboard subcommand
// attaches itself here in its own init().
var Cmd = &cobra.Command{
	Use:   "dash",
	Short: "Multi-pane live dashboards (multi-venue book, perp screener, options chain, vol metrics)",
	Long: "Multi-pane live dashboards aggregating data across exchanges.\n\n" +
		"Each subcommand opens a TUI with one or more panels sharing a single\n" +
		"WebSocket connection and a synchronised \"selection\" (active symbol /\n" +
		"expiry / venue). Panels react to selection changes and re-subscribe\n" +
		"automatically.\n\n" +
		"Common keys across every dashboard:\n" +
		"  q  Q  ctrl+c       quit\n" +
		"  ?  h  H            keybinding overlay\n" +
		"  esc                close help / panel-specific back\n" +
		"  tab / shift+tab    cycle focused panel\n" +
		"  1 / 2 / 3          jump to main / side / strip panel\n\n" +
		"Panel-specific keys are documented inside each dashboard's `?` overlay.\n\n" +
		"NDJSON / agent piping: dashboards are TTY-only. Agents and scripts\n" +
		"should keep using `laevitas ws` for raw streams (those work unchanged and\n" +
		"produce the same NDJSON they always have).",
	// Setting Run makes cobra treat the parent as Runnable and include
	// it in the root help list — even when every subcommand is Hidden
	// (today: only the `demo` stub). Calling `laevitas dash` with no args
	// prints help, which is the behaviour users expect from a group.
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

// ─── demo subcommand ───────────────────────────────────────────────────────
//
// `laevitas dash demo` opens a single empty panel — the smallest possible
// exercise of the kernel. It's not in the help text because it's not
// a user-facing feature; it's there so we can verify the foundations
// boot cleanly before any real dashboard exists.
//
// We'll delete this once book / chain / perps / vol are all shipped.

var demoCmd = &cobra.Command{
	Use:    "demo",
	Short:  "Stub dashboard for kernel verification — single empty panel",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if cfg.APIKey == "" {
			return fmt.Errorf("no API key configured. Set LAEVITAS_API_KEY or run `laevitas config init`")
		}

		root := dashboard.NewRoot(dashboard.Config{
			Title:  "demo",
			Layout: dashboard.LayoutSingle,
			Panels: map[dashboard.PaneSlot]dashboard.Panel{
				dashboard.PaneMain: &stubPanel{title: "demo panel"},
			},
			APIKey:     cfg.APIKey,
			GatewayURL: "",
			Selection:  dashboard.Selection{},
		})

		prog := tea.NewProgram(
			root,
			tea.WithAltScreen(),
			tea.WithMouseCellMotion(),
		)
		if _, err := prog.Run(); err != nil {
			return fmt.Errorf("dashboard: %w", err)
		}
		return nil
	},
}

func init() {
	Cmd.AddCommand(demoCmd)
}

// ─── stub panel for the demo command ───────────────────────────────────────

// stubPanel is a no-op panel used by `dash demo` to verify the kernel
// boots and renders. It subscribes to nothing, ignores every message,
// and just paints a "kernel alive" banner. Production panels will be
// in their own files (e.g. cmd/dash/book.go).
type stubPanel struct {
	title  string
	width  int
	height int
}

func (s *stubPanel) Init() tea.Cmd { return nil }

func (s *stubPanel) Update(msg tea.Msg) (dashboard.Panel, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		s.width, s.height = size.Width, size.Height
	}
	return s, nil
}

func (s *stubPanel) View(width, height int, ctx dashboard.PanelContext) string {
	bold := output.Bold
	green := output.BrandGreen
	grey := output.BrandGreyMid
	reset := output.Reset
	body := bold + green + "▲ kernel alive" + reset + "\n\n" +
		grey + "the foundations are wired. real dashboards land next." + reset + "\n\n" +
		grey + "feed state: " + ctx.FeedState.String() + "  " + ctx.SpinnerFrame + reset
	// We deliberately don't pad to height here — the kernel's
	// placeholder helper does that for empty layouts; for a real
	// panel the expectation is to fill the cell yourself.
	return body
}

func (s *stubPanel) Subscriptions(_ dashboard.Selection) dashboard.FeedSpec {
	return dashboard.FeedSpec{} // empty — the demo proves boot, not data
}

func (s *stubPanel) Title() string { return s.title }

// Capabilities — the demo panel claims the always-on basics
// (Pause/Help) and nothing else. Single-pane dashboard means the
// kernel won't add MultiPane, so the footer stays minimal.
func (s *stubPanel) Capabilities() keymap.Capabilities {
	return keymap.Capabilities{Pause: true, Help: true}
}

// Compile-time check that stubPanel implements the Panel interface.
// Catches signature drift on the kernel side at build time rather
// than at first run.
var _ dashboard.Panel = (*stubPanel)(nil)

// silence "unused" warnings for cmdutil + os while the dashboard
// surface is still being assembled. These are imports we'll need
// once real subcommands land; keeping them in place avoids a churny
// diff in the next PR.
var _ = cmdutil.Verbose
var _ = os.Stderr
