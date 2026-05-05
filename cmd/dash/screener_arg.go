package dash

// Argument parser for `laevitas dash flow`. Lives in its own file
// so the parser is unit-testable without spinning up cobra or the
// dashboard. The cobra command in flow.go is a thin wrapper that
// calls into ParseFlowArgs and converts errors to fmt.Errorf.
//
// Argument order: `dash flow <market> <currency>`. Market first
// matches `dash book <market> <symbol>` and the rest of the CLI's
// market-leading grammar (`ws perpetuals book ...`,
// `instruments list <market> ...`); a future
// `dash flow futures BTC` lands without a breaking-change tag.
//
// Invariants the parser enforces:
//
//   - Currency is uppercased and non-empty. We accept any 1–6
//     character ASCII letter token; v0.10.0 doesn't pre-validate
//     against the catalog because the screener's REST snapshot
//     is the ground truth — if the currency has no perps the
//     snapshot returns zero rows and the user sees a clear
//     "no rows for {currency}" placeholder, which is more
//     useful than a shell error from a stale CLI whitelist.
//
//   - Market is normalised to canonical form via
//     api.NormalizeMarket and constrained to perpetuals for
//     v0.10.0 (the only market the FlowPanel detail composite is
//     wired for; futures/options come in v0.11+). A market token
//     that normalises to anything else is rejected with a clear
//     "unsupported in flow" message.
//
// Invariants the parser does NOT enforce:
//
//   - Live availability (rate-limit-friendly: that costs a REST
//     round trip per command invocation, every time).
//   - Venue presence (no --venue flag yet — drill-down picks the
//     venue interactively from the screener list).

import (
	"fmt"
	"strings"

	"github.com/laevitas/cli/internal/api"
)

// flowSupportedMarkets is the set of canonical markets the flow
// dashboard ships with in v0.10.0. Adding a market means: (a)
// confirming the detail composite's panes work for that market
// (book.<market> / trades.<market> / liquidations.<market> all
// available on the gateway), and (b) updating
// FlowLiquidationsPanel.liquidationsChannelForSelection's allow
// list. If you add to one without the other, the dashboard will
// open but the liquidations pane will silently render empty.
//
// For now: perpetuals only.
var flowSupportedMarkets = map[string]struct{}{
	"perpetuals": {},
}

// FlowArgs is the parsed result of `dash flow <currency> [market]`.
// All fields are canonical (uppercase currency, canonical-plural
// market token).
type FlowArgs struct {
	Currency string
	Market   string
}

// ParseFlowArgs validates the positional args for `dash flow`.
// args[0] is the market token; args[1] is the currency. Returns
// a typed error on validation failure so the cobra layer can
// format it without re-parsing the message.
//
// Caller is responsible for calling cobra.ExactArgs(2) to
// guarantee the right number of positional arguments — the
// parser itself trusts the slice length.
//
// Argument order matches the rest of the CLI: market first
// (`dash book <market> <symbol>`, `ws <market> book <pair>`,
// `instruments list <market>`). A user who types
// `dash flow BTC perpetuals` would therefore get a clear
// "unknown market BTC" error rather than silently succeeding —
// which is the right failure shape since "BTC" isn't a market.
func ParseFlowArgs(args []string) (FlowArgs, error) {
	if len(args) < 2 {
		return FlowArgs{}, fmt.Errorf("market and currency are both required (e.g. perpetuals BTC)")
	}

	rawMarket := strings.TrimSpace(args[0])
	market, ok := api.NormalizeMarket(rawMarket)
	if !ok {
		return FlowArgs{}, fmt.Errorf(
			"unknown market %q. Use perpetuals (aliases: perp, swap, perpetual all accepted)",
			args[0],
		)
	}
	if _, supported := flowSupportedMarkets[market]; !supported {
		return FlowArgs{}, fmt.Errorf(
			"flow dashboard supports perpetuals only in v0.10.0; got %q. Futures/options/spot land in a later release",
			market,
		)
	}

	rawCurrency := strings.TrimSpace(args[1])
	if !looksLikeCurrency(rawCurrency) {
		return FlowArgs{}, fmt.Errorf(
			"invalid currency %q. Use a short ASCII code like BTC, ETH, SOL",
			args[1],
		)
	}
	currency := strings.ToUpper(rawCurrency)

	return FlowArgs{Currency: currency, Market: market}, nil
}
