package panels

// FlowBookPanel — compact, single-venue, selection-driven L2 book
// ladder for the flow detail dashboard.
//
// Deliberately separate from BookPanel: this is a passive detail
// pane (drilled into from the screener) that renders ONE instrument
// from ONE venue, with no chrome, no venue strip, no segmented
// bars, no aggregation. BookPanel owns the multi-venue dashboard
// surface; trying to coerce it into single-venue mode would mean
// gating ~600 lines of cross-venue logic behind flags. Cheaper to
// write a focused 150-line panel that does only what flow detail
// needs.
//
// Lifecycle:
//
//   - Subscriptions(sel) returns the single channel for the current
//     selection, or empty when sel is incomplete (no Symbol). The
//     kernel's FeedRouter handles add/remove.
//   - Update(SelectionChangedMsg) clears the cached snapshot; the
//     next FeedTickMsg whose channel matches the new selection
//     populates it. Without this, a drill from BTC to ETH would
//     keep showing BTC's last book until the new feed warmed up.
//   - Update(FeedTickMsg) accepts only events whose channel matches
//     the current selection's channel string. In-flight events
//     from the previous selection (the gateway hasn't processed
//     our unsubscribe yet) are dropped. This is the "stale event
//     filter" Codex emphasised in the round-1 review.
//
// Capabilities: NONE. The panel is passive in v0.10.0; declaring
// keys here would put dead entries in the footer when the panel
// is wrapped in a composite with activeChildNone. If we add keys
// later (depth tier cycle), FlowPanel must own them — see
// composite.go's "Capabilities ↔ key-routing contract" doc.

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/ladder"
	"github.com/laevitas/cli/internal/output"
)

// flowBookMaxLevels caps how many levels per side the panel
// renders, regardless of how many the wire payload carries. The
// panel's vertical real estate decides how many fit; we trim at
// render time.
const flowBookMaxLevels = 50

// flowBookMinWidth is the smallest width where the compact top-of-
// book fallback is still useful. Below this, rows are still clipped
// rather than refused; the dashboard should render something at any
// terminal size.
const flowBookMinWidth = 29

// flowBookCompactWidth is where the stacked PRICE / SIZE / CUM / bar
// ladder has enough room to show its core columns. Below this, prefer
// a compact top-of-book view.
const flowBookCompactWidth = 44

// flowBookMinHeight is the smallest row count below which the
// ladder can't show at least one level per side plus the spread
// separator (1 ask + 1 bid + 1 separator = 3, plus 2 rows budget
// for the side padding the layout needs to stay stable = 5).
const flowBookMinHeight = 5

// FlowBookPanel implements dashboard.Panel.
type FlowBookPanel struct {
	// selection is the latest selection the panel has been told
	// about, either via constructor or via SelectionChangedMsg.
	// The current channel string is computed from this on demand —
	// keeping selection as the single source of truth (rather than
	// caching channel as a parallel field) avoids the class of bug
	// where Subscriptions and Update install different values.
	selection dashboard.Selection

	// snapshot holds the latest accepted snapshot for the current
	// channel. nil when no event has arrived yet on this selection
	// (renders a "waiting" placeholder).
	snapshot *api.BookSnapshot

	// depthTier caps how many levels per side the panel renders.
	// Cycles through the canonical tiers (10/20/50/100) on `d`.
	// Defaults to 0, which means "no cap" — renders as many levels
	// as the pane's height allows.
	depthTier int

	// groupSize buckets adjacent price levels by `+/-`. Zero means
	// no grouping (raw levels as the API delivers them). When set,
	// adjacent levels whose prices fall in the same bucket are
	// summed into one displayed level. Cycle: 0 → 0.1 → 1 → 10 →
	// 100 → 0; rotating through cycles bigger groupings until the
	// scan loops back to raw.
	groupSize float64

	// viewport tracks scroll position through deeper depth tiers.
	// Same semantics as the canonical ws book ladder: offset 0 is
	// centred on the spread, positive shows deeper asks, negative
	// shows deeper bids.
	viewport ladder.Viewport

	// viewHeight is the most recent View height. Key handling uses
	// it to size PgUp/PgDn to the visible ladder page.
	viewHeight int
}

// flowBookDefaultDepthTier is the depth tier the BOOK card opens
// with. Matches the legacy `ws perpetuals book` ladder and the
// multi-venue `dash book` panel default — 10 levels per side is
// the canonical "fits the pane and shows enough liquidity to
// scan" tier. Cycled by `d` (10 → 20 → 50 → 100 → off).
const flowBookDefaultDepthTier = 10

// NewFlowBookPanel constructs the panel with an initial selection.
// The kernel's first Subscriptions/Update pass at startup uses this
// selection to seed the subscription set; the first matching
// FeedTickMsg populates the snapshot.
//
// An empty Selection is acceptable — the panel renders the
// "no instrument selected" placeholder until a SelectionChangedMsg
// installs a real one.
//
// depthTier defaults to 10 (the legacy convention); user can cycle
// via `d` to 20/50/100/off.
func NewFlowBookPanel(initial dashboard.Selection) *FlowBookPanel {
	return &FlowBookPanel{
		selection: initial,
		depthTier: flowBookDefaultDepthTier,
	}
}

// currentChannel returns the WS channel string for the panel's
// current selection, or empty when the selection is incomplete.
// Computed on demand (not cached) so the panel has a single source
// of truth for "what should I be subscribed to right now."
func (p *FlowBookPanel) currentChannel() string {
	return channelForSelection(p.selection)
}

// CardSubtitle returns the venue:instrument identity for the
// CardPanel decorator's top-border label. Empty when no
// selection is installed (the card renders just its static
// "BOOK" title in that case).
func (p *FlowBookPanel) CardSubtitle() string {
	if p.selection.Venue == "" || p.selection.Symbol == "" {
		return ""
	}
	return p.selection.Venue + ":" + p.selection.Symbol
}

// Init has no startup commands — the panel is reactive only.
func (p *FlowBookPanel) Init() tea.Cmd { return nil }

// Update handles the two messages this panel cares about:
// SelectionChangedMsg (resets state, recomputes channel) and
// FeedTickMsg (accepts events for the current channel only,
// drops stale ones).
//
// All other messages are ignored — the panel doesn't take
// keyboard input, doesn't track focus, doesn't schedule timers.
func (p *FlowBookPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		// Keys reach the book only when FlowPanel's detail-mode
		// dispatcher routes them here. The panel responds to
		// depth/group/recenter actions (declared in its
		// Capabilities); other keys silently no-op.
		p.applyKey(keymap.ClassifyKey(m.String()))
	case dashboard.SelectionChangedMsg:
		p.selection = m.New
		// Clear the cached snapshot so we don't keep showing the
		// previous instrument's book until the new feed warms up.
		// The next matching FeedTickMsg will populate it.
		p.snapshot = nil
		p.viewport.Recenter()
	case dashboard.FeedTickMsg:
		want := p.currentChannel()
		if want == "" || m.Event.Channel != want {
			// Stale event from a previous selection (the gateway
			// hasn't processed our unsubscribe yet), or no
			// selection installed yet — drop. Without this filter,
			// a drill from BTC→ETH would briefly render BTC ticks
			// under the ETH header.
			return p, nil
		}
		var snap api.BookSnapshot
		if err := json.Unmarshal(m.Event.Data, &snap); err != nil {
			// Malformed payload — keep the previous snapshot rather
			// than blanking the panel. The kernel's soft-error
			// channel handles diagnostic surfacing.
			return p, nil
		}
		snap.Channel = m.Event.Channel
		p.snapshot = &snap
	}
	return p, nil
}

// Subscriptions returns the single channel for the current
// selection. The kernel calls this on init and after every
// SelectionChangedMsg; the FeedRouter computes add/remove against
// the desired set. Empty when the selection is incomplete (no
// Symbol/Venue/Market).
//
// Side effect: synchronises p.selection with the kernel's `sel`
// when their channel strings differ. The kernel is the source of
// truth for selection; if a panel was constructed with an empty
// Selection but the kernel's root has a populated initial
// selection, the router would otherwise subscribe to the right
// channel while the panel's filter (Update → currentChannel())
// kept rejecting matching ticks. The two states must stay in
// agreement.
//
// Comparing by channel string (rather than full struct) avoids
// resyncing for selection edits that don't affect this panel's
// channel — a Currency-only change at the screener level, for
// instance, doesn't need to clear our snapshot.
func (p *FlowBookPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	ch := channelForSelection(sel)
	if ch != p.currentChannel() {
		p.selection = sel
		// Snapshot is keyed by channel; if the channel changed, the
		// previous snapshot is now stale. Mirror what
		// SelectionChangedMsg does so the panel renders "waiting"
		// rather than the previous instrument's book.
		p.snapshot = nil
		p.viewport.Recenter()
	}
	if ch == "" {
		return dashboard.FeedSpec{}
	}
	return dashboard.FeedSpec{Channels: []string{ch}}
}

// Title is empty — the flow detail composite already carries
// instrument identity in the stats bar above.
func (p *FlowBookPanel) Title() string { return "" }

// Capabilities advertises the keys the book pane responds to:
// `d` cycles depth tier (10 → 20 → 50 → 100 → off), `+/-` cycles
// price grouping, `c` recenters (resets viewport + clears any
// transient state). Reaches the panel only when the parent
// composite routes keys to it; in v0.10.0's flow detail layout,
// FlowPanel routes detail-mode keys to the book pane explicitly.
func (p *FlowBookPanel) Capabilities() keymap.Capabilities {
	return keymap.Capabilities{
		ListNav:    true,
		DepthCycle: true,
		Group:      true,
		Recenter:   true,
	}
}

// flowBookGroupCycle is the ordered list of grouping bucket sizes
// the panel rotates through on `+/-`. Zero means "no grouping"
// (raw API levels). The list is intentionally short — five steps
// covers the typical perp tick-size range from $0.10 to $100,
// which is plenty for visual density tuning. Rotating past the
// last entry wraps back to 0.
var flowBookGroupCycle = []float64{0, 0.1, 1, 10, 100}

// flowBookDepthCycle is the ordered list of depth tier caps the
// `d` key cycles through. Matches the canonical 10/20/50/100
// tiers used elsewhere in the codebase.
//
// 0 ("no cap") was previously included as a fifth entry but
// caused the stats line to lie: ladder.LiquidityForTier(0) silently
// falls back to tier-10 liquidity, while the StatsLine label
// reads "DEPTH 0 / BIDLIQ0 / ASKLIQ0 / IMB0" — the user sees
// numbers that disagree with their label. Dropping 0 from the
// cycle keeps the stats line truthful: every label maps to a
// real tier the API actually computed liquidity for.
//
// Users wanting to see all available depth can press `+` to
// widen the price grouping instead — that compresses depth into
// fewer rows without breaking the tier-bound stats.
var flowBookDepthCycle = []int{10, 20, 50, 100}

// applyKey processes one classified key action against the panel.
// Returns the (possibly mutated) panel and whether the key was
// consumed. Routed in from FlowPanel's detail-mode key dispatcher.
func (p *FlowBookPanel) applyKey(action keymap.Action) bool {
	rowCap := ladder.RowCap(p.viewHeight)
	switch action {
	case keymap.ActUp:
		p.viewport.ScrollUp(rowCap)
		return true
	case keymap.ActDown:
		p.viewport.ScrollDown(rowCap)
		return true
	case keymap.ActPageUp:
		p.viewport.PageUp(rowCap)
		return true
	case keymap.ActPageDown:
		p.viewport.PageDown(rowCap)
		return true
	case keymap.ActTop:
		p.viewport.SnapTop(1 << 20)
		return true
	case keymap.ActBottom:
		p.viewport.SnapBottom(1 << 20)
		return true
	case keymap.ActDepthCycle:
		// Find current tier in the cycle; advance one step,
		// wrapping. Unknown values reset to the first.
		idx := -1
		for i, v := range flowBookDepthCycle {
			if v == p.depthTier {
				idx = i
				break
			}
		}
		idx = (idx + 1) % len(flowBookDepthCycle)
		p.depthTier = flowBookDepthCycle[idx]
		p.viewport.Recenter()
		return true
	case keymap.ActGroupUp:
		// Step UP through the group cycle (wider buckets).
		idx := 0
		for i, v := range flowBookGroupCycle {
			if v == p.groupSize {
				idx = i
				break
			}
		}
		idx = (idx + 1) % len(flowBookGroupCycle)
		p.groupSize = flowBookGroupCycle[idx]
		p.viewport.Recenter()
		return true
	case keymap.ActGroupDown:
		// Step DOWN through the group cycle (narrower buckets).
		idx := 0
		for i, v := range flowBookGroupCycle {
			if v == p.groupSize {
				idx = i
				break
			}
		}
		idx = (idx - 1 + len(flowBookGroupCycle)) % len(flowBookGroupCycle)
		p.groupSize = flowBookGroupCycle[idx]
		p.viewport.Recenter()
		return true
	case keymap.ActRecenter:
		p.viewport.Recenter()
		return true
	}
	return false
}

// View renders the canonical single-venue ladder via the shared
// output.RenderSingleVenueLadder. Same visual as `ws perpetuals
// book`: stats line on top (MID/SPREAD/IMB/LIQ/DEPTH/GROUP),
// stacked ask/spread/bid rows, shared PRICE / SIZE / CUM columns,
// level-relative bars, whale ▲ marker, and a full spread label.
//
// width and height are the card's interior dimensions (the
// CardPanel decorator consumes 2 cells of each for the border).
// The shared renderer emits as many rows as the height-derived
// row cap allows; we pad/truncate to fit the card's row count.
//
// If snapshot is nil (no event yet), renders a "waiting" line.
func (p *FlowBookPanel) View(width, height int, ctx dashboard.PanelContext) string {
	p.viewHeight = height
	if width <= 0 || height <= 0 {
		return ""
	}
	if p.snapshot == nil {
		label := "waiting for book…"
		if p.currentChannel() == "" {
			label = ""
		}
		return waitingView(width, height, label, ctx.SpinnerFrame)
	}
	if width < flowBookCompactWidth || height < flowBookMinHeight {
		return p.viewCompactBook(width, height)
	}

	body := output.RenderSingleVenueLadder(output.LadderRenderOpts{
		Snapshot:      p.snapshot,
		DepthTier:     p.depthTier,
		GroupTickSize: p.groupSize,
		Viewport:      p.viewport,
		// Flashes / Sparkline: nil for now. The flow detail
		// version is read-mostly; per-level flash arrows and the
		// MID sparkline are nice-to-have polish for v0.10.1 once
		// we wire a per-pair microprice ring on the panel.
		BarWidth: 12, // narrower than legacy 18 — card is ~50 cells
		Width:    width,
		Height:   height,
		Paused:   false,
	})

	// Pad / truncate to exactly `height` rows so the lipgloss
	// composite layout doesn't shift when the ladder grows or
	// shrinks frame-to-frame. The shared renderer emits a
	// variable-row output (depends on data depth); the card
	// allocated a fixed slot.
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return strings.Join(lines, "\n")
}

// viewCompactBook is the sub-ladder fallback for cramped panes. It
// preserves the actionable top-of-book scan without bars, cumulative
// columns, or stats chrome: bid price on the left, ask price on the
// right, one level per row.
func (p *FlowBookPanel) viewCompactBook(width, height int) string {
	if p.snapshot == nil || width <= 0 || height <= 0 {
		return ""
	}
	rows := make([]string, 0, height)
	if height > 1 {
		rows = append(rows, output.PadRightAnsi(output.BrandGreyMid+output.TruncateAnsi("BID PX | ASK PX", width)+output.Reset, width))
	}

	maxLevels := height - len(rows)
	if maxLevels > len(p.snapshot.Bids) {
		maxLevels = len(p.snapshot.Bids)
	}
	if maxLevels > len(p.snapshot.Asks) {
		maxLevels = len(p.snapshot.Asks)
	}
	if maxLevels < 1 {
		maxLevels = 1
	}
	for i := 0; i < maxLevels && len(rows) < height; i++ {
		bid, ask := "", ""
		if i < len(p.snapshot.Bids) {
			bid = output.FormatBookPrice(p.snapshot.Bids[i].Price)
			if width >= 30 {
				bid += " " + output.FormatBookSize(p.snapshot.Bids[i].Size)
			}
			bid = output.BrandGreen + bid + output.Reset
		}
		if i < len(p.snapshot.Asks) {
			ask = output.FormatBookPrice(p.snapshot.Asks[i].Price)
			if width >= 30 {
				ask += " " + output.FormatBookSize(p.snapshot.Asks[i].Size)
			}
			ask = output.Red + ask + output.Reset
		}
		line := bid
		if ask != "" {
			if line != "" {
				line += output.BrandGreyMid + " | " + output.Reset
			}
			line += ask
		}
		rows = append(rows, output.PadRightAnsi(line, width))
	}
	for len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}
	return strings.Join(rows, "\n")
}

// channelForSelection builds the WS channel string for a selection.
// Returns empty string when the selection lacks any field needed
// to construct a complete channel (Market, Venue, Symbol all
// required).
func channelForSelection(sel dashboard.Selection) string {
	if sel.Market == "" || sel.Venue == "" || sel.Symbol == "" {
		return ""
	}
	return fmt.Sprintf("book.%s.%s.%s", sel.Market, sel.Venue, sel.Symbol)
}

// waitingView renders a single line indicating the panel is
// waiting for the first event. Pads to (width, height) so the
// composite layout doesn't shift when the snapshot lands.
//
// Each caller passes its own `label` — "waiting for book…",
// "waiting for trades…", "loading BTC perps…", etc. The label
// is the panel's responsibility because every flow panel waits
// for something different; a hardcoded label here would lie
// about what the user is actually waiting for. Pass the empty
// string for label when the selection is incomplete (no venue /
// symbol picked yet) and the function renders "no instrument
// selected" instead.
//
// Safe for any (width, height): negative or zero values clamp to
// 0 so callers don't have to gate at the panel edge. If the label
// is longer than width, it's truncated to fit. flow_chart relies
// on this safety because its own View skips width/height bounds
// (delegating to candles.Render only after candles exist) — the
// pre-candle waiting state has to handle tiny panes itself.
func waitingView(width, height int, label string, spinner string) string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	if width == 0 || height == 0 {
		return ""
	}

	if label == "" {
		label = "no instrument selected"
	} else if spinner != "" {
		label = spinner + " " + label
	}

	// Truncate label to width when it doesn't fit. Plain rune
	// truncation is fine — these labels never carry ANSI escapes.
	if output.VisibleWidth(label) > width {
		runes := []rune(label)
		if width <= len(runes) {
			label = string(runes[:width])
		} else {
			label = ""
		}
	}

	pad := (width - output.VisibleWidth(label)) / 2
	if pad < 0 {
		pad = 0
	}
	row := strings.Repeat(" ", pad) + label
	if extra := width - output.VisibleWidth(row); extra > 0 {
		row = row + strings.Repeat(" ", extra)
	}

	mid := height / 2
	var b strings.Builder
	b.Grow(width * height)
	for i := 0; i < height; i++ {
		if i == mid {
			b.WriteString(row)
		} else {
			b.WriteString(strings.Repeat(" ", width))
		}
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// placeholder renders a single message in the middle of an
// otherwise-blank panel. Used for "too small" and similar
// degenerate cases. Same shape contract as waitingView (pads to
// width × height, no trailing newline).
//
// Uses output.VisibleWidth so callers can pass ANSI-styled
// messages (e.g. with brand-grey colour codes) and get correct
// centring without over-padding by the rune count of the escape
// sequences.
func placeholder(width, height int, msg string) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	// Truncate over-long messages so they don't bleed past the panel
	// edge into the neighbouring pane. ANSI-aware so styled error
	// strings (e.g. red-prefixed network errors) keep their escape
	// sequences balanced after the cut.
	if output.VisibleWidth(msg) > width {
		msg = output.TruncateAnsi(msg, width)
	}
	pad := (width - output.VisibleWidth(msg)) / 2
	if pad < 0 {
		pad = 0
	}
	row := strings.Repeat(" ", pad) + msg
	if extra := width - output.VisibleWidth(row); extra > 0 {
		row = row + strings.Repeat(" ", extra)
	}
	mid := height / 2
	var b strings.Builder
	b.Grow(width * height)
	for i := 0; i < height; i++ {
		if i == mid {
			b.WriteString(row)
		} else {
			b.WriteString(strings.Repeat(" ", width))
		}
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Compile-time interface satisfaction.
var _ Panel = (*FlowBookPanel)(nil)
