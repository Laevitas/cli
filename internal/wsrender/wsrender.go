// Package wsrender implements the live-updating terminal renderer for
// `laevitas ws`. It owns a small rolling buffer of recent events and dispatches
// per-channel-type to format columns appropriately (trades vs ticker vs vt
// vs predictions).
//
// Used only when stdout is a TTY. NDJSON output is produced by the cmd
// layer directly without involving this package.
//
// The renderer is built on Bubble Tea (charmbracelet/bubbletea) — a Go TUI
// framework with a frame-diffing renderer that handles Windows Terminal's
// raw-mode quirks correctly. We tried hand-rolled ANSI escapes first; they
// scrolled instead of redrawing on Windows and corrupted the buffer.
package wsrender

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/tapefilter"
	"github.com/laevitas/cli/internal/wsclient"
)

// maxRows caps how many recent events the renderer keeps in the rolling
// ring buffer. Sized to comfortably fill a tall terminal (iTerm / Windows
// Terminal / VS Code typically run 40-60 rows). The renderer trims to
// the actual visible height per frame, so the only cost on a small
// terminal is ~50 KB of resident memory holding events that won't render
// — a non-issue.
//
// Was 18 for a default 80x24 terminal; on bigger windows that left ~70%
// of the screen blank. v0.8.2 bumped to 100.
const maxRows = 100

// LiveTable is the live-updating terminal renderer. The cmd layer wires
// events from wsclient.Events into Push; Run starts the Bubble Tea program
// which blocks until the user presses 'q' / Ctrl-C.
//
// Push and SetLastError are safe to call from any goroutine — they update
// shared state under a mutex, and the Bubble Tea program polls that state
// on a tick. Bubble Tea's own message bus could carry the events directly,
// but using a shared buffer keeps this package independent of how the
// caller produces events.
type LiveTable struct {
	channels []string

	mu      sync.Mutex
	events  []wsclient.Event // ring buffer, most recent at end
	updates int64
	startAt time.Time
	lastErr string
}

// NewLiveTable creates a renderer for the given subscription channels. The
// channels are decorative — used for the header — and don't affect dispatch.
func NewLiveTable(channels []string) *LiveTable {
	return &LiveTable{
		channels: channels,
		events:   make([]wsclient.Event, 0, maxRows),
		startAt:  time.Now(),
	}
}

// Push adds an event to the ring buffer. Safe to call from any goroutine.
func (lt *LiveTable) Push(ev wsclient.Event) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.updates++
	if len(lt.events) >= maxRows {
		copy(lt.events, lt.events[1:])
		lt.events = lt.events[:maxRows-1]
	}
	lt.events = append(lt.events, ev)
}

// SetLastError records the most recent soft error from wsclient. Displayed
// dimmed in the footer until the next event arrives.
func (lt *LiveTable) SetLastError(msg string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	lt.lastErr = msg
}

// snapshot copies the renderer's mutable state under lock so the Bubble Tea
// View() function can read consistent data without blocking the producer.
func (lt *LiveTable) snapshot() (events []wsclient.Event, updates int64, elapsed time.Duration, lastErr string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	events = make([]wsclient.Event, len(lt.events))
	copy(events, lt.events)
	return events, lt.updates, time.Since(lt.startAt), lt.lastErr
}

// Run starts the Bubble Tea program. Blocks until the user quits or the
// stream ends. The caller's event/error pumps run in their own goroutines
// and feed the LiveTable via Push / SetLastError; the program just polls
// snapshot() on a tick.
func (lt *LiveTable) Run() error {
	prog := tea.NewProgram(
		newModel(lt),
		tea.WithAltScreen(), // dedicated alt-screen buffer; restored on exit
		tea.WithMouseCellMotion(),
	)
	_, err := prog.Run()
	return err
}

// ─── Bubble Tea model ───────────────────────────────────────────────────────

// model is the Bubble Tea state. Most "state" lives on LiveTable since it's
// updated from non-Tea goroutines; the model just captures the terminal
// size and a pointer back to the table.
type model struct {
	table  *LiveTable
	width  int
	height int

	// paused freezes the visible buffer in place (events keep arriving
	// in lt.events but renderEvents is called with a frozen snapshot).
	paused     bool
	pausedSnap []wsclient.Event
	minUSD     float64
	// helpOpen toggles the keybinding overlay in the body.
	helpOpen bool
}

func newModel(lt *LiveTable) model {
	return model{table: lt, width: 100, height: 30}
}

// tickMsg fires every 100ms and triggers a re-render. Bubble Tea's renderer
// diffs the View output against what's on screen, so re-rendering at a fixed
// cadence with no actual changes is cheap (no redraw happens).
type tickMsg time.Time

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Init() tea.Cmd {
	return tickEvery(100 * time.Millisecond)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Route through the shared classifyKey vocabulary (keymap.go)
		// so every TUI surface dispatches identically. Rolling tape
		// only acts on the "always" bindings — list/ladder actions
		// fall through to no-op.
		switch classifyKey(msg.String()) {
		case actQuit:
			return m, tea.Quit
		case actPause:
			m.paused = !m.paused
			if m.paused {
				ev, _, _, _ := m.table.snapshot()
				m.pausedSnap = ev
			} else {
				m.pausedSnap = nil
			}
			return m, nil
		case actHelp:
			m.helpOpen = !m.helpOpen
			return m, nil
		case actTapeFilter:
			if m.hasTradeStreams() {
				m.minUSD = tapefilter.Next(m.minUSD)
			}
			return m, nil
		case actEsc:
			// Esc closes help if open; otherwise no-op (rolling tape
			// has no drill-down to back out of).
			if m.helpOpen {
				m.helpOpen = false
			}
			return m, nil
		}
	case tea.MouseMsg:
		// Wheel events on the rolling tape toggle pause. Rolling tape
		// isn't a scrollable list — but pausing on wheel-up matches
		// what users instinctively do (stop the tape so they can read
		// it). Click events are NOT consumed so the terminal keeps
		// native click-drag-to-select for copy-paste.
		switch classifyMouse(msg.Button) {
		case actWheelUp:
			if !m.paused {
				m.paused = true
				ev, _, _, _ := m.table.snapshot()
				m.pausedSnap = ev
			}
			return m, nil
		case actWheelDown:
			if m.paused {
				m.paused = false
				m.pausedSnap = nil
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tickMsg:
		// Schedule the next tick; that's all we need to keep View running.
		return m, tickEvery(100 * time.Millisecond)
	}
	return m, nil
}

func (m model) View() string {
	events, updates, elapsed, lastErr := m.table.snapshot()
	if m.paused && m.pausedSnap != nil {
		events = m.pausedSnap
	}
	unfilteredEvents := len(events)
	if m.hasTradeStreams() {
		events = filterTradeTapeEvents(events, m.minUSD)
	}

	width := m.width
	if width <= 0 {
		width = 100
	}

	if m.helpOpen {
		return renderHelpOverlay("rolling tape", width)
	}

	// Cap the visible body to fit the current terminal height so the table
	// never overflows the screen. The chrome (header + separator + column
	// row + blank + footer) takes 5 lines; everything else is data rows.
	height := m.height
	if height <= 0 {
		height = 30
	}
	const chrome = 5
	visibleRows := height - chrome
	if visibleRows < 1 {
		visibleRows = 1
	}

	var b strings.Builder

	// Header — sticky, brand-styled, shows channel + update count + rate
	b.WriteString(renderHeader(m.table.channels, updates, elapsed, width))
	b.WriteByte('\n')

	// Separator
	b.WriteString(output.BrandGreyMid + strings.Repeat("─", width) + output.Reset)
	b.WriteByte('\n')

	// Body — dispatched by channel type. Trim to visibleRows newest first
	// so we never overflow the terminal height.
	if len(events) == 0 {
		b.WriteString(output.BrandGreyMid)
		if m.minUSD > 0 && unfilteredEvents > 0 {
			b.WriteString("  no trades >= " + tapefilter.Label(m.minUSD))
		} else {
			b.WriteString("  waiting for events...")
		}
		b.WriteString(output.Reset)
		b.WriteByte('\n')
	} else {
		// Renderers take the slice oldest→newest internally and reverse on
		// display, so we keep the tail (most recent visibleRows events).
		shown := events
		if len(shown) > visibleRows {
			shown = shown[len(shown)-visibleRows:]
		}
		b.WriteString(renderEvents(shown, width, showExchangeColumn(m.table.channels)))
	}

	// Footer — keypress hint + soft error if present. Hint mirrors the
	// standard keybinding vocabulary (see renderHelpOverlay) so users
	// only have to learn it once across every TUI surface.
	b.WriteByte('\n')
	b.WriteString(output.BrandGreyMid)
	if lastErr != "" {
		b.WriteString("⚠ " + truncate(lastErr, width-20) + "  ")
	}
	if m.paused {
		b.WriteString(output.Yellow + "PAUSED   " + output.BrandGreyMid)
	}
	if m.minUSD > 0 && m.hasTradeStreams() {
		b.WriteString("min " + tapefilter.Label(m.minUSD) + "   ")
	}
	b.WriteString(footerHints(m.footerSurface()))
	b.WriteString(output.Reset)

	return b.String()
}

func (m model) hasTradeStreams() bool {
	for _, ch := range m.table.channels {
		if strings.HasPrefix(ch, "trades.") {
			return true
		}
	}
	return false
}

func (m model) footerSurface() string {
	if m.hasTradeStreams() {
		return "tape"
	}
	return "stream"
}

// ─── help overlay ───────────────────────────────────────────────────────────

// renderHelpOverlay delegates to the shared keymap.RenderHelpOverlay
// using the surface→capabilities map in this package's keymap.go.
// Kept under the local name so existing callers (tape model, book
// model) don't all change in the same commit; new code calls
// keymap.RenderHelpOverlay directly.
//
// surface is one of "rolling tape", "book scan", "book ladder".
// Falls back to a Help-only capability for any unknown surface.
func renderHelpOverlay(surface string, width int) string {
	caps := surfaceCapabilities(helpSurfaceTag(surface))
	bold, green, grey, light, reset := output.HelpStyleStrings()
	style := keymapHelpStyle(bold, green, grey, light, reset)
	return keymap.RenderHelpOverlay(surface, caps, style, width)
}

// helpSurfaceTag normalises a human-readable surface name back to
// the legacy tag surfaceCapabilities understands. Two distinct
// inputs feed renderHelpOverlay — the model's mode-specific name
// ("rolling tape" / "book scan" / "book ladder") and the
// FooterHints tags ("tape" / "scan" / "ladder") — so this stays a
// small switch rather than spreading the mapping across files.
func helpSurfaceTag(humanName string) string {
	switch humanName {
	case "rolling tape":
		return "tape"
	case "book scan":
		return "scan"
	case "book ladder":
		return "ladder"
	}
	return ""
}

// keymapHelpStyle is a tiny adapter so wsrender doesn't have to
// import internal/keymap's struct directly at every call site.
func keymapHelpStyle(bold, green, grey, light, reset string) keymap.HelpStyle {
	return keymap.HelpStyle{Bold: bold, Green: green, Grey: grey, LightGrey: light, Reset: reset}
}

// padRight pads s with spaces on the right so it occupies exactly width
// runes of visible space. Used by renderHelpOverlay for the keys column.
func padRight(s string, width int) string {
	w := visibleWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// ─── header ─────────────────────────────────────────────────────────────────

// renderHeader produces the top status line. Includes channel summary,
// total updates, and instantaneous rate (events per second over the
// session window — coarse but useful for "is the connection alive?").
func renderHeader(channels []string, updates int64, elapsed time.Duration, width int) string {
	bold := output.Bold
	green := output.BrandGreen
	grey := output.BrandGreyMid
	reset := output.Reset

	chanLabel := strings.Join(channels, " · ")
	if len(chanLabel) > width-40 {
		chanLabel = truncate(chanLabel, width-40)
	}

	rate := 0.0
	if elapsed.Seconds() > 0 {
		rate = float64(updates) / elapsed.Seconds()
	}

	return fmt.Sprintf("%s%s▲%s  %s%s%s   %supdates: %d · %.1f/s%s",
		bold, green, reset,
		bold, chanLabel, reset,
		grey, updates, rate, reset,
	)
}

// ─── per-channel renderers ──────────────────────────────────────────────────

// renderEvents picks a column layout per channel type and formats every
// row. exchangeCol is decided once from the subscription list (see
// showExchangeColumn) and tells the child renderer whether to insert an
// EXCHANGE column. We don't decide per-frame from event content — that
// caused the column to flicker in/out depending on which venue happened
// to fire most recently.
func renderEvents(events []wsclient.Event, width int, exchangeCol bool) string {
	if len(events) == 0 {
		return ""
	}
	// Use the most recent event's channel to choose formatter — all events
	// in the buffer share a stream type.
	ch := events[len(events)-1].Channel
	switch {
	case strings.HasPrefix(ch, "trades.predictions."):
		return renderPredictionsTrades(events, width)
	case strings.HasPrefix(ch, "trades.spot."):
		return renderSpotTrades(events, width, exchangeCol)
	case strings.HasPrefix(ch, "trades.options."):
		return renderOptionsTrades(events, width, exchangeCol)
	case strings.HasPrefix(ch, "trades."): // perpetuals + futures
		return renderDerivTrades(events, width, exchangeCol)
	case strings.HasPrefix(ch, "ohlc.ticker."):
		return renderTicker(events, width, exchangeCol)
	case strings.HasPrefix(ch, "ohlc.vt."):
		return renderVT(events, width, exchangeCol)
	case strings.HasPrefix(ch, "liquidations."):
		return renderLiquidations(events, width, exchangeCol)
	default:
		return renderGeneric(events, width)
	}
}

func filterTradeTapeEvents(events []wsclient.Event, minUSD float64) []wsclient.Event {
	if minUSD <= 0 {
		return events
	}
	out := make([]wsclient.Event, 0, len(events))
	for _, ev := range events {
		if !strings.HasPrefix(ev.Channel, "trades.") || tapefilter.AllowsNotional(tradeEventNotionalUSD(ev), minUSD) {
			out = append(out, ev)
		}
	}
	return out
}

func tradeEventNotionalUSD(ev wsclient.Event) float64 {
	var d struct {
		Price       float64 `json:"price"`
		Amount      float64 `json:"amount"`
		Size        float64 `json:"size"`
		CoinAmount  float64 `json:"coin_amount"`
		QuoteAmount float64 `json:"quote_amount"`
		AmountUSD   float64 `json:"amount_usd"`
		PremiumUSD  float64 `json:"premium_usd"`
		Notional    float64 `json:"notional"`
	}
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		return 0
	}
	for _, v := range []float64{d.AmountUSD, d.QuoteAmount, d.PremiumUSD, d.Notional} {
		if v > 0 {
			return v
		}
	}
	size := d.CoinAmount
	if size == 0 {
		size = d.Size
	}
	if size == 0 && strings.HasPrefix(ev.Channel, "trades.predictions.") {
		size = d.Amount
	}
	if size > 0 && d.Price > 0 {
		return size * d.Price
	}
	if d.Amount > 0 {
		return d.Amount
	}
	return 0
}

// ─── column aligner ─────────────────────────────────────────────────────────
//
// All per-channel renderers go through this helper. It collects the rows as
// a 2D slice of cells, measures each column's actual width across header +
// data, then prints the result with consistent gutters and alignment.
//
// Numeric columns right-align so prices line up by least-significant digit;
// text columns left-align. Color codes inside a cell are stripped before
// width measurement so they don't throw off alignment.

type colAlign int

const (
	alignLeft colAlign = iota
	alignRight
)

// renderTable formats one frame.
//   - headers: column titles. Length defines column count.
//   - rows:    data rows; each must be the same length as headers.
//   - aligns:  per-column alignment (left/right). If nil, all columns left-align.
//   - width:   terminal width (used to truncate the last column if rows overflow).
func renderTable(headers []string, rows [][]string, aligns []colAlign, width int) string {
	if len(headers) == 0 {
		return ""
	}
	cols := len(headers)
	if aligns == nil {
		aligns = make([]colAlign, cols)
	}

	// Measure widths from header + data, ignoring ANSI escapes.
	widths := make([]int, cols)
	for c := 0; c < cols; c++ {
		widths[c] = visibleWidth(headers[c])
	}
	for _, row := range rows {
		for c := 0; c < cols && c < len(row); c++ {
			if w := visibleWidth(row[c]); w > widths[c] {
				widths[c] = w
			}
		}
	}

	const gutter = 2 // spaces between columns

	var b strings.Builder

	// Header row — bold + light grey.
	b.WriteString(output.Bold + output.BrandGreyLight)
	for c := 0; c < cols; c++ {
		if c > 0 {
			b.WriteString(strings.Repeat(" ", gutter))
		}
		b.WriteString(padCell(headers[c], widths[c], aligns[c]))
	}
	b.WriteString(output.Reset)
	b.WriteByte('\n')

	// Data rows.
	for _, row := range rows {
		for c := 0; c < cols; c++ {
			if c > 0 {
				b.WriteString(strings.Repeat(" ", gutter))
			}
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			b.WriteString(padCell(cell, widths[c], aligns[c]))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// padCell pads s with spaces to fit `width` visible columns. Left- or
// right-justified per align. Color escapes are not counted toward width.
func padCell(s string, width int, align colAlign) string {
	pad := width - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	spaces := strings.Repeat(" ", pad)
	if align == alignRight {
		return spaces + s
	}
	return s + spaces
}

// visibleWidth returns the printable width of s, ignoring ANSI escape
// sequences. We only handle the SGR escapes we actually emit: `\x1b[...m`.
//
// Counts runes (not bytes) so multi-byte UTF-8 characters like the em dash
// `—` (3 bytes, 1 column) align correctly. Without this, cells containing
// the placeholder glyph would over-pad and shove subsequent columns left.
func visibleWidth(s string) int {
	w := 0
	bytes := []byte(s)
	i := 0
	for i < len(bytes) {
		if bytes[i] == 0x1b && i+1 < len(bytes) && bytes[i+1] == '[' {
			// Skip until 'm' (or end of string).
			j := i + 2
			for j < len(bytes) && bytes[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		// Decode one UTF-8 rune. Each rune counts as one display column.
		// This is still approximate (East Asian wide chars are 2 columns,
		// zero-width joiners are 0), but renderers only emit ASCII + a few
		// punctuation symbols like `—` so single-rune-per-column is safe.
		_, size := utf8.DecodeRune(bytes[i:])
		if size == 0 {
			size = 1
		}
		i += size
		w++
	}
	return w
}

// ─── per-channel renderers ──────────────────────────────────────────────────

// exchangeFromChannel pulls the exchange segment out of a wire channel.
// Channel grammar:
//
//	{stream}.{market}.{exchange}.{instrument}[.{tf}]
//	ohlc.{dataType}.{market}.{exchange}.{instrument}.{tf}
//
// Exchange is segment 2 for trades / liquidations / book, segment 3 for
// ohlc.*. Returns empty string if the channel isn't recognisable.
func exchangeFromChannel(ch string) string {
	parts := strings.Split(ch, ".")
	if len(parts) < 4 {
		return ""
	}
	if parts[0] == "ohlc" {
		if len(parts) < 5 {
			return ""
		}
		return parts[3]
	}
	return parts[2]
}

// showExchangeColumn returns true when any subscribed pattern has a
// wildcard in the exchange position — meaning the user has explicitly
// asked for events from many venues, and a stable EXCHANGE column makes
// rows unambiguous.
//
// This is decided once from the subscription list (not from the rolling
// event window), so the column structure is fixed for the session. We
// avoided that earlier and the column appeared / disappeared depending
// on which exchange happened to fire in the last 18 events.
func showExchangeColumn(channels []string) bool {
	for _, ch := range channels {
		ex := exchangeFromChannel(ch)
		if ex == "*" {
			return true
		}
	}
	return false
}

// insertExchangeHeader / insertExchangeAlign / insertExchange place the
// EXCHANGE column right after TIME. The natural read order is when →
// where → what, so EXCHANGE belongs between TIME and INSTRUMENT, not at
// the leftmost edge. Every per-channel renderer follows the same TIME-
// first column convention, so this one helper works for all of them.
func insertExchangeHeader(headers []string) []string {
	out := make([]string, 0, len(headers)+1)
	out = append(out, headers[0]) // TIME
	out = append(out, "EXCHANGE")
	out = append(out, headers[1:]...)
	return out
}

func insertExchangeAlign(aligns []colAlign) []colAlign {
	out := make([]colAlign, 0, len(aligns)+1)
	out = append(out, aligns[0])
	out = append(out, alignLeft)
	out = append(out, aligns[1:]...)
	return out
}

// insertExchange inserts the venue name into row[1], pushing the rest
// right. Falls back to "—" when the channel doesn't carry a recognisable
// exchange segment, so the column never collapses to empty.
func insertExchange(row []string, exchange string) []string {
	if exchange == "" {
		exchange = "—"
	}
	out := make([]string, 0, len(row)+1)
	out = append(out, row[0])
	out = append(out, exchange)
	out = append(out, row[1:]...)
	return out
}

func renderDerivTrades(events []wsclient.Event, width int, exchangeCol bool) string {
	headers := []string{"TIME", "INSTRUMENT", "PRICE", "AMOUNT", "SIDE", "OI", "BASIS"}
	aligns := []colAlign{alignLeft, alignLeft, alignRight, alignRight, alignLeft, alignRight, alignRight}
	if exchangeCol {
		headers = insertExchangeHeader(headers)
		aligns = insertExchangeAlign(aligns)
	}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Timestamp      int64   `json:"timestamp"`
			InstrumentName string  `json:"instrument_name"`
			Price          float64 `json:"price"`
			Amount         float64 `json:"amount"`
			Direction      string  `json:"direction"`
			OpenInterest   float64 `json:"open_interest"`
			Basis          float64 `json:"basis"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		// Color the price by direction so the eye reads side and signed
		// movement together; SIDE column gets a stronger badge.
		priceColored := directionPriceColor(d.Direction) + formatNumFull(d.Price) + output.Reset

		row := []string{
			formatTime(d.Timestamp),
			truncate(d.InstrumentName, 22),
			priceColored,
			formatNumFull(d.Amount),
			colorSide(d.Direction),
			formatNumFull(d.OpenInterest),
			formatNumFull(d.Basis),
		}
		if exchangeCol {
			row = insertExchange(row, exchangeFromChannel(events[i].Channel))
		}
		rows = append(rows, row)
	}
	return renderTable(headers, rows, aligns, width)
}

func renderSpotTrades(events []wsclient.Event, width int, exchangeCol bool) string {
	// CCY dropped — quote currency is implied by the channel name (e.g.
	// `binance:BTCUSDT` → USDT). Keeping it as a column added clutter
	// without information.
	headers := []string{"TIME", "INSTRUMENT", "PRICE", "AMOUNT", "SIDE", "QUOTE"}
	aligns := []colAlign{alignLeft, alignLeft, alignRight, alignRight, alignLeft, alignRight}
	if exchangeCol {
		headers = insertExchangeHeader(headers)
		aligns = insertExchangeAlign(aligns)
	}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Timestamp      int64   `json:"timestamp"`
			InstrumentName string  `json:"instrument_name"`
			Price          float64 `json:"price"`
			Amount         float64 `json:"amount"`
			Direction      string  `json:"direction"`
			QuoteAmount    float64 `json:"quote_amount"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		priceColored := directionPriceColor(d.Direction) + formatNumFull(d.Price) + output.Reset
		row := []string{
			formatTime(d.Timestamp),
			truncate(d.InstrumentName, 22),
			priceColored,
			formatNumFull(d.Amount),
			colorSide(d.Direction),
			formatNumFull(d.QuoteAmount),
		}
		if exchangeCol {
			row = insertExchange(row, exchangeFromChannel(events[i].Channel))
		}
		rows = append(rows, row)
	}
	return renderTable(headers, rows, aligns, width)
}

func renderOptionsTrades(events []wsclient.Event, width int, exchangeCol bool) string {
	headers := []string{"TIME", "INSTRUMENT", "SIDE", "PRICE", "IV", "DELTA", "PREMIUM_USD"}
	aligns := []colAlign{alignLeft, alignLeft, alignLeft, alignRight, alignRight, alignRight, alignRight}
	if exchangeCol {
		headers = insertExchangeHeader(headers)
		aligns = insertExchangeAlign(aligns)
	}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Timestamp      int64   `json:"timestamp"`
			InstrumentName string  `json:"instrument_name"`
			Direction      string  `json:"direction"`
			Price          float64 `json:"price"`
			IV             float64 `json:"iv"`
			Delta          float64 `json:"delta"`
			PremiumUSD     float64 `json:"premium_usd"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		row := []string{
			formatTime(d.Timestamp),
			truncate(d.InstrumentName, 30),
			colorSide(d.Direction),
			formatNumFull(d.Price),
			fmt.Sprintf("%.1f", d.IV),
			fmt.Sprintf("%+.3f", d.Delta),
			formatNumFull(d.PremiumUSD),
		}
		if exchangeCol {
			row = insertExchange(row, exchangeFromChannel(events[i].Channel))
		}
		rows = append(rows, row)
	}
	return renderTable(headers, rows, aligns, width)
}

func renderPredictionsTrades(events []wsclient.Event, width int) string {
	headers := []string{"TIME", "OUTCOME", "SIDE", "PRICE", "SIZE", "EVENT"}
	aligns := []colAlign{alignLeft, alignLeft, alignLeft, alignRight, alignRight, alignLeft}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Timestamp    int64   `json:"timestamp"`
			HumanOutcome string  `json:"human_outcome"`
			Outcome      string  `json:"outcome"`
			Side         string  `json:"side"`
			Price        float64 `json:"price"`
			Size         float64 `json:"size"`
			EventSlug    string  `json:"event_slug"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		outcome := d.HumanOutcome
		if outcome == "" {
			outcome = d.Outcome
		}
		rows = append(rows, []string{
			formatTime(d.Timestamp),
			truncate(outcome, 30),
			colorSide(d.Side),
			formatNumFull(d.Price),
			formatNumFull(d.Size),
			truncate(d.EventSlug, 30),
		})
	}
	return renderTable(headers, rows, aligns, width)
}

func renderTicker(events []wsclient.Event, width int, exchangeCol bool) string {
	headers := []string{"TIME", "INSTRUMENT", "OPEN", "HIGH", "LOW", "CLOSE", "OI"}
	aligns := []colAlign{alignLeft, alignLeft, alignRight, alignRight, alignRight, alignRight, alignRight}
	if exchangeCol {
		headers = insertExchangeHeader(headers)
		aligns = insertExchangeAlign(aligns)
	}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Timestamp      int64   `json:"timestamp"`
			InstrumentName string  `json:"instrument_name"`
			MarkOpen       float64 `json:"mark_price_open"`
			MarkHigh       float64 `json:"mark_price_high"`
			MarkLow        float64 `json:"mark_price_low"`
			MarkClose      float64 `json:"mark_price_close"`
			LastOpen       float64 `json:"last_price_open"`
			LastHigh       float64 `json:"last_price_high"`
			LastLow        float64 `json:"last_price_low"`
			LastClose      float64 `json:"last_price_close"`
			OIClose        float64 `json:"oi_close"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		// Spot uses last_price_*; perps/futures/options use mark_price_*.
		open, high, low, closeV := d.MarkOpen, d.MarkHigh, d.MarkLow, d.MarkClose
		if open == 0 && d.LastOpen != 0 {
			open, high, low, closeV = d.LastOpen, d.LastHigh, d.LastLow, d.LastClose
		}

		closeStr := formatNumFull(closeV)
		if closeV > open {
			closeStr = output.BrandGreen + closeStr + output.Reset
		} else if closeV < open {
			closeStr = output.Red + closeStr + output.Reset
		}

		row := []string{
			formatTime(d.Timestamp),
			truncate(d.InstrumentName, 22),
			formatNumFull(open),
			formatNumFull(high),
			formatNumFull(low),
			closeStr,
			formatNumFull(d.OIClose),
		}
		if exchangeCol {
			row = insertExchange(row, exchangeFromChannel(events[i].Channel))
		}
		rows = append(rows, row)
	}
	return renderTable(headers, rows, aligns, width)
}

func renderVT(events []wsclient.Event, width int, exchangeCol bool) string {
	// LIQ LONG/SHORT shows liquidations by position side, not by trade
	// direction. The API field naming is confusing on purpose:
	//   liquidation_sell_volume = forced sells = LONGS being liquidated
	//   liquidation_buy_volume  = forced buys  = SHORTS being liquidated
	// We swap the field-to-column mapping so the column actually reads as
	// "longs liquidated / shorts liquidated" — what a trader actually
	// wants to know.
	headers := []string{"TIME", "INSTRUMENT", "OPEN", "HIGH", "LOW", "CLOSE", "VWAP", "VOLUME", "BUY/SELL", "LIQ LONG/SHORT"}
	aligns := []colAlign{
		alignLeft, alignLeft,
		alignRight, alignRight, alignRight, alignRight, alignRight,
		alignRight, alignLeft, alignLeft,
	}
	if exchangeCol {
		headers = insertExchangeHeader(headers)
		aligns = insertExchangeAlign(aligns)
	}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		// Field shape per the v1.17.0 wire spec:
		//   buy_volume / sell_volume   — total contracts traded each side
		//   liquidation_buy_volume etc — forced-liquidation portion of each side
		// There is no top-level `volume` field; total volume = buy + sell.
		var d struct {
			Timestamp      int64   `json:"timestamp"`
			InstrumentName string  `json:"instrument_name"`
			Open           float64 `json:"open"`
			High           float64 `json:"high"`
			Low            float64 `json:"low"`
			Close          float64 `json:"close"`
			VWAP           float64 `json:"vwap"`
			BuyVolume      float64 `json:"buy_volume"`
			SellVolume     float64 `json:"sell_volume"`
			LiqBuyVolume   float64 `json:"liquidation_buy_volume"`
			LiqSellVolume  float64 `json:"liquidation_sell_volume"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		totalVolume := d.BuyVolume + d.SellVolume

		closeStr := formatNumFull(d.Close)
		if d.Close > d.Open {
			closeStr = output.BrandGreen + closeStr + output.Reset
		} else if d.Close < d.Open {
			closeStr = output.Red + closeStr + output.Reset
		}

		// Buy/sell breakdown rendered as `buy_qty / sell_qty` so the user
		// can read aggressor pressure at a glance. Preserves whatever
		// decimal precision the API returned for each side.
		buySell := fmt.Sprintf("%s / %s",
			formatNumFull(d.BuyVolume),
			formatNumFull(d.SellVolume),
		)
		// LIQ LONG/SHORT — the column header reads as the position that
		// got wiped, not the resulting trade direction:
		//   LONG  ← liquidation_sell_volume  (longs forced to sell)
		//   SHORT ← liquidation_buy_volume   (shorts forced to buy)
		// This is the inverse of how the API field names suggest, but it's
		// what every trader-facing terminal (Coinglass, Bybit dashboard,
		// etc.) reports under "long/short liquidations."
		liqLongShort := fmt.Sprintf("%s / %s",
			formatNumFull(d.LiqSellVolume),
			formatNumFull(d.LiqBuyVolume),
		)

		row := []string{
			formatTime(d.Timestamp),
			truncate(d.InstrumentName, 22),
			formatNumFull(d.Open),
			formatNumFull(d.High),
			formatNumFull(d.Low),
			closeStr,
			formatNumFull(d.VWAP),
			formatNumFull(totalVolume),
			buySell,
			liqLongShort,
		}
		if exchangeCol {
			row = insertExchange(row, exchangeFromChannel(events[i].Channel))
		}
		rows = append(rows, row)
	}
	return renderTable(headers, rows, aligns, width)
}

// renderLiquidations formats forced-close events from the v1.21.0 channel.
// Layout reads as a "who got rekt" tape: position_side is the human-relevant
// field (LONG/SHORT — what was liquidated), direction is its inverse (the
// forced order side) and isn't shown to keep the table tight.
//
// Color semantics: long liquidation = price dropped enough to forced-close
// longs → red. Short liquidation = price ripped up → green. This matches
// how a trader reads the tape, not how an exchange logs the order.
func renderLiquidations(events []wsclient.Event, width int, exchangeCol bool) string {
	headers := []string{"TIME", "INSTRUMENT", "SIDE", "PRICE", "AMOUNT", "USD", "MARK"}
	aligns := []colAlign{alignLeft, alignLeft, alignLeft, alignRight, alignRight, alignRight, alignRight}
	if exchangeCol {
		headers = insertExchangeHeader(headers)
		aligns = insertExchangeAlign(aligns)
	}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		var d struct {
			Timestamp      int64   `json:"timestamp"`
			InstrumentName string  `json:"instrument_name"`
			PositionSide   string  `json:"position_side"`
			Price          float64 `json:"price"`
			Amount         float64 `json:"amount"`
			AmountUSD      float64 `json:"amount_usd"`
			MarkPrice      float64 `json:"mark_price"`
		}
		_ = json.Unmarshal(events[i].Data, &d)

		row := []string{
			formatTime(d.Timestamp),
			truncate(d.InstrumentName, 22),
			colorPositionSide(d.PositionSide),
			formatNumFull(d.Price),
			formatNumFull(d.Amount),
			formatNumFull(d.AmountUSD),
			formatNumFull(d.MarkPrice),
		}
		if exchangeCol {
			row = insertExchange(row, exchangeFromChannel(events[i].Channel))
		}
		rows = append(rows, row)
	}
	return renderTable(headers, rows, aligns, width)
}

func renderGeneric(events []wsclient.Event, width int) string {
	headers := []string{"CHANNEL", "DATA"}
	aligns := []colAlign{alignLeft, alignLeft}

	rows := make([][]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		rows = append(rows, []string{
			truncate(ev.Channel, 50),
			truncate(string(ev.Data), width-55),
		})
	}
	return renderTable(headers, rows, aligns, width)
}

// ─── helpers ────────────────────────────────────────────────────────────────

func headerStyle(s string) string {
	return output.Bold + output.BrandGreyLight + s + output.Reset
}

func colorSide(side string) string {
	switch strings.ToLower(side) {
	case "buy":
		return output.BrandGreen + " buy" + output.Reset
	case "sell":
		return output.Red + "sell" + output.Reset
	default:
		return output.BrandGreyMid + truncate(side, 4) + output.Reset
	}
}

// colorPositionSide tags a liquidation row by which side of the book got
// forced out. Longs liquidated on a drop → red; shorts liquidated on a
// rally → green. The visual maps to "what just moved the price," not the
// inverse forced-order direction.
func colorPositionSide(side string) string {
	switch strings.ToLower(side) {
	case "long":
		return output.Red + "LONG " + output.Reset
	case "short":
		return output.BrandGreen + "SHORT" + output.Reset
	default:
		return output.BrandGreyMid + truncate(side, 5) + output.Reset
	}
}

func directionPriceColor(direction string) string {
	switch strings.ToLower(direction) {
	case "buy":
		return output.BrandGreen
	case "sell":
		return output.Red
	default:
		return ""
	}
}

// formatTime renders a unix-ms timestamp as HH:MM:SS in local time.
func formatTime(ts int64) string {
	if ts == 0 {
		return "        "
	}
	t := time.UnixMilli(ts).Local()
	return t.Format("15:04:05")
}

// formatNum is a shim to output.FormatNum. Kept under the local name
// so existing call sites don't need editing in the same commit; new
// renderers should call output.FormatNum directly.
func formatNum(v float64, decimals int) string {
	if v == 0 {
		return "—"
	}
	return output.FormatNum(v, decimals)
}

// formatBigNum renders large numbers with K/M/B suffixes.
//
// Reserved for header-line summaries (e.g. updates rate). Per user feedback,
// trade rows must show the exact dollar amount — both human traders and
// automated consumers want the raw value, not an order-of-magnitude
// summary. Use formatNumFull for any data cell.
func formatBigNum(v float64) string {
	if v == 0 {
		return "—"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", v/1_000_000_000)
	case abs >= 1_000_000:
		return fmt.Sprintf("%.2fM", v/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("%.1fK", v/1_000)
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

// formatNumFull is a shim to output.FormatNumFull. Same intent as
// formatNum's shim: keep call sites in this file unchanged while
// the canonical implementation lives in internal/output for the
// dashboard panels to share.
func formatNumFull(v float64) string { return output.FormatNumFull(v) }

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return s[:max-1] + "…"
}
