package panels

// FlowLiquidationsPanel — live forced-close events for the flow
// detail dashboard.
//
// Same lifecycle as FlowTapePanel/FlowBookPanel: passive,
// selection-driven, single-venue, channel-filtered. Channel prefix
// is `liquidations.<market>.<venue>.<instrument>`.
//
// Liquidations are derivatives-only (perpetuals + futures) per the
// gateway's streamsByMarket. The panel itself enforces this by
// returning empty subscriptions for non-derivatives selections
// (see liquidationsChannelForSelection). FlowPanel is the primary
// gate — it doesn't wire the panel for spot/options/predictions —
// but the channel-builder's check is the safety net so a
// misconfigured FlowPanel doesn't end up subscribing to a
// channel that will never fire.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// flowLiqCapacity bounds the liquidation ring. Liquidations are
// rarer than trades, so a smaller ring is sufficient — the user
// values seeing the most recent 10-20 over having access to a long
// historical tape.
const flowLiqCapacity = 32

// flowLiqMinWidth is the smallest width that fits the narrow row
// format (TIME / SIDE / USD / @ / PRICE) — AGO column is dropped
// when the pane is below flowLiqAGOThreshold cells wide.
//
// Narrow row layout:
//
//	gutter (3) + time (8) + space + side (5) + space + notional (10) +
//	space + at (3) + space + price (12) + trailing margin (1) = 43 cells.
//
// Below this, the panel renders the "(too small)" placeholder.
const flowLiqMinWidth = 43

// flowLiqAGOThreshold is the width at which the AGO column gets
// added back to each row. AGO takes ~8 cells (3-space gap + 5
// chars like "365d"). Below this, rows are TIME / SIDE / USD /
// @ / PRICE only.
//
// Math: narrow (43) + AGO column (8) = 51.
const flowLiqAGOThreshold = 51

// flowLiqMinHeight: header + at least one liquidation = 2.
const flowLiqMinHeight = 2

// liqEvent is the panel's local representation of one liquidation.
// Stripped to just the fields the row format needs.
type liqEvent struct {
	timestamp time.Time
	price     float64
	// notionalUSD is the USD value of the liquidated position.
	// Wire payloads vary by venue; we resolve from the available
	// fields in Update.
	notionalUSD float64
	// side is the position side that got liquidated: "long" or
	// "short". A long liquidation is a forced sell into the
	// market; a short is a forced buy.
	side string
}

// FlowLiquidationsPanel implements dashboard.Panel.
type FlowLiquidationsPanel struct {
	selection dashboard.Selection
	ring      []liqEvent
}

// NewFlowLiquidationsPanel constructs the panel with an initial
// selection.
func NewFlowLiquidationsPanel(initial dashboard.Selection) *FlowLiquidationsPanel {
	return &FlowLiquidationsPanel{selection: initial}
}

// currentChannel returns the WS channel for the panel's current
// selection. Empty when the selection is incomplete.
func (p *FlowLiquidationsPanel) currentChannel() string {
	return liquidationsChannelForSelection(p.selection)
}

// CardSubtitle returns the venue:instrument identity for the
// CardPanel decorator's top-border label. Empty when no
// selection is installed.
func (p *FlowLiquidationsPanel) CardSubtitle() string {
	if p.selection.Venue == "" || p.selection.Symbol == "" {
		return ""
	}
	return p.selection.Venue + ":" + p.selection.Symbol
}

// Init has no startup commands.
func (p *FlowLiquidationsPanel) Init() tea.Cmd { return nil }

// Update handles SelectionChangedMsg + FeedTickMsg. Same shape as
// FlowTapePanel.
func (p *FlowLiquidationsPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch m := msg.(type) {
	case dashboard.SelectionChangedMsg:
		p.selection = m.New
		p.ring = nil
	case dashboard.FeedTickMsg:
		want := p.currentChannel()
		if want == "" || m.Event.Channel != want {
			return p, nil
		}
		// Wire payload field set varies by venue. Common fields:
		//   side / position_side: "long" / "short"
		//   amount_usd / quote_amount: USD notional
		//   price: liquidation price
		//   date: RFC3339 timestamp
		//   timestamp: epoch millis
		// We try the well-known names in order; missing fields
		// default to zero / empty rather than failing the decode.
		var liq struct {
			Date         string  `json:"date"`
			Timestamp    int64   `json:"timestamp"`
			Price        float64 `json:"price"`
			AmountUSD    float64 `json:"amount_usd"`
			QuoteAmount  float64 `json:"quote_amount"`
			Side         string  `json:"side"`
			PositionSide string  `json:"position_side"`
		}
		if err := json.Unmarshal(m.Event.Data, &liq); err != nil {
			return p, nil
		}
		notional := liq.AmountUSD
		if notional == 0 {
			notional = liq.QuoteAmount
		}
		side := strings.ToLower(liq.Side)
		if side == "" {
			side = strings.ToLower(liq.PositionSide)
		}
		// Validate: skip incomplete payloads. An empty side would
		// render as SHORT (since side != "long") with $0 notional,
		// pretending a liquidation happened when none did. Drop
		// rather than pollute the panel with phantom events.
		if liq.Price <= 0 || notional <= 0 {
			return p, nil
		}
		if side != "long" && side != "short" {
			return p, nil
		}
		p.appendLiq(liqEvent{
			timestamp:   parseTradeTime(liq.Date, liq.Timestamp),
			price:       liq.Price,
			notionalUSD: notional,
			side:        side,
		})
	}
	return p, nil
}

// appendLiq prepends to the ring (newest at index 0), evicting
// from the tail at capacity. Same pattern as FlowTapePanel.
func (p *FlowLiquidationsPanel) appendLiq(e liqEvent) {
	p.ring = append([]liqEvent{e}, p.ring...)
	if len(p.ring) > flowLiqCapacity {
		p.ring = p.ring[:flowLiqCapacity]
	}
}

// Subscriptions returns the liquidations channel for the current
// selection. Same Subscriptions-syncs-p.selection contract as
// FlowBookPanel.
func (p *FlowLiquidationsPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	ch := liquidationsChannelForSelection(sel)
	if ch != p.currentChannel() {
		p.selection = sel
		p.ring = nil
	}
	if ch == "" {
		return dashboard.FeedSpec{}
	}
	return dashboard.FeedSpec{Channels: []string{ch}}
}

func (p *FlowLiquidationsPanel) Title() string                     { return "" }
func (p *FlowLiquidationsPanel) Capabilities() keymap.Capabilities { return keymap.Capabilities{} }

// View renders header + recent liquidations. Empty ring → waiting
// placeholder. Liquidation rows: time, side (LONG/SHORT), notional
// in USD, "@", price.
func (p *FlowLiquidationsPanel) View(width, height int, ctx dashboard.PanelContext) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.ring) == 0 {
		// Three empty states, in dependency order:
		//  1. No selection → "no instrument selected" (handled by
		//     waitingView with empty label).
		//  2. Selection set but feed still warming up → spinner +
		//     "waiting for liquidations…".
		//  3. Feed live but no liquidations have fired yet → static
		//     "no recent liquidations" (no spinner).
		//
		// (3) is the common case after a few minutes on a quiet
		// market — perpetual liquidations are minute-scale, not
		// second-scale. Without the static label the spinner
		// implies the feed is broken when really nothing's
		// happening.
		if p.currentChannel() == "" {
			return waitingView(width, height, "", ctx.SpinnerFrame)
		}
		if ctx.FeedState == dashboard.FeedHealthy {
			// Feed is alive; we just haven't seen a liquidation yet.
			// Static placeholder, no spinner — distinguishes from
			// "still warming up" where the spinner is informative.
			return waitingView(width, height, "no recent liquidations", "")
		}
		return waitingView(width, height, "waiting for liquidations…", ctx.SpinnerFrame)
	}
	if width < flowLiqMinWidth || height < flowLiqMinHeight {
		return p.viewCompactLiquidations(width, height)
	}

	grey := output.BrandGreyMid
	red := output.Red
	green := output.BrandGreen
	reset := output.Reset

	rows := make([]string, 0, height)

	// Stats line: 1h rolling aggregate of liquidations in the
	// ring. Long / short USD totals + net direction + count.
	// Anchor on the wall clock so the 1h window genuinely ages
	// out — without that, a quiet hour of liquidations would
	// keep the last batch on-screen forever as "current."
	statsLine := buildLiqStats(p.ring, time.Now(), width)
	rows = append(rows, statsLine)

	// Header. Two layouts based on width:
	//   wide   (≥ flowLiqAGOThreshold): TIME / SIDE / USD / @ / PRICE / AGO
	//   narrow (< flowLiqAGOThreshold): TIME / SIDE / USD / @ / PRICE
	// AGO takes ~8 cells; on tighter panes (e.g. side-by-side
	// with TAPE at 40% of half-screen) we drop it rather than
	// truncate prices off the right edge.
	wideAGO := width >= flowLiqAGOThreshold
	var header string
	if wideAGO {
		header = fmt.Sprintf("   %-8s %-5s %-10s     %-12s   %s", "TIME", "SIDE", "USD", "PRICE", "AGO")
	} else {
		header = fmt.Sprintf("   %-8s %-5s %-10s     %s", "TIME", "SIDE", "USD", "PRICE")
	}
	header = grey + header + reset
	headerVisible := output.VisibleWidth(header)
	if headerVisible < width {
		header = header + strings.Repeat(" ", width-headerVisible)
	}
	rows = append(rows, header)

	now := time.Now()
	// height-2 to leave room for stats + column header.
	maxRows := height - 2
	if maxRows > len(p.ring) {
		maxRows = len(p.ring)
	}
	for i := 0; i < maxRows; i++ {
		e := p.ring[i]
		// LONG liquidation = forced sell (red); SHORT = forced buy
		// (green). Trader convention: red ink for "longs flushed."
		sideColor := red
		sideLabel := "LONG "
		if e.side != "long" {
			sideColor = green
			sideLabel = "SHORT"
		}

		timeCell := e.timestamp.Format("15:04:05")
		usdCell := formatUSD(e.notionalUSD)
		priceCell := output.FormatBookPrice(e.price)

		var raw, colored string
		if wideAGO {
			agoCell := formatRelativeAge(now.Sub(e.timestamp))
			raw = fmt.Sprintf("   %-8s %-5s %-10s  @  %-12s   %s",
				timeCell, sideLabel, usdCell, priceCell, agoCell)
			colored = fmt.Sprintf("   %-8s %s%-5s%s %-10s  %s@%s  %s%-12s%s   %s%s%s",
				timeCell,
				sideColor, sideLabel, reset,
				usdCell,
				grey, reset,
				sideColor, priceCell, reset,
				grey, agoCell, reset,
			)
		} else {
			raw = fmt.Sprintf("   %-8s %-5s %-10s  @  %s",
				timeCell, sideLabel, usdCell, priceCell)
			colored = fmt.Sprintf("   %-8s %s%-5s%s %-10s  %s@%s  %s%s%s",
				timeCell,
				sideColor, sideLabel, reset,
				usdCell,
				grey, reset,
				sideColor, priceCell, reset,
			)
		}
		visible := output.VisibleWidth(raw)
		if visible < width {
			colored = colored + strings.Repeat(" ", width-visible)
		}
		rows = append(rows, colored)
	}
	for len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}
	return strings.Join(rows, "\n")
}

// viewCompactLiquidations keeps only the liquidation direction and
// notional. That is enough to scan stress direction in a tiny pane
// and avoids refusing to render below the full table width.
func (p *FlowLiquidationsPanel) viewCompactLiquidations(width, height int) string {
	rows := make([]string, 0, height)
	maxRows := height
	if maxRows > len(p.ring) {
		maxRows = len(p.ring)
	}
	for i := 0; i < maxRows; i++ {
		e := p.ring[i]
		color := output.Red
		side := "LONG"
		if e.side != "long" {
			color = output.BrandGreen
			side = "SHORT"
		}
		line := fmt.Sprintf("%s %s", side, formatUSDStat(e.notionalUSD))
		rows = append(rows, output.PadRightAnsi(color+line+output.Reset, width))
	}
	for len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}
	return strings.Join(rows, "\n")
}

// liquidationsChannelForSelection builds the WS channel string.
// Empty when selection is incomplete OR when the market doesn't
// publish liquidations (per the gateway's streamsByMarket: only
// perpetuals and futures emit forced-close events).
//
// Gating here as a safety net: FlowPanel is supposed to skip
// wiring this panel for non-derivatives selections, but if a
// future caller or misconfigured FlowPanel routes one through,
// returning empty rather than subscribing to a never-firing
// channel keeps the panel honest. The "no instrument selected"
// placeholder shows instead of an indefinite "waiting for
// liquidations…".
func liquidationsChannelForSelection(sel dashboard.Selection) string {
	if sel.Market == "" || sel.Venue == "" || sel.Symbol == "" {
		return ""
	}
	if sel.Market != "perpetuals" && sel.Market != "futures" {
		return ""
	}
	return fmt.Sprintf("liquidations.%s.%s.%s", sel.Market, sel.Venue, sel.Symbol)
}

// formatUSD renders a notional value in compact form. Picks the
// magnitude bucket so the column stays narrow:
//
//	abs < 1k    → "$XXX" (no decimals — sub-thousand liquidations
//	              are essentially noise on perps but we render
//	              them rather than blank the cell).
//	abs < 10k   → "$X.XK"
//	abs < 1M    → "$XXXK"
//	abs >= 1M   → "$X.XM"
func formatUSD(v float64) string {
	if v <= 0 {
		return ""
	}
	switch {
	case v < 1_000:
		return fmt.Sprintf("$%.0f", v)
	case v < 10_000:
		return fmt.Sprintf("$%.1fK", v/1_000)
	case v < 1_000_000:
		return fmt.Sprintf("$%.0fK", v/1_000)
	default:
		return fmt.Sprintf("$%.1fM", v/1_000_000)
	}
}

// formatUSDStat renders a USD value for stats lines, where zero
// is a meaningful value ("the window has zero buy notional") not
// a degenerate one. Returns "$0" for zero or near-zero
// (sub-cent) values, the same compact form as formatUSD for
// non-zero values.
//
// Distinct from formatUSD because the two callers want different
// zero handling: per-trade row cells render zero as blank (a
// trade with zero size shouldn't have happened), while stats
// lines render zero as "$0" (a 60s window with no buys is real
// information, not an absence of data).
func formatUSDStat(v float64) string {
	if v <= 0 {
		return "$0"
	}
	switch {
	case v < 1_000:
		return fmt.Sprintf("$%.0f", v)
	case v < 10_000:
		return fmt.Sprintf("$%.1fK", v/1_000)
	case v < 1_000_000:
		return fmt.Sprintf("$%.0fK", v/1_000)
	default:
		return fmt.Sprintf("$%.1fM", v/1_000_000)
	}
}

// formatRelativeAge renders a duration as a compact "Ns / Nm /
// Nh" age. Used by the liquidations panel's AGO column so the
// user can see "this just happened" vs "this happened 8 minutes
// ago" without having to subtract from the wall clock.
//
// Buckets:
//
//	d < 0    → "now"   (clock skew between server and client)
//	d < 60s  → "Ns"    (whole seconds)
//	d < 60m  → "Nm"    (whole minutes)
//	d < 24h  → "Nh"    (whole hours)
//	d ≥ 24h  → "Nd"    (whole days; rare for liquidations but
//	                    handles the case of leaving the dashboard
//	                    open overnight)
func formatRelativeAge(d time.Duration) string {
	if d < 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// buildLiqStats emits a single-line aggregate over the most
// recent 1 hour of liquidations in the ring.
//
// Format (full):
//
//	1h · 12 events · LONGS $480K / SHORTS $120K · NET LONG-FLUSH $360K
//
// Width-adaptive: full bar at wide widths, condensed at medium,
// just the net direction at narrow.
//
// `ring` is the panel's liquidation ring (newest-first, capped
// at flowLiqCapacity = 32 events). We walk it forward summing
// per-side USD until we hit a liquidation older than 1h, then
// stop.
//
// `now` is the wall-clock anchor for the rolling window. Anchor
// on the wall clock (not the newest retained event's timestamp)
// so the 1h window genuinely ages out — liquidations are
// minute-to-hour-scale events and the panel's empty state must
// converge to "no recent activity" without waiting for a fresh
// event to push the old ones out.
//
// "LONG-FLUSH" = longs got hit harder; "SHORT-SQUEEZE" = shorts
// got hit harder. Both are common trader vocabulary for the
// directional bias of forced closes.
func buildLiqStats(ring []liqEvent, now time.Time, width int) string {
	if len(ring) == 0 || width <= 0 {
		return strings.Repeat(" ", width)
	}

	const window = time.Hour
	cutoff := now.Add(-window)

	var longUSD, shortUSD float64
	count := 0
	for _, e := range ring {
		if e.timestamp.Before(cutoff) {
			break
		}
		count++
		if e.side == "long" {
			longUSD += e.notionalUSD
		} else {
			shortUSD += e.notionalUSD
		}
	}

	// Net direction colour-coded but with no jargon label:
	//   net > 0 (longs hit harder) → red    (trader convention)
	//   net < 0 (shorts hit harder) → green
	//   net == 0 or empty            → grey (neutral)
	//
	// Sign on the absolute notional carries the direction; the
	// colour reinforces it. No "LONG-FLUSH" / "SHORT-SQUEEZE"
	// jargon — the user reads the sign and the colour.
	grey := output.BrandGreyMid
	red := output.Red
	green := output.BrandGreen
	reset := output.Reset

	net := longUSD - shortUSD
	netColor := red
	netSign := "+"
	switch {
	case count == 0 || net == 0:
		netColor = grey
		netSign = ""
	case net < 0:
		netColor = green
		netSign = "-"
	}
	netAbs := net
	if netAbs < 0 {
		netAbs = -netAbs
	}

	full := fmt.Sprintf("%s1h · %d events · %sLONGS %s%s / %sSHORTS %s%s · NET %s%s%s%s",
		grey, count,
		red, formatUSDStat(longUSD), grey,
		green, formatUSDStat(shortUSD), grey,
		netColor, netSign, formatUSDStat(netAbs), reset)
	medium := fmt.Sprintf("%s1h · %sLONGS %s%s / %sSHORTS %s%s · NET %s%s%s%s",
		grey,
		red, formatUSDStat(longUSD), grey,
		green, formatUSDStat(shortUSD), grey,
		netColor, netSign, formatUSDStat(netAbs), reset)
	minimal := fmt.Sprintf("%s1h · NET %s%s%s%s",
		grey, netColor, netSign, formatUSDStat(netAbs), reset)

	for _, candidate := range []string{full, medium, minimal} {
		if output.VisibleWidth(candidate) <= width {
			pad := width - output.VisibleWidth(candidate)
			if pad > 0 {
				candidate += strings.Repeat(" ", pad)
			}
			return candidate
		}
	}
	return output.TruncateAnsi(minimal, width)
}

// Compile-time interface satisfaction.
var _ Panel = (*FlowLiquidationsPanel)(nil)
