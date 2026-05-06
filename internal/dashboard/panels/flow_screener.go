package panels

// FlowScreenerPanel — the perp screener that lists every venue's
// perp for a given currency, lets the user navigate with up/down,
// and emits SelectionChangedMsg on every cursor move so the
// detail composite below can warm subscriptions before the user
// drills.
//
// Lifecycle:
//
//   - Init returns a tea.Cmd that fetches the initial REST snapshot.
//   - Update(refreshTickMsg) re-fetches on a timer.
//   - Update(snapshotMsg) installs new rows, preserving the cursor's
//     identity (instrument:venue) across refreshes so a re-fetch
//     doesn't yank the cursor to a different row.
//   - Update(KeyMsg up/down) moves the cursor and emits
//     SelectionChangedMsg via tea.Cmd.
//   - Update(KeyMsg enter) emits a FlowDrillMsg the parent FlowPanel
//     consumes to switch its mode to detail.
//
// Subscriptions: a "stable overscanned window" of trades channels
// for the visible rows, plus the cursor row. v0.10.0 keeps this
// simple — no live last-price flash from those subscriptions yet
// (deferred to v0.10.1 once the basic drill is dogfooded). The
// subscriptions exist to *warm* the detail-pane caches so the
// drill is instant; FlowPanel reads selection identity, not WS
// data, here.
//
// Capabilities: ListNav + Drill — the screener IS the interactive
// panel in flow mode. FlowPanel composes screener mode and detail
// mode at the parent level; the screener doesn't know about
// detail directly, only that Enter drills.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/dashboard/columns"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// flowScreenerRefreshInterval is how often the panel re-fetches
// the REST snapshot. 30s is a balance between freshness and rate-
// limit politeness — perp snapshots aren't intra-second-volatile
// and the WS overlay (v0.10.1) will deliver real-time price
// updates inside the refresh window.
const flowScreenerRefreshInterval = 30 * time.Second

// flowScreenerOverscanRows is how many rows above and below the
// cursor the panel keeps subscribed. The window is anchored on the
// cursor, not on the rendered viewport (the panel doesn't know its
// height — the kernel decides that), so "overscan" here means
// neighbouring rows in the snapshot, not pixels above/below the
// visible area. Reduces flicker when the cursor moves by one and
// the next row's stream needs to be already warm.
const flowScreenerOverscanRows = 5

// FlowDrillMsg is emitted when the user presses Enter on a
// screener row. FlowPanel consumes this to switch its mode from
// screener to detail. Carries the identity of the row that was
// drilled so FlowPanel can install the selection without
// re-walking the screener's row list.
type FlowDrillMsg struct {
	Selection dashboard.Selection
}

// flowScreenerSnapshotMsg carries a fresh REST snapshot back to
// the panel. Internal to the package — the kernel doesn't know
// about it.
type flowScreenerSnapshotMsg struct {
	rows []columns.PerpRow
	err  error
}

// flowScreenerRefreshMsg is the tick that triggers a re-fetch.
// Internal.
type flowScreenerRefreshMsg struct{}

// FlowScreenerScope describes the REST snapshot scope and row
// ordering for the flow screener. Currency and Exchange are both
// optional individually, but callers should provide at least one.
type FlowScreenerScope struct {
	Currency string
	Exchange string
	Market   string
	Sort     string
	SortDesc bool
}

// FlowScreenerPanel implements dashboard.Panel.
type FlowScreenerPanel struct {
	// client fetches the REST snapshot. nil-safe — the panel
	// renders an error placeholder if no client was provided
	// rather than nil-derefing.
	client *api.Client

	// currency is the screener's filter (e.g. "BTC"). Set at
	// construction; doesn't change during the panel's lifetime.
	// Currency-level selection edits land on FlowPanel which
	// destroys+rebuilds the screener.
	currency string

	// exchange is the optional venue filter (e.g. "binance").
	// When set, the screener fetches that venue directly. It can
	// be combined with currency for a narrow venue+currency scope.
	exchange string

	// market is the canonical product family — "perpetuals" for
	// v0.10.0. Stored so the panel doesn't have to know it's a
	// perps-only screener; future market screeners can reuse the
	// same panel type with different columns + market.
	market string

	sortKey  string
	sortDesc bool

	// rows is the latest fetched snapshot. Empty before the first
	// fetch completes (renders "loading…"). Stable order — we
	// don't sort here, the API returns rows in its own order.
	rows []columns.PerpRow

	// cursor is the index into rows of the highlighted row.
	// Clamped to [0, len(rows)-1]; reset to 0 when rows is empty.
	// Cursor moves drive SelectionChangedMsg.
	cursor int

	// loading is true between Init's fetch dispatch and the first
	// snapshotMsg arrival. Drives the placeholder shown in View.
	loading bool

	// lastErr is the most recent fetch error, if any. Cleared on
	// successful fetch. Renders below the table so a transient
	// network blip doesn't blank the whole screener.
	lastErr string

	// lastEmittedSelection tracks the selection most recently sent
	// out via SelectionChangedMsg. Used to dedupe repeated emits
	// when the cursor moves but the row identity is unchanged
	// (e.g. snapshot refresh that preserves cursor position) —
	// keeps the kernel's selection-mutation cycle quiet.
	lastEmittedSelection dashboard.Selection

	// liveTicks holds the most recent WS-derived last-price for each
	// row, keyed by row identity (venue:instrument). Survives
	// snapshot refreshes that reorder rows. Used to overlay
	// real-time LAST values on top of the REST snapshot's
	// MarkPrice — REST is authoritative for OI/funding/spread, WS
	// gives sub-second freshness on the price column.
	//
	// Map rather than per-row struct field so the WS overlay can
	// merge naturally across snapshot refreshes: a fresh REST
	// snapshot rebuilds the rows slice, but liveTicks survives,
	// keyed by identity. The view layer reads from liveTicks to
	// render LAST + flash; if no tick has arrived for a row,
	// the REST MarkPrice is shown unchanged.
	liveTicks map[string]liveTick
}

// liveTick is one row's most recent WS price update. The direction
// drives the LAST column's tick arrow (▲/▼) so the user can see at
// a glance which venues are printing buys vs sells.
type liveTick struct {
	price     float64
	direction string // "up" / "down" — relative to the previous tick on the same row
}

// NewFlowScreenerPanel constructs the panel. currency and market
// are required — empty values produce a panel that can't fetch and
// will render "no currency selected" indefinitely. client may be
// nil for tests; the panel renders "no API client" in that case.
func NewFlowScreenerPanel(client *api.Client, scope FlowScreenerScope) *FlowScreenerPanel {
	if scope.Sort == "" {
		scope.Sort = "volume"
		scope.SortDesc = true
	}
	return &FlowScreenerPanel{
		client:   client,
		currency: strings.ToUpper(scope.Currency),
		exchange: strings.ToLower(scope.Exchange),
		market:   scope.Market,
		sortKey:  scope.Sort,
		sortDesc: scope.SortDesc,
		loading:  true,
	}
}

// Init kicks off the first REST fetch and the refresh timer. Both
// are tea.Cmds; Bubble Tea runs them concurrently. The fetch's
// result arrives as flowScreenerSnapshotMsg; the timer's tick
// arrives as flowScreenerRefreshMsg.
func (p *FlowScreenerPanel) Init() tea.Cmd {
	return tea.Batch(p.fetchCmd(), p.tickCmd())
}

// Update handles refresh ticks, snapshot results, and cursor keys.
func (p *FlowScreenerPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch m := msg.(type) {
	case flowScreenerRefreshMsg:
		// Timer fired — kick off another fetch and re-arm the timer.
		return p, tea.Batch(p.fetchCmd(), p.tickCmd())

	case flowScreenerSnapshotMsg:
		if m.err != nil {
			p.lastErr = m.err.Error()
			p.loading = false
			return p, nil
		}
		// Successful fetch. Preserve cursor identity across the
		// refresh: if the previously-highlighted row is still in
		// the new snapshot, keep the cursor on it; otherwise
		// clamp to the nearest valid index.
		var prevID string
		if p.cursor >= 0 && p.cursor < len(p.rows) {
			prevID = rowIdentity(p.rows[p.cursor])
		}
		p.rows = m.rows
		p.lastErr = ""
		p.loading = false
		p.cursor = findRowByIdentity(p.rows, prevID, p.cursor)
		// Cursor identity changed → emit SelectionChangedMsg so the
		// detail composite (or future caches) re-aligns. If the
		// previous cursor row vanished from the snapshot, we
		// clamp + re-emit; if it survived, the identity is the
		// same and we don't emit (avoid unnecessary kernel work).
		// Capture old/new by value before mutating — the cmd's
		// closure must not read p.lastEmittedSelection at execution
		// time (cmds run concurrently with Update).
		if cur := p.currentSelection(); !selectionsEqual(cur, p.lastEmittedSelection) {
			old := p.lastEmittedSelection
			p.lastEmittedSelection = cur
			return p, makeSelectionChangedCmd(old, cur)
		}
		return p, nil

	case dashboard.FeedTickMsg:
		// WS overlay: a trade tick on one of the screener's
		// subscribed channels updates the LAST column for that row
		// without waiting for the next 30s REST refresh.
		//
		// REST stays authoritative for everything else (OI,
		// funding, spread, row set). The overlay is intentionally
		// scoped to LAST because that's the field that benefits
		// most from sub-second freshness — OI and funding move on
		// minute-or-longer timescales.
		p.applyTradeTick(m.Event.Channel, m.Event.Data)
		return p, nil

	case tea.KeyMsg:
		return p.handleKey(m)
	}
	return p, nil
}

// rowWithOverlay returns a copy of r with MarkPrice replaced by
// the most recent WS tick price for r's identity, if one exists.
// Falls back to r unchanged when no tick has arrived yet — the
// REST-snapshot mark_price is fine for the initial view.
//
// PerpRow is a value type (no pointers), so the copy is cheap and
// the overlay can't accidentally mutate the underlying snapshot
// slice. The screener's stored rows stay REST-truth; the overlay
// is per-render-frame.
func (p *FlowScreenerPanel) rowWithOverlay(r columns.PerpRow) columns.PerpRow {
	if p.liveTicks == nil {
		return r
	}
	id := r.Exchange + ":" + r.InstrumentName
	tick, ok := p.liveTicks[id]
	if !ok {
		return r
	}
	if p.market == "spot" {
		r.LastPriceClose = tick.price
	} else {
		r.MarkPrice = tick.price
	}
	return r
}

func (p *FlowScreenerPanel) scopeLabel() string {
	switch {
	case p.currency != "" && p.exchange != "":
		return p.currency + "@" + p.exchange
	case p.currency != "":
		return p.currency
	case p.exchange != "":
		return p.exchange
	default:
		return "all"
	}
}

// applyTradeTick decodes a WS trade payload and updates the
// liveTicks map for the matching row identity. No-op when:
//   - the channel doesn't match the screener's market prefix
//   - the channel doesn't resolve to a known row
//   - the payload decodes to a zero/negative price (skip rather
//     than corrupt)
//
// Direction is computed relative to the previous tick on the same
// row, not the trade's own buy/sell direction. A tick at $80,000
// after a previous $79,990 is "up" regardless of whether the
// matching trade was a buy or a sell — the visual indicator
// reflects price drift, which is what the user cares about for a
// screener column.
func (p *FlowScreenerPanel) applyTradeTick(channel string, data []byte) {
	// Channel format: trades.<market>.<venue>.<symbol>. We split
	// rather than re-parse because the venue/symbol is the
	// identity key we need; the market is implicit from p.market
	// but we still verify the prefix to avoid mismatched panels'
	// channels leaking through.
	prefix := "trades." + p.market + "."
	if !strings.HasPrefix(channel, prefix) {
		return
	}
	rest := channel[len(prefix):]
	dotIdx := strings.IndexByte(rest, '.')
	if dotIdx < 0 {
		return
	}
	venue := rest[:dotIdx]
	symbol := rest[dotIdx+1:]
	id := venue + ":" + symbol

	var t struct {
		Price float64 `json:"price"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return
	}
	if t.Price <= 0 {
		return
	}

	if p.liveTicks == nil {
		p.liveTicks = make(map[string]liveTick, 32)
	}
	prev := p.liveTicks[id]
	dir := prev.direction
	switch {
	case prev.price == 0:
		// First tick for this row — no prior price to compare.
		dir = ""
	case t.Price > prev.price:
		dir = "up"
	case t.Price < prev.price:
		dir = "down"
		// equal: keep prior direction so a flat run doesn't blank
		// the indicator on every tick that happens to match.
	}
	p.liveTicks[id] = liveTick{price: t.Price, direction: dir}
}

// handleKey processes cursor navigation and drill-into.
func (p *FlowScreenerPanel) handleKey(m tea.KeyMsg) (Panel, tea.Cmd) {
	if len(p.rows) == 0 {
		// No rows yet — keys are no-ops. Nothing to navigate.
		return p, nil
	}
	switch m.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
			return p, p.afterCursorMove()
		}
	case "down", "j":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
			return p, p.afterCursorMove()
		}
	case "enter":
		// Drill: emit FlowDrillMsg with the current selection.
		// FlowPanel consumes this to switch to detail mode.
		// Snapshot the selection by value — the cmd must not
		// invoke p.currentSelection() at execution time, which
		// would race a concurrent cursor move and could drill the
		// wrong row.
		return p, makeDrillCmd(p.currentSelection())
	}
	return p, nil
}

// afterCursorMove emits SelectionChangedMsg if the cursor's row
// identity is different from the last emitted selection.
// Deduping at this level keeps the kernel's selection-mutation
// cycle quiet on rapid arrow-key presses where the row identity
// happens to be the same (shouldn't happen in screener but the
// invariant is cheap).
//
// The returned cmd captures `old` and `new` by value at this
// point — never reads p.lastEmittedSelection or p.cursor at cmd
// execution time. Bubble Tea runs cmds concurrently with Update,
// so any subsequent state mutation here would race the cmd's
// closure read; pre-snapshot the values to avoid that class of
// bug.
func (p *FlowScreenerPanel) afterCursorMove() tea.Cmd {
	cur := p.currentSelection()
	if selectionsEqual(cur, p.lastEmittedSelection) {
		return nil
	}
	old := p.lastEmittedSelection
	p.lastEmittedSelection = cur
	return makeSelectionChangedCmd(old, cur)
}

// currentSelection returns the Selection corresponding to the
// cursor row. Empty/zero when rows is empty or cursor is out of
// range.
func (p *FlowScreenerPanel) currentSelection() dashboard.Selection {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return dashboard.Selection{Currency: p.currency, Market: p.market}
	}
	r := p.rows[p.cursor]
	return dashboard.Selection{
		Currency: p.currency,
		Market:   p.market,
		Venue:    r.Exchange,
		Symbol:   r.InstrumentName,
	}
}

// makeSelectionChangedCmd returns a tea.Cmd that fires
// SelectionChangedMsg with the given (old, new) selection pair.
// Captures both values by value — the cmd MUST NOT read panel
// state at execution time because Bubble Tea runs cmds
// concurrently with Update, so a subsequent cursor move could
// have already overwritten p.lastEmittedSelection.
func makeSelectionChangedCmd(old, sel dashboard.Selection) tea.Cmd {
	return func() tea.Msg {
		return dashboard.SelectionChangedMsg{Old: old, New: sel}
	}
}

// makeDrillCmd returns a tea.Cmd that fires FlowDrillMsg with the
// given selection. Same closure-by-value rule as the selection
// command — capturing p.currentSelection() at cmd-execution time
// would race a concurrent cursor move and could drill the wrong
// row.
func makeDrillCmd(sel dashboard.Selection) tea.Cmd {
	return func() tea.Msg {
		return FlowDrillMsg{Selection: sel}
	}
}

// Subscriptions returns the trades channels for the cursor-overscan
// window. Anchored on the cursor: cursor row is always in the set,
// plus flowScreenerOverscanRows neighbouring rows above and below
// in the snapshot order.
//
// Bounded by flowScreenerOverscanRows*2 + 1 channels regardless of
// snapshot size, so a 100-row snapshot doesn't subscribe to 100
// trades streams. The detail composite's panels declare their own
// subscriptions on top; FeedRouter dedupes.
//
// Returns empty when there are no rows yet (loading state).
func (p *FlowScreenerPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	if len(p.rows) == 0 {
		return dashboard.FeedSpec{}
	}
	low, high := p.overscanWindow()
	channels := make([]string, 0, high-low+1)
	for i := low; i <= high; i++ {
		r := p.rows[i]
		if r.Exchange == "" || r.InstrumentName == "" {
			continue
		}
		ch := fmt.Sprintf("trades.%s.%s.%s", p.market, r.Exchange, r.InstrumentName)
		channels = append(channels, ch)
	}
	return dashboard.FeedSpec{Channels: channels}
}

// overscanWindow returns the [low, high] inclusive index range of
// rows the screener wants subscribed. Anchored on the cursor with
// flowScreenerOverscanRows above and below; clamped to valid
// indices.
func (p *FlowScreenerPanel) overscanWindow() (int, int) {
	low := p.cursor - flowScreenerOverscanRows
	high := p.cursor + flowScreenerOverscanRows
	if low < 0 {
		low = 0
	}
	if high >= len(p.rows) {
		high = len(p.rows) - 1
	}
	return low, high
}

// Title is empty — the screener composite doesn't carry chrome.
func (p *FlowScreenerPanel) Title() string { return "" }

// Capabilities — ListNav for cursor up/down, Drill for Enter.
// Listed so the kernel's footer hints advertise the keys.
func (p *FlowScreenerPanel) Capabilities() keymap.Capabilities {
	return keymap.Capabilities{
		ListNav: true,
		Drill:   true,
	}
}

// View renders the screener: header row, row-per-row table, with
// the cursor row highlighted. Loading and error states render
// inline placeholders rather than blanking the panel.
func (p *FlowScreenerPanel) View(width, height int, ctx dashboard.PanelContext) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	if p.client == nil {
		return placeholder(width, height, "(no API client)")
	}
	if p.loading {
		// Currency-specific label so the user can distinguish a
		// stuck `dash flow BTC` from a stuck `dash flow ETH` at a
		// glance. The shared waitingView helper centres a single
		// line; for v0.10.0 that's fine — the snapshot fetch is a
		// fast REST call and the loading state is brief.
		return waitingView(width, height, "loading "+p.scopeLabel()+" "+p.market+"…", ctx.SpinnerFrame)
	}
	if len(p.rows) == 0 && p.lastErr != "" {
		return placeholder(width, height, "✗ "+p.lastErr)
	}
	if len(p.rows) == 0 {
		return placeholder(width, height, "no instruments for "+p.scopeLabel())
	}

	grey := output.BrandGreyMid
	reset := output.Reset

	// Header row: pad each column to its declared width, separator
	// space between columns. If width is too narrow, columns from
	// the right are dropped (price/volume/funding are less
	// important than instrument/last for screener navigation).
	visibleCols := p.columnsFitting(width)
	if len(visibleCols) == 0 {
		return p.viewInstrumentOnly(width, height)
	}
	header := buildRow(visibleCols, func(c columns.Column[columns.PerpRow]) string {
		return c.Header
	})
	header = grey + header + reset
	headerVisible := output.VisibleWidth(header)
	if headerVisible < width {
		header = header + strings.Repeat(" ", width-headerVisible)
	}

	rows := make([]string, 0, height)
	rows = append(rows, header)

	// Row budget: height - 1 header - 1 footer (for error/status).
	// If lastErr is empty we use height - 1.
	maxRows := height - 1
	if p.lastErr != "" {
		maxRows -= 1
	}
	if maxRows < 1 {
		maxRows = 1
	}

	// Render rows, oldest at top, cursor row highlighted.
	displayLow, displayHigh := p.viewportWindow(maxRows)
	for i := displayLow; i <= displayHigh; i++ {
		// Apply the WS overlay: if a live tick has arrived for this
		// row's identity, stamp the live price onto a copy of the
		// row before passing to the column extractors. The
		// extractors are agnostic — they see a regular PerpRow with
		// updated MarkPrice; the column rendering uses the stamped
		// price for LAST. SPREAD is computed from BidPrice/
		// AskPrice which the WS overlay doesn't update (those move
		// faster than mark and are less useful to the screener
		// scan), so SPREAD remains REST-fresh.
		row := p.rowWithOverlay(p.rows[i])
		line := buildRowFromCols(visibleCols, row)
		// Pad to width.
		visible := output.VisibleWidth(line)
		if visible < width {
			line = line + strings.Repeat(" ", width-visible)
		}
		// Highlight cursor row by inverting (background fill).
		if i == p.cursor {
			line = output.BrandGreenBg + output.Bold + line + reset
		}
		rows = append(rows, line)
	}

	// Pad with blank rows so the panel always renders height rows.
	for len(rows) < height-1 {
		rows = append(rows, strings.Repeat(" ", width))
	}

	// Footer: error or status hint. Truncate over-long error
	// strings (e.g. wrapped network errors carrying full URLs and
	// stack hints) so they don't bleed past the panel edge into
	// the neighbouring pane. ANSI-aware: the red prefix and reset
	// stay balanced after the cut.
	footer := ""
	if p.lastErr != "" {
		footer = output.Red + "✗ " + p.lastErr + reset
	}
	if footer != "" {
		footerVisible := output.VisibleWidth(footer)
		if footerVisible > width {
			footer = output.TruncateAnsi(footer, width)
			footerVisible = output.VisibleWidth(footer)
		}
		if footerVisible < width {
			footer = footer + strings.Repeat(" ", width-footerVisible)
		}
		rows = append(rows, footer)
	} else if len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}

	return strings.Join(rows, "\n")
}

// viewInstrumentOnly is the narrowest useful screener mode. It
// keeps navigation alive at tiny widths by showing only the row
// identity, truncated to the pane width.
func (p *FlowScreenerPanel) viewInstrumentOnly(width, height int) string {
	rows := make([]string, 0, height)
	header := output.BrandGreyMid + output.TruncateAnsi("INSTRUMENT", width) + output.Reset
	rows = append(rows, output.PadRightAnsi(header, width))

	maxRows := height - 1
	if maxRows < 0 {
		maxRows = 0
	}
	displayLow, displayHigh := p.viewportWindow(maxRows)
	for i := displayLow; i <= displayHigh && len(rows) < height; i++ {
		row := p.rowWithOverlay(p.rows[i])
		label := row.Exchange + ":" + row.InstrumentName
		line := output.PadRightAnsi(label, width)
		if i == p.cursor {
			line = output.BrandGreenBg + output.Bold + line + output.Reset
		}
		rows = append(rows, line)
	}
	for len(rows) < height {
		rows = append(rows, strings.Repeat(" ", width))
	}
	return strings.Join(rows, "\n")
}

// columnsFitting returns the prefix of PerpColumns that fits in
// `width` cells. Columns are joined with single spaces, so the
// total visible width is sum(col.Width) + (len(cols)-1) gutters
// + 3 cells leading gutter for visual breathing room.
func (p *FlowScreenerPanel) columnsFitting(width int) []columns.Column[columns.PerpRow] {
	const leadingGutter = 3
	total := leadingGutter
	cols := p.columnSet()
	for i, c := range cols {
		next := total + c.Width
		if i > 0 {
			next += 1 // inter-column gutter
		}
		if next > width {
			return cols[:i]
		}
		total = next
	}
	return cols
}

func (p *FlowScreenerPanel) columnSet() []columns.Column[columns.PerpRow] {
	switch p.market {
	case "futures":
		return columns.FuturesColumns
	case "spot":
		return columns.SpotColumns
	default:
		return columns.PerpColumns
	}
}

// viewportWindow returns the [low, high] inclusive row indices to
// render given the visible row budget and cursor position. Tries
// to keep the cursor centred; clamps to valid indices.
func (p *FlowScreenerPanel) viewportWindow(maxRows int) (int, int) {
	if maxRows >= len(p.rows) {
		return 0, len(p.rows) - 1
	}
	half := maxRows / 2
	low := p.cursor - half
	if low < 0 {
		low = 0
	}
	high := low + maxRows - 1
	if high >= len(p.rows) {
		high = len(p.rows) - 1
		low = high - maxRows + 1
		if low < 0 {
			low = 0
		}
	}
	return low, high
}

// buildRow joins the headers/cells of `cols` with single-space
// gutters and a leading 3-space gutter. extract pulls the cell
// string from a column; for the header row it's c.Header.
func buildRow[T any](cols []columns.Column[T], extract func(columns.Column[T]) string) string {
	var b strings.Builder
	b.WriteString("   ") // leading gutter
	for i, c := range cols {
		if i > 0 {
			b.WriteByte(' ')
		}
		s := extract(c)
		if len(s) > c.Width {
			s = s[:c.Width]
		}
		b.WriteString(s)
		if pad := c.Width - len(s); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	return b.String()
}

// buildRowFromCols builds a data row by calling each column's
// Extract on the row.
func buildRowFromCols(cols []columns.Column[columns.PerpRow], row columns.PerpRow) string {
	return buildRow(cols, func(c columns.Column[columns.PerpRow]) string {
		return c.Extract(row)
	})
}

// rowIdentity returns the identity string for a row — venue +
// instrument. Used to preserve cursor position across REST
// refreshes when the underlying snapshot may reorder rows.
func rowIdentity(r columns.PerpRow) string {
	return r.Exchange + ":" + r.InstrumentName
}

// findRowByIdentity returns the index of the row matching id, or
// fallback (clamped to valid range) if not found. A vanished row
// (delisting, venue outage) is the realistic case where this
// matters; the cursor stays close to where it was rather than
// jumping to row 0.
func findRowByIdentity(rows []columns.PerpRow, id string, fallback int) int {
	if id != "" {
		for i, r := range rows {
			if rowIdentity(r) == id {
				return i
			}
		}
	}
	if fallback < 0 {
		fallback = 0
	}
	if len(rows) == 0 {
		return 0
	}
	if fallback >= len(rows) {
		fallback = len(rows) - 1
	}
	return fallback
}

// selectionsEqual reports whether two Selections refer to the
// same instrument identity. We compare on the dimensions the
// detail panes care about (Market+Venue+Symbol+Currency) — the
// other Selection fields aren't used by flow.
func selectionsEqual(a, b dashboard.Selection) bool {
	return a.Currency == b.Currency &&
		a.Market == b.Market &&
		a.Venue == b.Venue &&
		a.Symbol == b.Symbol
}

// fetchCmd returns a tea.Cmd that fetches a fresh multi-venue
// snapshot for p.currency and posts the result as
// flowScreenerSnapshotMsg.
//
// The /api/v1/perpetuals/snapshot endpoint requires an `exchange`
// query parameter — passing currency alone returns HTTP 400. To
// build a multi-venue screener we therefore:
//
//  1. Hit /api/v1/perpetuals/catalog?currency=<ccy> once to
//     discover the set of exchanges that list a perp for the
//     currency. Catalog is cheap (no per-row data, just identity)
//     and the venue set is what the screener actually needs to
//     enumerate.
//
//  2. Fan out one snapshot call per exchange in parallel. Each
//     returns 1–3 rows (BTC-PERPETUAL, BTC_USDC-PERPETUAL, etc.
//     on deribit; usually 1 row on binance/okx). We merge them
//     into a single PerpRow slice.
//
//  3. Per-venue errors are non-fatal: if one exchange errors out
//     (rate limit, listing changed, transient 5xx) the screener
//     still renders the rows from the venues that succeeded. Only
//     when EVERY venue errors do we surface an aggregate error.
//
// Performance: typical fan-out is ~6–10 venues for major
// currencies; HTTP 1.1 keep-alives plus the api.Client's internal
// transport pool make the parallel calls land in roughly the same
// wall time as one sequential call. The catalog probe is the only
// added serial step.
func (p *FlowScreenerPanel) fetchCmd() tea.Cmd {
	return func() tea.Msg {
		if p.client == nil {
			return flowScreenerSnapshotMsg{err: fmt.Errorf("no API client")}
		}
		if p.exchange != "" {
			rows, err := p.fetchOneVenueSnapshot(p.exchange)
			if err != nil {
				return flowScreenerSnapshotMsg{err: err}
			}
			p.sortRows(rows)
			return flowScreenerSnapshotMsg{rows: rows}
		}
		exchanges, err := p.fetchVenueList()
		if err != nil {
			return flowScreenerSnapshotMsg{err: err}
		}
		if len(exchanges) == 0 {
			// Catalog returned no venues for this currency — return
			// an empty rows slice rather than an error so the View
			// can render the "no instruments for {currency}"
			// placeholder. Distinguishes "API said nothing here"
			// from "API failed."
			return flowScreenerSnapshotMsg{rows: nil}
		}
		rows, errs := p.fetchSnapshotsParallel(exchanges)
		if len(rows) == 0 && len(errs) > 0 {
			// Every fan-out call failed. Surface the first error so
			// the user sees something actionable; a "5 venues failed"
			// summary is less useful than the actual underlying
			// HTTP/network reason.
			return flowScreenerSnapshotMsg{err: errs[0]}
		}
		p.sortRows(rows)
		return flowScreenerSnapshotMsg{rows: rows}
	}
}

// fetchVenueList hits the perpetuals catalog and returns the
// distinct set of exchange identifiers carrying the currency. The
// catalog rows are deliberately minimal ({exchange,
// instrument_name}); we only need the exchange field to seed the
// fan-out.
func (p *FlowScreenerPanel) fetchVenueList() ([]string, error) {
	var env struct {
		Success bool `json:"success"`
		Data    []struct {
			Exchange string `json:"exchange"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	params := &api.RequestParams{Currency: p.currency}
	body, err := p.client.Get(p.catalogEndpoint(), params)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if !env.Success && env.Error != nil {
		return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
	}
	seen := make(map[string]struct{}, len(env.Data))
	out := make([]string, 0, len(env.Data))
	for _, row := range env.Data {
		if row.Exchange == "" {
			continue
		}
		if _, dup := seen[row.Exchange]; dup {
			continue
		}
		seen[row.Exchange] = struct{}{}
		out = append(out, row.Exchange)
	}
	return out, nil
}

// fetchSnapshotsParallel fans out one snapshot call per exchange,
// runs them concurrently, and merges the results in deterministic
// (exchange, instrument_name) order so a re-render with the same
// underlying data produces an identical row sequence.
//
// Returns (rows, errs). errs is the slice of per-venue errors;
// rows is the merged success slice. An empty rows + non-empty
// errs means every venue failed — caller decides how to surface.
func (p *FlowScreenerPanel) fetchSnapshotsParallel(exchanges []string) ([]columns.PerpRow, []error) {
	type result struct {
		exchange string
		rows     []columns.PerpRow
		err      error
	}

	ch := make(chan result, len(exchanges))
	for _, ex := range exchanges {
		go func(ex string) {
			rows, err := p.fetchOneVenueSnapshot(ex)
			ch <- result{exchange: ex, rows: rows, err: err}
		}(ex)
	}

	merged := make(map[string][]columns.PerpRow, len(exchanges))
	var errs []error
	for i := 0; i < len(exchanges); i++ {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		merged[r.exchange] = r.rows
	}

	// Deterministic merge: sort exchanges, append rows in that
	// order. Within a venue the API returns rows in its own order;
	// we don't second-guess that — sorting by instrument_name
	// across venues would scramble the natural "every venue's
	// USDT-margined contract first" grouping that makes the
	// screener readable.
	sortedEx := make([]string, 0, len(merged))
	for ex := range merged {
		sortedEx = append(sortedEx, ex)
	}
	sort.Strings(sortedEx)

	total := 0
	for _, rows := range merged {
		total += len(rows)
	}
	out := make([]columns.PerpRow, 0, total)
	for _, ex := range sortedEx {
		out = append(out, merged[ex]...)
	}
	return out, errs
}

// fetchOneVenueSnapshot runs a single
// /api/v1/perpetuals/snapshot?currency=&exchange= call and decodes
// it into a slice of PerpRow. Returns an empty slice + nil error
// when the venue replies success-but-empty (e.g. it lists the
// currency but the snapshot window had no data).
func (p *FlowScreenerPanel) fetchOneVenueSnapshot(exchange string) ([]columns.PerpRow, error) {
	var env struct {
		Success bool              `json:"success"`
		Data    []columns.PerpRow `json:"data"`
		Error   *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	params := &api.RequestParams{
		Currency: p.currency,
		Exchange: exchange,
	}
	body, err := p.client.Get(p.snapshotEndpoint(), params)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}
	if !env.Success && env.Error != nil {
		return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
	}
	// The snapshot rows from the API don't always carry the
	// exchange field on every venue — some return it, some
	// don't. Stamp it in here so downstream code (cursor identity,
	// channel-builder) can rely on row.Exchange always being set.
	for i := range env.Data {
		if env.Data[i].Exchange == "" {
			env.Data[i].Exchange = exchange
		}
	}
	return env.Data, nil
}

func (p *FlowScreenerPanel) catalogEndpoint() string {
	switch p.market {
	case "futures":
		return api.FuturesCatalog
	case "spot":
		return api.SpotCatalog
	default:
		return api.PerpsCatalog
	}
}

func (p *FlowScreenerPanel) snapshotEndpoint() string {
	switch p.market {
	case "futures":
		return api.FuturesSnapshot
	case "spot":
		return api.SpotSnapshot
	default:
		return api.PerpsSnapshot
	}
}

func (p *FlowScreenerPanel) sortRows(rows []columns.PerpRow) {
	if len(rows) < 2 {
		return
	}
	key := p.sortKey
	desc := p.sortDesc
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if key == "instrument" {
			left := rowIdentity(a)
			right := rowIdentity(b)
			if desc {
				return left > right
			}
			return left < right
		}
		av := p.sortValue(a, key)
		bv := p.sortValue(b, key)
		if av == bv {
			return rowIdentity(a) < rowIdentity(b)
		}
		if desc {
			return av > bv
		}
		return av < bv
	})
}

func (p *FlowScreenerPanel) sortValue(r columns.PerpRow, key string) float64 {
	switch key {
	case "last":
		return r.Last(p.market)
	case "spread":
		return r.Spread()
	case "volume":
		if p.market == "spot" {
			return r.Volume24h
		}
		return r.Volume24hUSD
	case "quote-volume":
		return r.QuoteVolume24h
	case "liquidity":
		return r.TotalLiquidityClose
	case "oi":
		return r.OI * r.Last(p.market)
	case "funding":
		return r.FundingRate
	case "basis":
		return r.Basis()
	case "dte":
		return r.DaysToExpiry
	default:
		return 0
	}
}

// tickCmd returns a tea.Cmd that waits flowScreenerRefreshInterval
// then posts flowScreenerRefreshMsg. Used to schedule periodic
// REST refreshes; Update batches this with a fresh fetchCmd to
// keep the cycle going.
func (p *FlowScreenerPanel) tickCmd() tea.Cmd {
	return tea.Tick(flowScreenerRefreshInterval, func(time.Time) tea.Msg {
		return flowScreenerRefreshMsg{}
	})
}

// Compile-time interface satisfaction.
var _ Panel = (*FlowScreenerPanel)(nil)
