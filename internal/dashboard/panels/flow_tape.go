package panels

// FlowTapePanel — live trade tape for the flow detail dashboard.
//
// Renders the most recent N trades for the current selection's
// channel: timestamp, direction (buy/sell), size, price. New
// trades arrive on the WS `trades.<market>.<venue>.<instrument>`
// channel and append to a ring; the oldest fall off when the ring
// is full.
//
// Same selection-driven lifecycle as FlowBookPanel:
//   - Constructor takes initial selection.
//   - Subscriptions(sel) syncs p.selection and returns the channel.
//   - Update(SelectionChangedMsg) installs new selection, clears ring.
//   - Update(FeedTickMsg) drops events whose channel doesn't match.
//   - Capabilities advertises display filtering; FlowPanel routes
//     that key only when the tape pane is focused.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/tapefilter"
)

// flowTapeCapacity bounds the trade ring. The visible window is
// height-driven (one row per trade); we keep a few extra so that
// brief height shrinks don't drop trades the user could scroll
// back to. v0.10.0 doesn't have scrolling so the surplus is
// purely future-proofing — keeping it small to bound memory.
const flowTapeCapacity = 64

const flowTapeStatsBuckets = 5 * 60

// flowTapeMinWidth is the smallest width that fits the narrow
// row format (TIME / DIR / SIZE / PRICE) — USD column is dropped
// when the pane is below flowTapeUSDThreshold cells wide.
//
// Narrow row layout:
//
//	gutter (3) + time (8) + space + dir (4) + space + size (10) +
//	space + price (12) + trailing margin (1) = 39 cells.
//
// Below this, the panel renders the "(too small)" placeholder.
const flowTapeMinWidth = 39

// flowTapeUSDThreshold is the width at which the USD notional
// column gets added back to each row. Below this, the row is
// TIME / DIR / SIZE / PRICE only — USD takes ~12 cells of width
// (10 + leading space + colour codes) and that's a meaningful
// chunk on narrower panes.
//
// Math for the threshold: narrow (39) + USD column (12) = 51.
// Anything ≥ 51 fits the full 5-column row.
const flowTapeUSDThreshold = 51

// flowTapeMinHeight: one header-or-blank row + at least one trade
// row. Two cells minimum.
const flowTapeMinHeight = 2

// tapeTrade is the panel's local representation of one trade
// event, stripped to just the fields the row format needs. Decoded
// from the WS payload; we don't keep the full JSON around.
type tapeTrade struct {
	timestamp time.Time
	price     float64
	size      float64
	// direction is "buy" or "sell" verbatim from the wire payload;
	// some venues use "BUY"/"SELL" so we lowercase before storing.
	direction string
}

type tapeStatsBucket struct {
	unix    int64
	buyUSD  float64
	sellUSD float64
	count   int
}

type tapeStatsWindow struct {
	buyUSD  float64
	sellUSD float64
	count   int
}

type tapeStatsRing struct {
	buckets [flowTapeStatsBuckets]tapeStatsBucket
}

func (s *tapeStatsRing) add(ts time.Time, usd float64, direction string) {
	if ts.IsZero() || usd <= 0 {
		return
	}
	sec := ts.Unix()
	idx := int(sec % flowTapeStatsBuckets)
	if idx < 0 {
		idx += flowTapeStatsBuckets
	}
	b := &s.buckets[idx]
	if b.unix != sec {
		*b = tapeStatsBucket{unix: sec}
	}
	if direction == "buy" {
		b.buyUSD += usd
	} else {
		b.sellUSD += usd
	}
	b.count++
}

func (s *tapeStatsRing) window(now time.Time, seconds int) tapeStatsWindow {
	if seconds <= 0 {
		return tapeStatsWindow{}
	}
	nowSec := now.Unix()
	cutoff := nowSec - int64(seconds)
	var out tapeStatsWindow
	for i := range s.buckets {
		b := s.buckets[i]
		if b.count == 0 || b.unix <= cutoff || b.unix > nowSec {
			continue
		}
		out.buyUSD += b.buyUSD
		out.sellUSD += b.sellUSD
		out.count += b.count
	}
	return out
}

func (w tapeStatsWindow) net() float64 {
	return w.buyUSD - w.sellUSD
}

// FlowTapePanel implements dashboard.Panel.
type FlowTapePanel struct {
	// selection is the latest selection the panel knows about,
	// either via constructor or via SelectionChangedMsg /
	// Subscriptions side-effect. Channel is derived on demand.
	selection dashboard.Selection

	// ring is the most recent trades for the current channel,
	// newest at the front (index 0). Cleared on selection change.
	// Bounded by flowTapeCapacity.
	ring []tapeTrade

	stats tapeStatsRing

	// minUSD filters visible rows by notional. Trades still land in
	// ring + stats so changing the filter never destroys context.
	// Zero means unfiltered tape.
	minUSD float64

	// defaultMinUSD is restored on selection/subscription changes.
	// Spot "large prints" starts above zero; normal tape starts at
	// zero.
	defaultMinUSD float64

	// lockMinUSD freezes the threshold so `F min size` is a no-op.
	// LARGE PRINTS opts in: the pane is opinionated and shouldn't
	// have its threshold adjusted from inside the dashboard. Users
	// who want a different filter use the regular TAPE pane (which
	// starts at "all" and cycles via `F`) or the WS NDJSON feed.
	lockMinUSD bool
}

// NewFlowTapePanel constructs the panel with an initial selection.
// Same constructor contract as FlowBookPanel; see that file for
// the selection-vs-channel reasoning.
func NewFlowTapePanel(initial dashboard.Selection) *FlowTapePanel {
	return &FlowTapePanel{selection: initial}
}

// NewFlowLargePrintsPanel constructs a tape-style panel that keeps
// only trades at or above minUSD notional. Used for spot detail
// dashboards where liquidations do not exist.
func NewFlowLargePrintsPanel(initial dashboard.Selection, minUSD float64) *FlowTapePanel {
	return &FlowTapePanel{
		selection:     initial,
		minUSD:        minUSD,
		defaultMinUSD: minUSD,
		lockMinUSD:    true,
	}
}

// currentChannel returns the WS channel the panel should be
// subscribed to right now. Empty when selection is incomplete.
func (p *FlowTapePanel) currentChannel() string {
	return tradesChannelForSelection(p.selection)
}

// CardSubtitle returns the venue:instrument identity for the
// CardPanel decorator's top-border label. Empty when no
// selection is installed.
func (p *FlowTapePanel) CardSubtitle() string {
	if p.selection.Venue == "" || p.selection.Symbol == "" {
		return ""
	}
	label := p.selection.Venue + ":" + p.selection.Symbol
	if p.minUSD > 0 {
		label += " · min " + tapefilter.Label(p.minUSD)
	}
	return label
}

// Init has no startup commands.
func (p *FlowTapePanel) Init() tea.Cmd { return nil }

// Update handles SelectionChangedMsg (resets state) and
// FeedTickMsg (appends to the ring after channel match).
func (p *FlowTapePanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if keymap.ClassifyKey(m.String()) == keymap.ActTapeFilter {
			p.cycleMinUSD()
		}
	case dashboard.SelectionChangedMsg:
		p.selection = m.New
		p.ring = nil
		p.stats = tapeStatsRing{}
		p.minUSD = p.defaultMinUSD
	case dashboard.FeedTickMsg:
		want := p.currentChannel()
		if want == "" || m.Event.Channel != want {
			return p, nil
		}
		var t struct {
			Date      string  `json:"date"`
			Timestamp int64   `json:"timestamp"`
			Price     float64 `json:"price"`
			Amount    float64 `json:"amount"`      // contracts/coin amount on most channels
			Size      float64 `json:"size"`        // some venues label it "size"
			CoinAmt   float64 `json:"coin_amount"` // perps "amount" is USD; coin_amount is the unit qty
			Direction string  `json:"direction"`
		}
		if err := json.Unmarshal(m.Event.Data, &t); err != nil {
			// Malformed — preserve existing ring rather than blank it.
			return p, nil
		}
		// Resolve "size" from the available fields. Different markets
		// report differently — predictions use Size, perps use
		// CoinAmt (the unit quantity; Amount is USD notional).
		size := t.CoinAmt
		if size == 0 {
			size = t.Size
		}
		if size == 0 {
			size = t.Amount
		}
		// Validate: skip events that decoded to a partial/empty
		// payload. Without these guards an "{}" event would render
		// as "00:00:00 SELL 0 0" — direction defaulting to "" is
		// !=  "buy" so the SELL branch fires, and zero price/size
		// would corrupt the visual scan. Better to drop than
		// pollute the tape.
		direction := strings.ToLower(t.Direction)
		if t.Price <= 0 || size <= 0 {
			return p, nil
		}
		if direction != "buy" && direction != "sell" {
			return p, nil
		}
		ts := parseTradeTime(t.Date, t.Timestamp)
		p.appendTrade(tapeTrade{
			timestamp: ts,
			price:     t.Price,
			size:      size,
			direction: direction,
		})
		p.stats.add(ts, t.Price*size, direction)
	}
	return p, nil
}

func (p *FlowTapePanel) cycleMinUSD() {
	if p.lockMinUSD {
		return
	}
	p.minUSD = tapefilter.Next(p.minUSD)
}

// appendTrade inserts at the front (newest first). When the ring
// is at capacity the oldest entry falls off the back. A simpler
// shape than a true ring buffer — capacity is small, allocation
// cost is negligible, and the prepend pattern keeps the View loop
// trivial (range over ring, oldest at the bottom).
func (p *FlowTapePanel) appendTrade(t tapeTrade) {
	p.ring = append([]tapeTrade{t}, p.ring...)
	if len(p.ring) > flowTapeCapacity {
		p.ring = p.ring[:flowTapeCapacity]
	}
}

// Subscriptions returns the trades channel for the current
// selection. Same Subscriptions-syncs-p.selection contract as
// FlowBookPanel — see that file's doc-comment for rationale.
func (p *FlowTapePanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	ch := tradesChannelForSelection(sel)
	if ch != p.currentChannel() {
		p.selection = sel
		p.ring = nil
		p.stats = tapeStatsRing{}
		p.minUSD = p.defaultMinUSD
	}
	if ch == "" {
		return dashboard.FeedSpec{}
	}
	return dashboard.FeedSpec{Channels: []string{ch}}
}

// Title — passive panel, parent composite owns chrome.
func (p *FlowTapePanel) Title() string { return "" }

// Capabilities declares the tape's display filter. The parent flow
// panel routes `F` only when the tape pane is focused.
func (p *FlowTapePanel) Capabilities() keymap.Capabilities {
	if p.lockMinUSD {
		return keymap.Capabilities{}
	}
	return keymap.Capabilities{TapeFilter: true}
}

// View renders the trade tape. Top row is a header; rows below
// list trades newest-first. Empty ring shows "waiting for trades…"
// in the middle.
func (p *FlowTapePanel) View(width, height int, ctx dashboard.PanelContext) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if len(p.ring) == 0 {
		label := "waiting for trades…"
		if p.minUSD > 0 {
			label = "waiting for large prints…"
		}
		if p.currentChannel() == "" {
			label = ""
		}
		return waitingView(width, height, label, ctx.SpinnerFrame)
	}
	if width < flowTapeMinWidth || height < flowTapeMinHeight {
		return p.viewCompactTape(width, height)
	}

	grey := output.BrandGreyMid
	red := output.Red
	green := output.BrandGreen
	reset := output.Reset

	rows := make([]string, 0, height)

	// Stats line: 60s rolling aggregate of trades in the ring.
	// Buy / sell USD totals + net delta + count. Anchor on the
	// wall clock (not the newest trade's timestamp) so the
	// window genuinely ages out in quiet markets — otherwise a
	// 5-minute-old print would still count as "current."
	statsLines := buildTapeStats(&p.stats, time.Now(), width, p.minUSD)
	rows = append(rows, statsLines...)

	// Header row. Two layouts based on width:
	//   wide   (≥ flowTapeUSDThreshold): TIME / DIR / SIZE / PRICE / USD
	//   narrow (< flowTapeUSDThreshold): TIME / DIR / SIZE / PRICE
	// USD takes ~12 cells; on tighter panes (e.g. side-by-side
	// with LIQ at 60% of half-screen) we drop it rather than
	// truncate prices off the right edge.
	wideUSD := width >= flowTapeUSDThreshold
	var header string
	if wideUSD {
		header = fmt.Sprintf("   %-8s %-4s %-10s %-12s %s", "TIME", "DIR", "SIZE", "PRICE", "USD")
	} else {
		header = fmt.Sprintf("   %-8s %-4s %-10s %s", "TIME", "DIR", "SIZE", "PRICE")
	}
	header = grey + header + reset
	headerVisible := output.VisibleWidth(header)
	if headerVisible < width {
		header = header + strings.Repeat(" ", width-headerVisible)
	}
	rows = append(rows, header)

	// Trade rows. Leave room for stats + column header.
	trades := p.visibleTrades()
	maxTrades := height - len(rows)
	if maxTrades > len(trades) {
		maxTrades = len(trades)
	}
	for i := 0; i < maxTrades; i++ {
		t := trades[i]
		dirColor := green
		dirLabel := "BUY "
		if t.direction != "buy" {
			dirColor = red
			dirLabel = "SELL"
		}

		timeCell := t.timestamp.Format("15:04:05")
		sizeCell := output.FormatBookSize(t.size)
		priceCell := output.FormatBookPrice(t.price)

		// Build either the 5-column row or the 4-column narrow
		// row depending on width. Same colour treatment in both
		// (DIR and PRICE coloured by side; USD also coloured
		// when present).
		var raw, colored string
		if wideUSD {
			usdCell := formatUSD(t.price * t.size)
			raw = fmt.Sprintf("   %-8s %-4s %-10s %-12s %s",
				timeCell, dirLabel, sizeCell, priceCell, usdCell)
			colored = fmt.Sprintf("   %-8s %s%-4s%s %-10s %s%-12s%s %s%s%s",
				timeCell,
				dirColor, dirLabel, reset,
				sizeCell,
				dirColor, priceCell, reset,
				dirColor, usdCell, reset,
			)
		} else {
			raw = fmt.Sprintf("   %-8s %-4s %-10s %s",
				timeCell, dirLabel, sizeCell, priceCell)
			colored = fmt.Sprintf("   %-8s %s%-4s%s %-10s %s%s%s",
				timeCell,
				dirColor, dirLabel, reset,
				sizeCell,
				dirColor, priceCell, reset,
			)
		}
		// Pad to width using the raw row's visible width — ANSI
		// codes in `colored` aren't visible, so padding from `raw`'s
		// length is correct.
		visible := output.VisibleWidth(raw)
		if visible < width {
			colored = colored + strings.Repeat(" ", width-visible)
		}
		rows = append(rows, colored)
	}

	// Pad blank rows below the trades so the panel always renders
	// height rows.
	for len(rows) < height {
		if p.minUSD > 0 && len(trades) == 0 && len(rows) == len(statsLines)+1 {
			rows = append(rows, output.PadRightAnsi(output.BrandGreyMid+"   no trades >= "+tapefilter.Label(p.minUSD)+output.Reset, width))
			continue
		}
		rows = append(rows, strings.Repeat(" ", width))
	}

	return strings.Join(rows, "\n")
}

// viewCompactTape is the smallest useful tape: side, price, size.
// It drops stats and headers so a cramped pane still shows live
// prints instead of a placeholder.
func (p *FlowTapePanel) viewCompactTape(width, height int) string {
	rows := make([]string, 0, height)
	trades := p.visibleTrades()
	maxRows := height
	if maxRows > len(trades) {
		maxRows = len(trades)
	}
	for i := 0; i < maxRows; i++ {
		t := trades[i]
		color := output.BrandGreen
		side := "B"
		if t.direction != "buy" {
			color = output.Red
			side = "S"
		}
		line := fmt.Sprintf("%s %s %s", side, output.FormatBookPrice(t.price), output.FormatBookSize(t.size))
		rows = append(rows, output.PadRightAnsi(color+line+output.Reset, width))
	}
	for len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}
	return strings.Join(rows, "\n")
}

func (p *FlowTapePanel) visibleTrades() []tapeTrade {
	if p.minUSD <= 0 {
		return p.ring
	}
	out := make([]tapeTrade, 0, len(p.ring))
	for _, t := range p.ring {
		if tapefilter.AllowsNotional(t.price*t.size, p.minUSD) {
			out = append(out, t)
		}
	}
	return out
}

// tradesChannelForSelection builds the "trades.<market>.<venue>.
// <symbol>" channel string for a selection, or empty when the
// selection is incomplete.
func tradesChannelForSelection(sel dashboard.Selection) string {
	if sel.Market == "" || sel.Venue == "" || sel.Symbol == "" {
		return ""
	}
	return fmt.Sprintf("trades.%s.%s.%s", sel.Market, sel.Venue, sel.Symbol)
}

// parseTradeTime resolves a trade event's timestamp. Wire payloads
// carry both `date` (RFC3339) and `timestamp` (epoch millis); we
// prefer the parsed date, falling back to timestamp if unparseable
// or absent. Panel-time fallback (time.Now) used as the last
// resort so the row always has SOMETHING to display.
func parseTradeTime(date string, ts int64) time.Time {
	if date != "" {
		if t, err := time.Parse(time.RFC3339, date); err == nil {
			return t
		}
	}
	if ts > 0 {
		return time.UnixMilli(ts).UTC()
	}
	return time.Now().UTC()
}

func buildTapeStats(stats *tapeStatsRing, now time.Time, width int, minUSD float64) []string {
	if width <= 0 {
		return nil
	}
	if stats == nil {
		return []string{strings.Repeat(" ", width)}
	}
	five := stats.window(now, 5*60)
	full := appendTapeFilterLabel(formatTapeStatsFull(five), minUSD)
	if output.VisibleWidth(full) <= width {
		return []string{padTapeStatLine(full, width)}
	}
	compact := appendTapeFilterLabel(formatTapeStatsCompact(five), minUSD)
	if output.VisibleWidth(compact) <= width {
		return []string{padTapeStatLine(compact, width)}
	}
	return []string{padTapeStatLine(formatTapeStatsCompact(five), width)}
}

func appendTapeFilterLabel(line string, minUSD float64) string {
	return line + output.BrandGreyMid + " · " + output.Reset + tapeFilterLabel(minUSD)
}

func tapeFilterLabel(minUSD float64) string {
	return output.BrandGreyMid + "min " + output.Reset + tapefilter.Label(minUSD)
}

func formatTapeStatsCompact(five tapeStatsWindow) string {
	grey := output.BrandGreyMid
	reset := output.Reset
	return grey + "5m " + reset + formatTapeWindowNet(five)
}

func formatTapeStatsFull(w tapeStatsWindow) string {
	grey := output.BrandGreyMid
	green := output.BrandGreen
	red := output.Red
	reset := output.Reset
	if w.count == 0 {
		return grey + "5m -" + reset
	}
	return fmt.Sprintf("%s5m %sBUY %s%s / %sSELL %s%s · NET %s%s",
		grey, green, formatUSDStat(w.buyUSD), grey,
		red, formatUSDStat(w.sellUSD), grey,
		formatTapeNet(w.net()), reset)
}

func formatTapeWindowNet(w tapeStatsWindow) string {
	if w.count == 0 {
		return output.BrandGreyMid + "-" + output.Reset
	}
	return "NET " + formatTapeNet(w.net())
}

func formatTapeNet(net float64) string {
	color := output.BrandGreen
	sign := "+"
	if net < 0 {
		color = output.Red
		sign = "-"
		net = -net
	} else if net == 0 {
		color = output.BrandGreyMid
		sign = ""
	}
	return color + sign + formatUSDStat(net) + output.Reset
}

func padTapeStatLine(line string, width int) string {
	if output.VisibleWidth(line) > width {
		return output.TruncateAnsi(line, width)
	}
	return output.PadRightAnsi(line, width)
}

// Compile-time interface satisfaction.
var _ Panel = (*FlowTapePanel)(nil)
