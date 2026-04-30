// Book channel renderer — separate from the rolling-tape live table because
// books are replacing snapshots, not append-only events. Two views:
//
//   - scan:   one summary row per subscribed (exchange, instrument). The
//             trader's "watchlist" view. Default for multi-pair.
//   - ladder: centre-price depth ladder for a single pair, Bloomberg DEPT
//             style. Default for single-pair, drilled-into from scan via
//             Enter.
//
// Navigation is k9s-style: list → Enter → detail → Esc. The active view is
// the only thing that consumes input; everything else routes through the
// shared snapshot store.
package wsrender

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

// BookSnapshot is the parsed wire payload. We only decode the fields we
// render — depth ladders and pre-computed liquidity / imbalance stats. Raw
// asks/bids are []bookLevel rather than [][]float64 so the predictions
// object-shape can be tolerated alongside the tuple-shape; see
// bookLevel.UnmarshalJSON.
type BookSnapshot struct {
	Channel        string      `json:"-"`
	ReceivedAt     time.Time   `json:"-"`
	Timestamp      int64       `json:"timestamp"`
	Exchange       string      `json:"exchange"`
	InstrumentName string      `json:"instrument_name"`
	Currency       string      `json:"currency"`
	InstrumentType string      `json:"instrument_type"`
	QuoteCurrency  string      `json:"quote_currency,omitempty"`
	Depth          int         `json:"depth"`
	Asks           []bookLevel `json:"asks"`
	Bids           []bookLevel `json:"bids"`
	AskLiq10       float64     `json:"ask_liquidity_10"`
	AskLiq20       float64     `json:"ask_liquidity_20"`
	AskLiq50       float64     `json:"ask_liquidity_50"`
	AskLiq100      float64     `json:"ask_liquidity_100"`
	BidLiq10       float64     `json:"bid_liquidity_10"`
	BidLiq20       float64     `json:"bid_liquidity_20"`
	BidLiq50       float64     `json:"bid_liquidity_50"`
	BidLiq100      float64     `json:"bid_liquidity_100"`
	Imbalance10    float64     `json:"imbalance_10"`
	Imbalance20    float64     `json:"imbalance_20"`
	Imbalance50    float64     `json:"imbalance_50"`
	Imbalance100   float64     `json:"imbalance_100"`
	Microprice     float64     `json:"microprice"`
}

// bookLevel is one [price, size] pair. The wire format is normally a JSON
// tuple, but predictions currently emit objects {price, size} (producer
// fix in flight per the API team). Both shapes decode here so a single
// renderer covers every market.
type bookLevel struct {
	Price float64
	Size  float64
}

func (b *bookLevel) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' {
		var tuple [2]float64
		if err := json.Unmarshal(data, &tuple); err != nil {
			return err
		}
		b.Price, b.Size = tuple[0], tuple[1]
		return nil
	}
	// Object form: {"price": ..., "size": ...} (predictions, pre-fix).
	var obj struct {
		Price float64 `json:"price"`
		Size  float64 `json:"size"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	b.Price, b.Size = obj.Price, obj.Size
	return nil
}

// pairKey is the natural-language identifier from the channel string —
// "exchange:instrument", what the user typed. Used to key the snapshot
// store and to populate the scan view's first column.
type pairKey struct {
	Exchange   string
	Instrument string
}

func (k pairKey) String() string { return k.Exchange + ":" + k.Instrument }

// channelKey extracts the pairKey from a "book.{market}.{exchange}.{instrument}"
// channel string. Returns an empty key on malformed input — the caller treats
// that as a soft skip.
func channelKey(channel string) pairKey {
	parts := strings.SplitN(channel, ".", 4)
	if len(parts) != 4 || parts[0] != "book" {
		return pairKey{}
	}
	return pairKey{Exchange: parts[2], Instrument: parts[3]}
}

// ─── BookTable: shared state owned by the cmd layer ─────────────────────────

// BookTable holds the latest snapshot per (exchange, instrument). The cmd
// layer pushes events; the Bubble Tea program reads via snapshot().
//
// Snapshot semantics: every wire event is a complete book replacing the
// previous one — so we just overwrite under lock. No delta reconstruction.
type BookTable struct {
	channels []string
	layout   string // "scan" | "ladder"

	mu      sync.RWMutex
	books   map[pairKey]*BookSnapshot

	// changes[(pair,price)] -> latest size move recorded for that level
	// against the previous snapshot. Stored with a timestamp so the
	// renderer can decide whether the flash window is still active and
	// the direction so we render `↑` (built up) vs `↓` (eaten). We use
	// the price as a string key (formatted to the venue tick) so float
	// equality isn't a problem — a price level that drops out then comes
	// back at the same tick is correctly recognised as "stale flash."
	changes map[flashKey]levelChange

	// micro is a per-pair ring buffer of recent microprices. Used by the
	// header sparkline. 60 ticks is enough for ~1-2 minutes of context on
	// a hot perp; 1-3 minutes on a slow one.
	micro map[pairKey]*microRing

	updates int64
	startAt time.Time
	lastErr string
}

// flashKey is (pair, price-string). String price avoids float-equality
// fragility — we compare prices the same way the renderer formats them.
type flashKey struct {
	pair  pairKey
	price string
}

// levelChange captures one size move at a price level — direction and the
// time it happened. The renderer uses direction to pick the glyph (↑/↓)
// and ts to decide whether the change is still within the flash window.
//
// dir is +1 when the level grew (more liquidity arrived), -1 when it
// shrank or disappeared (eaten / pulled), 0 when no recent change.
type levelChange struct {
	dir int
	ts  time.Time
}

// microRing is a 60-element ring buffer of microprices. Oldest at head,
// newest at head-1. NaN-filled when not yet populated so the sparkline
// gracefully renders with leading blanks.
type microRing struct {
	data [60]float64
	head int
	full bool
}

func (r *microRing) push(v float64) {
	r.data[r.head] = v
	r.head = (r.head + 1) % len(r.data)
	if r.head == 0 {
		r.full = true
	}
}

// values returns the ring contents in oldest-to-newest order. Length is
// 60 if full, head otherwise.
func (r *microRing) values() []float64 {
	if !r.full {
		out := make([]float64, r.head)
		copy(out, r.data[:r.head])
		return out
	}
	out := make([]float64, len(r.data))
	copy(out, r.data[r.head:])
	copy(out[len(r.data)-r.head:], r.data[:r.head])
	return out
}

// NewBookTable creates a renderer for the given subscription set. layout
// chooses the default view ("scan" for multi-pair, "ladder" for single).
func NewBookTable(channels []string, layout string) *BookTable {
	return &BookTable{
		channels: channels,
		layout:   layout,
		books:    make(map[pairKey]*BookSnapshot, len(channels)),
		changes:  make(map[flashKey]levelChange),
		micro:    make(map[pairKey]*microRing, len(channels)),
		startAt:  time.Now(),
	}
}

// Push parses one wsclient.Event into a BookSnapshot, diffs it against the
// previous snapshot for that pair to record level-change times (used by
// the flash highlight), and updates the microprice ring buffer (used by
// the header sparkline).
//
// Safe to call from any goroutine. Soft-fails on malformed events — they
// go to lastErr instead of crashing the renderer.
func (bt *BookTable) Push(ev wsclient.Event) {
	key := channelKey(ev.Channel)
	if key.Exchange == "" {
		return
	}
	var snap BookSnapshot
	if err := json.Unmarshal(ev.Data, &snap); err != nil {
		bt.SetLastError(fmt.Sprintf("decode %s: %v", ev.Channel, err))
		return
	}
	snap.Channel = ev.Channel
	snap.ReceivedAt = time.Now()

	bt.mu.Lock()
	defer bt.mu.Unlock()

	// Diff against previous snapshot for this pair to flag changed levels.
	// We compare only the top 50 levels per side — past that the renderer
	// won't show the cells anyway. A level is "changed" if it's new at this
	// price OR its size moved by more than 0.1%.
	prev := bt.books[key]
	if prev != nil {
		now := snap.ReceivedAt
		bt.diffLevels(key, prev.Bids, snap.Bids, now)
		bt.diffLevels(key, prev.Asks, snap.Asks, now)
	}
	bt.books[key] = &snap
	bt.updates++

	// Update microprice ring.
	if snap.Microprice > 0 {
		ring := bt.micro[key]
		if ring == nil {
			ring = &microRing{}
			bt.micro[key] = ring
		}
		ring.push(snap.Microprice)
	}
}

// diffLevels records meaningful size moves between prev and curr,
// recovering direction so the renderer can paint `↑` (liquidity built)
// vs `↓` (liquidity eaten or pulled). Caller holds bt.mu.
//
// Threshold is 10% relative change — small enough to catch real
// rebalances, large enough that market-maker jitter on a hot book
// doesn't paint everything at once. Brand-new levels (price didn't exist
// in prev) count as +1, vanished levels (price exists in prev but not in
// the top-50 of curr) count as -1.
func (bt *BookTable) diffLevels(key pairKey, prev, curr []bookLevel, now time.Time) {
	const sizeEps = 0.10
	prevByPrice := make(map[string]float64, len(prev))
	for _, l := range prev {
		prevByPrice[formatBookPrice(l.Price)] = l.Size
	}
	currByPrice := make(map[string]bool, len(curr))
	for i, l := range curr {
		if i >= 50 {
			break
		}
		ps := formatBookPrice(l.Price)
		currByPrice[ps] = true
		old, existed := prevByPrice[ps]
		switch {
		case !existed:
			// New level appeared.
			bt.changes[flashKey{pair: key, price: ps}] = levelChange{dir: +1, ts: now}
		case old == 0:
			if l.Size > 0 {
				bt.changes[flashKey{pair: key, price: ps}] = levelChange{dir: +1, ts: now}
			}
		default:
			rel := (l.Size - old) / old
			abs := rel
			if abs < 0 {
				abs = -abs
			}
			if abs >= sizeEps {
				dir := +1
				if rel < 0 {
					dir = -1
				}
				bt.changes[flashKey{pair: key, price: ps}] = levelChange{dir: dir, ts: now}
			}
		}
	}
	// Levels that disappeared from the top-50 (got eaten or pulled out
	// of view) get a downward flash. We only mark prices that were in
	// prev but missing from curr.
	for ps := range prevByPrice {
		if !currByPrice[ps] {
			bt.changes[flashKey{pair: key, price: ps}] = levelChange{dir: -1, ts: now}
		}
	}
}

// SetLastError records a soft error from wsclient or our own decoding path.
// Surfaced dimmed in the footer.
func (bt *BookTable) SetLastError(msg string) {
	bt.mu.Lock()
	bt.lastErr = msg
	bt.mu.Unlock()
}

// snapshot returns a stable copy of the books map plus update count and
// elapsed time. The map is shallow-copied; *BookSnapshot pointers are
// shared, but they're only ever replaced (never mutated in place) so
// readers see a consistent picture.
func (bt *BookTable) snapshot() (books map[pairKey]*BookSnapshot, updates int64, elapsed time.Duration, lastErr string) {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	books = make(map[pairKey]*BookSnapshot, len(bt.books))
	for k, v := range bt.books {
		books[k] = v
	}
	return books, bt.updates, time.Since(bt.startAt), bt.lastErr
}

// flashesForPair returns the levels that changed within `window` of now,
// keyed by price string and carrying the direction sign. Used by the
// renderer to paint a `↑` (built) or `↓` (eaten) glyph and a brief flash.
func (bt *BookTable) flashesForPair(key pairKey, window time.Duration) map[string]int {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	cutoff := time.Now().Add(-window)
	out := make(map[string]int)
	for k, c := range bt.changes {
		if k.pair != key {
			continue
		}
		if c.ts.After(cutoff) {
			out[k.price] = c.dir
		}
	}
	return out
}

// microValuesForPair returns the microprice ring contents for a pair in
// oldest-to-newest order. Empty slice if no data yet.
func (bt *BookTable) microValuesForPair(key pairKey) []float64 {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	r := bt.micro[key]
	if r == nil {
		return nil
	}
	return r.values()
}

// orderedKeys returns every pair we have a snapshot for, sorted
// alphabetically by exchange then instrument. We discover pairs from
// arriving events rather than from the subscription list so wildcards
// work — a sub like `book.perpetuals.*.BTCUSDT` resolves into per-venue
// concrete events at runtime, and only those that have actually fired
// get a row.
//
// Alphabetical sort is the cheapest stable order; arrival-order would
// shuffle rows mid-session as new venues come online, which is jarring
// in the scan view.
func (bt *BookTable) orderedKeys() []pairKey {
	bt.mu.RLock()
	defer bt.mu.RUnlock()
	keys := make([]pairKey, 0, len(bt.books))
	for k := range bt.books {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Exchange != keys[j].Exchange {
			return keys[i].Exchange < keys[j].Exchange
		}
		return keys[i].Instrument < keys[j].Instrument
	})
	return keys
}

// hasWildcardSubscription reports whether any of the user's subscribed
// patterns contains a `*` segment. The cmd layer uses this to nudge
// auto-layout toward scan even on a "single pattern" subscription —
// wildcards expand to many concrete pairs.
func (bt *BookTable) hasWildcardSubscription() bool {
	for _, ch := range bt.channels {
		if strings.Contains(ch, "*") {
			return true
		}
	}
	return false
}

// Run starts the Bubble Tea program. Blocks until the user quits.
func (bt *BookTable) Run() error {
	prog := tea.NewProgram(
		newBookModel(bt),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := prog.Run()
	return err
}

// ─── Bubble Tea model ───────────────────────────────────────────────────────

type viewMode int

const (
	viewScan viewMode = iota
	viewLadder
)

type bookModel struct {
	bt *BookTable

	mode      viewMode
	width     int
	height    int

	// Cursor is which scan row is selected (and which pair the ladder view
	// shows when mode == viewLadder). Always valid when bt has at least one
	// pair; clamped on render.
	cursor int

	// scrollTop is the index of the first scan row currently visible. We
	// keep cursor and scroll independent so a viewport that's smaller than
	// the discovered set (e.g. wildcard `binance:*` with 600 spot pairs)
	// can scroll without reordering rows. Clamped on render so the cursor
	// is always within the visible window.
	scrollTop int

	// Depth tier the ladder shows: 10, 20, 50. Cycled with +/-.
	depthTier int

	// Paused freezes the latest snapshot in view (events keep flowing into
	// bt.books but the model snapshots once and re-uses it until 'p').
	paused      bool
	pausedSnap  map[pairKey]*BookSnapshot

	// helpOpen toggles the keybinding overlay in the body.
	helpOpen bool
}

func newBookModel(bt *BookTable) bookModel {
	mode := viewScan
	if bt.layout == "ladder" {
		mode = viewLadder
	}
	return bookModel{
		bt:        bt,
		mode:      mode,
		width:     100,
		height:    30,
		depthTier: 10,
	}
}

func (m bookModel) Init() tea.Cmd {
	return tickEvery(100 * time.Millisecond)
}

func (m bookModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Route through classifyKey (keymap.go) — the same vocabulary
		// every TUI surface uses. Per-action handling diverges by
		// surface (e.g. q in a drilled-into ladder goes back to scan;
		// q anywhere else quits) but the *keys* themselves are
		// defined once.
		switch classifyKey(msg.String()) {
		case actQuit:
			// In ladder-after-drilldown, q goes back to scan; in
			// scan or ladder-launched-as-default, q quits.
			if m.mode == viewLadder && m.bt.layout != "ladder" {
				m.mode = viewScan
				return m, nil
			}
			return m, tea.Quit
		case actEsc:
			// Esc precedence: close help overlay first; then back
			// out of ladder-after-drilldown to scan; otherwise quit.
			if m.helpOpen {
				m.helpOpen = false
				return m, nil
			}
			if m.mode == viewLadder && m.bt.layout != "ladder" {
				m.mode = viewScan
				return m, nil
			}
			return m, tea.Quit
		case actHelp:
			m.helpOpen = !m.helpOpen
			return m, nil
		case actEnter:
			// Drill into ladder only when at least one pair has
			// delivered a snapshot. Pressing Enter on an empty scan
			// view (still waiting on the first event) would
			// otherwise switch to a ladder showing "waiting for
			// snapshot…", which is confusing.
			if m.mode == viewScan && len(m.bt.orderedKeys()) > 0 {
				m.mode = viewLadder
			}
			return m, nil
		case actUp:
			if m.mode == viewScan && m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case actDown:
			if m.mode == viewScan {
				keys := m.bt.orderedKeys()
				if m.cursor < len(keys)-1 {
					m.cursor++
				}
			}
			return m, nil
		case actPageUp:
			if m.mode == viewScan {
				page := m.scanPageSize()
				m.cursor -= page
				if m.cursor < 0 {
					m.cursor = 0
				}
				m.scrollTop -= page
				if m.scrollTop < 0 {
					m.scrollTop = 0
				}
			}
			return m, nil
		case actPageDown:
			if m.mode == viewScan {
				keys := m.bt.orderedKeys()
				page := m.scanPageSize()
				m.cursor += page
				if m.cursor > len(keys)-1 {
					m.cursor = len(keys) - 1
				}
				if m.cursor < 0 {
					m.cursor = 0
				}
				m.scrollTop += page
				if m.scrollTop > len(keys)-page {
					m.scrollTop = len(keys) - page
				}
				if m.scrollTop < 0 {
					m.scrollTop = 0
				}
			}
			return m, nil
		case actTop:
			if m.mode == viewScan {
				m.cursor = 0
				m.scrollTop = 0
			}
			return m, nil
		case actBottom:
			if m.mode == viewScan {
				keys := m.bt.orderedKeys()
				m.cursor = len(keys) - 1
				if m.cursor < 0 {
					m.cursor = 0
				}
				page := m.scanPageSize()
				m.scrollTop = len(keys) - page
				if m.scrollTop < 0 {
					m.scrollTop = 0
				}
			}
			return m, nil
		case actDepthUp:
			if m.mode == viewLadder {
				m.depthTier = nextDepthTier(m.depthTier)
			}
			return m, nil
		case actDepthDown:
			if m.mode == viewLadder {
				m.depthTier = prevDepthTier(m.depthTier)
			}
			return m, nil
		case actPause:
			m.paused = !m.paused
			if m.paused {
				snap, _, _, _ := m.bt.snapshot()
				m.pausedSnap = snap
			} else {
				m.pausedSnap = nil
			}
			return m, nil
		}
	case tea.MouseMsg:
		// Wheel-only mouse handling via classifyMouse — same shared
		// vocabulary as keys. Click events are deliberately not
		// consumed so the terminal keeps native click-drag-to-select
		// for copy-paste (Shift on most terminals, Alt in VS Code).
		switch classifyMouse(msg.Button) {
		case actWheelUp:
			if m.mode == viewScan && m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case actWheelDown:
			if m.mode == viewScan {
				keys := m.bt.orderedKeys()
				if m.cursor < len(keys)-1 {
					m.cursor++
				}
			}
			return m, nil
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tickMsg:
		return m, tickEvery(100 * time.Millisecond)
	}
	return m, nil
}

func (m bookModel) View() string {
	books, updates, elapsed, lastErr := m.bt.snapshot()
	if m.paused && m.pausedSnap != nil {
		books = m.pausedSnap
	}

	if m.helpOpen {
		surface := "book scan"
		if m.mode == viewLadder {
			surface = "book ladder"
		}
		return renderHelpOverlay(surface, m.width)
	}

	var body string
	switch m.mode {
	case viewScan:
		body = m.renderScan(books, updates, elapsed)
	case viewLadder:
		body = m.renderLadder(books, updates, elapsed)
	}

	footer := m.renderFooter(lastErr)
	return body + "\n" + footer
}

// nextDepthTier / prevDepthTier cycle through {10, 20, 50}. We deliberately
// stop at 50 even though the wire payload carries _100 — past 50 the ladder
// stops being readable on a normal terminal.
func nextDepthTier(d int) int {
	switch d {
	case 10:
		return 20
	case 20:
		return 50
	default:
		return 10
	}
}

func prevDepthTier(d int) int {
	switch d {
	case 50:
		return 20
	case 20:
		return 10
	default:
		return 50
	}
}

// ─── scan view ──────────────────────────────────────────────────────────────

// scanPageSize returns the number of data rows that fit in the scan
// viewport given the current terminal height. Used by PgUp/PgDn and to
// clamp scroll. Chrome budget: header (1) + footer hint (1) + footer
// padding (1) + column-titles row (1) = 4 lines reserved.
func (m bookModel) scanPageSize() int {
	const chrome = 4
	h := m.height - chrome
	if h < 1 {
		h = 1
	}
	return h
}

// renderScan draws the multi-pair watchlist with viewport scrolling so a
// wildcard like `binance:*` (~600 spot pairs) doesn't push the chrome off
// screen. Scroll position is clamped on each render so the cursor stays
// visible — receiver is by value (Bubble Tea View() pattern), so we
// compute scrollTop locally and don't need to mutate the model.
//
// Pairs that haven't fired their first snapshot yet show "—" placeholders.
func (m bookModel) renderScan(books map[pairKey]*BookSnapshot, updates int64, elapsed time.Duration) string {
	header := m.renderHeader("scan", updates, elapsed)

	headers := []string{"", "PAIR", "BID SZ", "BID", "SPREAD", "BPS", "ASK", "ASK SZ", "MICRO", "IMB10", "UPD"}
	aligns := []colAlign{alignLeft, alignLeft, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignRight, alignLeft}

	keys := m.bt.orderedKeys()
	if len(keys) == 0 {
		patterns := strings.Join(m.bt.channels, ", ")
		return header + "\n  " + output.BrandGreyMid + "waiting for first snapshot on " + patterns + "…" + output.Reset
	}

	// Clamp cursor to a valid index — discovered set may have shrunk
	// since the last keystroke (rare but possible on reconnect).
	cursor := m.cursor
	if cursor >= len(keys) {
		cursor = len(keys) - 1
	}
	if cursor < 0 {
		cursor = 0
	}

	// Compute the visible window from m.scrollTop, then drag it to keep
	// the cursor inside [scrollTop, scrollTop+page). The mutation we'd
	// do here is local-only (we're a value receiver); the next Update
	// keystroke will rebase scrollTop relative to the cursor it sees.
	page := m.scanPageSize()
	scrollTop := m.scrollTop
	if cursor < scrollTop {
		scrollTop = cursor
	}
	if cursor >= scrollTop+page {
		scrollTop = cursor - page + 1
	}
	if scrollTop+page > len(keys) {
		scrollTop = len(keys) - page
	}
	if scrollTop < 0 {
		scrollTop = 0
	}
	end := scrollTop + page
	if end > len(keys) {
		end = len(keys)
	}
	visible := keys[scrollTop:end]

	rows := make([][]string, 0, len(visible))
	for i, k := range visible {
		absoluteIdx := scrollTop + i
		marker := "  "
		if absoluteIdx == cursor {
			marker = output.BrandGreen + "▸ " + output.Reset
		}
		snap := books[k]
		if snap == nil {
			rows = append(rows, []string{
				marker, k.String(), "—", "—", "—", "—", "—", "—", "—", "—", "waiting…",
			})
			continue
		}
		bid, ask := bestLevels(snap)
		spread := 0.0
		if ask.Price > 0 && bid.Price > 0 {
			spread = ask.Price - bid.Price
		}
		bps := 0.0
		if snap.Microprice > 0 && spread > 0 {
			bps = (spread / snap.Microprice) * 10_000
		}
		rows = append(rows, []string{
			marker,
			k.String(),
			formatBookSize(bid.Size),
			output.BrandGreen + formatBookPrice(bid.Price) + output.Reset,
			formatBookPrice(spread),
			formatNum(bps, 2),
			output.Red + formatBookPrice(ask.Price) + output.Reset,
			formatBookSize(ask.Size),
			formatBookPrice(snap.Microprice),
			colorImbalance(snap.Imbalance10),
			formatTime(snap.Timestamp),
		})
	}

	table := renderTable(headers, rows, aligns, m.width)

	// Position indicator next to the header — only when the list is
	// taller than the viewport, so the chrome stays clean for short
	// scans. Format: " 12–24 / 600" in brand-grey.
	pageInfo := ""
	if len(keys) > page {
		pageInfo = fmt.Sprintf("   %s%d–%d / %d%s",
			output.BrandGreyMid,
			scrollTop+1, end, len(keys),
			output.Reset,
		)
	}

	return header + pageInfo + "\n" + table
}

// ─── ladder view ────────────────────────────────────────────────────────────

// renderLadder draws the centre-price ladder for the selected pair. The
// price column is anchored centre, bids ascend on the LEFT, asks descend on
// the RIGHT — canonical Bloomberg DEPT / Bookmap / Webull layout. (We had
// it flipped in the first cut; traders read the side they're buying on the
// left, the side they're selling on the right.)
//
// We render top-N levels per side (N = depthTier). Sizes get a logarithmic
// bar chart so a single dominant level doesn't visually flatten every
// smaller one — common on books where a market-maker parks a 5+ BTC chunk
// next to dozens of 0.001 BTC quotes.
func (m bookModel) renderLadder(books map[pairKey]*BookSnapshot, updates int64, elapsed time.Duration) string {
	header := m.renderHeader("ladder", updates, elapsed)

	keys := m.bt.orderedKeys()
	if len(keys) == 0 {
		// No pair has delivered a snapshot yet. For a wildcard
		// subscription this is normal early-state — show what the user
		// typed so they know what they're waiting on.
		patterns := strings.Join(m.bt.channels, ", ")
		return header + "\n  " + output.BrandGreyMid + "waiting for first snapshot on " + patterns + "…" + output.Reset
	}
	if m.cursor >= len(keys) {
		m.cursor = len(keys) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	key := keys[m.cursor]
	snap := books[key]
	if snap == nil {
		return header + "\n  " + output.BrandGreyMid + "waiting for snapshot on " + key.String() + "…" + output.Reset
	}

	// Header strip: pair, mid, spread, imbalance, depth tier.
	bid, ask := bestLevels(snap)
	spread := 0.0
	bps := 0.0
	if ask.Price > 0 && bid.Price > 0 {
		spread = ask.Price - bid.Price
		if snap.Microprice > 0 {
			bps = (spread / snap.Microprice) * 10_000
		}
	}
	bidLiq, askLiq, imb := liquidityForTier(snap, m.depthTier)

	spark := sparklineMicro(m.bt.microValuesForPair(key))

	strip := fmt.Sprintf(
		"%s%s%s   MID %s %s   SPREAD %s (%s bps)   IMB%d %s   BIDLIQ%d %s   ASKLIQ%d %s",
		output.Bold, key.String(), output.Reset,
		formatBookPrice(snap.Microprice), spark,
		formatBookPrice(spread),
		formatNum(bps, 2),
		m.depthTier, colorImbalance(imb),
		m.depthTier, formatBookSize(bidLiq),
		m.depthTier, formatBookSize(askLiq),
	)

	// Layout: cum_bid | bid_size | bid_bar | PRICE | ask_bar | ask_size | cum_ask
	// Asks descend from top of frame (worst price at top, best price just
	// above the spread separator); bids descend from spread separator
	// (best price at top of bid block, worst at bottom). That puts the
	// best bid and best ask physically adjacent to the spread row.
	asks := snap.Asks
	bids := snap.Bids
	tier := m.depthTier
	if len(asks) > tier {
		asks = asks[:tier]
	}
	if len(bids) > tier {
		bids = bids[:tier]
	}

	maxSize := 0.0
	for _, l := range asks {
		if l.Size > maxSize {
			maxSize = l.Size
		}
	}
	for _, l := range bids {
		if l.Size > maxSize {
			maxSize = l.Size
		}
	}

	const barWidth = 18

	// Per-side cumulative totals — used both for the cum_ask / cum_bid
	// columns and for the whale-badge denominator. A whale level is one
	// that holds >=30% of its side's tier-cumulative liquidity. That's a
	// conservative threshold: on a normal book the largest level is 5-10%,
	// so 30% is genuinely "this is the wall."
	const whaleThreshold = 0.30
	bidCums := make([]float64, len(bids))
	bidTotal := 0.0
	for i, l := range bids {
		bidTotal += l.Size
		bidCums[i] = bidTotal
	}
	askCums := make([]float64, len(asks))
	askTotal := 0.0
	for i, l := range asks {
		askTotal += l.Size
		askCums[i] = askTotal
	}

	flashes := m.bt.flashesForPair(key, 250*time.Millisecond)

	headers := []string{"CUM BID", "BID SZ", "", "PRICE", "", "ASK SZ", "CUM ASK"}
	aligns := []colAlign{alignRight, alignRight, alignRight, alignRight, alignLeft, alignLeft, alignLeft}

	rows := make([][]string, 0, len(asks)+len(bids)+1)

	// Asks block — print worst-price at top down to best-price at bottom.
	// The bid columns (left half) stay empty in this block — keeps the
	// price column anchored centre and lets the eye flow downward to the
	// spread row.
	for i := len(asks) - 1; i >= 0; i-- {
		l := asks[i]
		ps := formatBookPrice(l.Price)
		whale := askTotal > 0 && l.Size/askTotal >= whaleThreshold
		rows = append(rows, []string{
			"",
			"",
			"",
			styleLevelPrice(ps, output.Red, flashes[ps]),
			barLeft(l.Size, maxSize, barWidth, output.Red),
			styleLevelSize(formatBookSize(l.Size), whale),
			formatBookSize(askCums[i]),
		})
	}

	// Spread row.
	rows = append(rows, []string{
		"", "", "",
		output.BrandGreyMid + "── spread " + formatBookPrice(spread) + " ──" + output.Reset,
		"", "", "",
	})

	// Bids block — best bid at top, worst at bottom.
	for i, l := range bids {
		ps := formatBookPrice(l.Price)
		whale := bidTotal > 0 && l.Size/bidTotal >= whaleThreshold
		rows = append(rows, []string{
			formatBookSize(bidCums[i]),
			styleLevelSize(formatBookSize(l.Size), whale),
			barRight(l.Size, maxSize, barWidth, output.BrandGreen),
			styleLevelPrice(ps, output.BrandGreen, flashes[ps]),
			"",
			"",
			"",
		})
	}

	table := renderTable(headers, rows, aligns, m.width)
	return header + "\n" + strip + "\n\n" + table
}

// ─── header / footer ────────────────────────────────────────────────────────

func (m bookModel) renderHeader(viewName string, updates int64, elapsed time.Duration) string {
	rate := 0.0
	if elapsed.Seconds() > 0 {
		rate = float64(updates) / elapsed.Seconds()
	}
	pausedTag := ""
	if m.paused {
		pausedTag = output.Yellow + "  PAUSED" + output.Reset
	}
	plural := "s"
	if updates == 1 {
		plural = ""
	}
	return fmt.Sprintf(
		"%s%s▲ book %s%s   %d snapshot%s   %.1f/s%s",
		output.Bold, output.BrandGreen, viewName, output.Reset,
		updates, plural,
		rate, pausedTag,
	)
}

func (m bookModel) renderFooter(lastErr string) string {
	// Hint string comes from the shared keymap so every surface stays
	// in sync. ladder-back is a separate surface from ladder so the
	// drill-down case can advertise `esc back`.
	var surface string
	switch m.mode {
	case viewScan:
		surface = "scan"
	case viewLadder:
		if m.bt.layout == "ladder" {
			surface = "ladder"
		} else {
			surface = "ladder-back"
		}
	}
	footer := output.BrandGreyMid + footerHints(surface) + output.Reset
	if lastErr != "" {
		footer += "\n" + output.Yellow + "⚠ " + lastErr + output.Reset
	}
	return footer
}

// ─── small helpers ──────────────────────────────────────────────────────────

func bestLevels(snap *BookSnapshot) (bid, ask bookLevel) {
	if len(snap.Bids) > 0 {
		bid = snap.Bids[0]
	}
	if len(snap.Asks) > 0 {
		ask = snap.Asks[0]
	}
	return bid, ask
}

func liquidityForTier(snap *BookSnapshot, tier int) (bidLiq, askLiq, imb float64) {
	switch tier {
	case 10:
		return snap.BidLiq10, snap.AskLiq10, snap.Imbalance10
	case 20:
		return snap.BidLiq20, snap.AskLiq20, snap.Imbalance20
	case 50:
		return snap.BidLiq50, snap.AskLiq50, snap.Imbalance50
	default:
		return snap.BidLiq10, snap.AskLiq10, snap.Imbalance10
	}
}

// colorImbalance maps a -1..1 imbalance to a green/red percentage badge.
// Positive (more bids) → green, negative (more asks) → red.
func colorImbalance(imb float64) string {
	pct := imb * 100
	sign := "+"
	color := output.BrandGreen
	if imb < 0 {
		sign = ""
		color = output.Red
	}
	return color + fmt.Sprintf("%s%.1f%%", sign, pct) + output.Reset
}

// styleLevelPrice renders a price string with its side color, prefixed
// with a direction glyph when the level recently changed: `↑` if the
// level grew (liquidity arrived — usually a market-maker stacking),
// `↓` if it shrank or vanished (eaten by an aggressor or pulled).
//
// dir == 0 means "no recent change" — render plain. baseColor is the
// side color (red asks, green bids); the glyph itself is colored by
// direction (green for build, red for eat) so it reads independent of
// which side it's on. The flash is the glyph alone — we deliberately
// don't recolor the price cell, because prior versions of this code
// painted the whole cell yellow and ended up with a screen of solid
// yellow on a hot book.
func styleLevelPrice(price, baseColor string, dir int) string {
	switch dir {
	case +1:
		return output.BrandGreen + "↑" + output.Reset + " " + baseColor + price + output.Reset
	case -1:
		return output.Red + "↓" + output.Reset + " " + baseColor + price + output.Reset
	default:
		return "  " + baseColor + price + output.Reset
	}
}

// styleLevelSize renders a size cell, prefixing a "▲" marker and bumping
// to bold when the level qualifies as a "whale" (>=30% of its side's
// cumulative tier liquidity). The marker is rendered without color so it
// reads against any side — the bar already carries the side color.
func styleLevelSize(size string, whale bool) string {
	if whale {
		return output.Bold + "▲ " + size + output.Reset
	}
	return size
}

// sparklineMicro renders a 1-line unicode-block sparkline of recent
// microprice ticks. Empty input → empty string (so the header strip
// doesn't get an awkward "▁▁▁▁" before any data has arrived). Width
// renders as min(8, len(values)) — short enough to live next to MID
// without dominating the strip.
func sparklineMicro(values []float64) string {
	if len(values) < 2 {
		return ""
	}
	const width = 8
	v := values
	if len(v) > width {
		v = v[len(v)-width:]
	}
	min := v[0]
	max := v[0]
	for _, x := range v {
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	rng := max - min
	// Eight unicode block heights, low to high.
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	// Color the sparkline by net direction over the window — gives an
	// instant "drifting up vs down" cue without having to read the line.
	color := output.BrandGreyMid
	if v[len(v)-1] > v[0] {
		color = output.BrandGreen
	} else if v[len(v)-1] < v[0] {
		color = output.Red
	}
	b.WriteString(color)
	for _, x := range v {
		idx := 0
		if rng > 0 {
			frac := (x - min) / rng
			idx = int(frac * float64(len(blocks)-1))
			if idx < 0 {
				idx = 0
			}
			if idx >= len(blocks) {
				idx = len(blocks) - 1
			}
		}
		b.WriteRune(blocks[idx])
	}
	b.WriteString(output.Reset)
	return b.String()
}

// barLength returns the number of cells filled, log-scaled. Linear scaling
// flattens every small level when one big level exists (e.g. one 5 BTC
// quote next to dozens of 0.001 BTC quotes — the small ones round to 0
// and read as "no liquidity," which is wrong).
//
// We use log1p so size 0 → 0 cells, size > 0 → at least 1 cell, and the
// curve still distinguishes "tiny" from "huge" without being dominated by
// the largest. ceil(1) for any positive size guarantees a visible mark
// when there's any liquidity at all.
func barLength(size, maxSize float64, width int) int {
	if maxSize <= 0 || size <= 0 {
		return 0
	}
	// log1p(size) / log1p(maxSize) is in (0, 1] for size in (0, maxSize].
	frac := math.Log1p(size) / math.Log1p(maxSize)
	if frac > 1 {
		frac = 1
	}
	cells := int(math.Ceil(frac * float64(width)))
	if cells < 1 {
		cells = 1
	}
	if cells > width {
		cells = width
	}
	return cells
}

// barRight renders a horizontal bar that grows toward the right edge — used
// for the ask side, so the bar starts adjacent to the centre PRICE column
// and extends outward to the right.
func barRight(size, maxSize float64, width int, color string) string {
	filled := barLength(size, maxSize, width)
	return color + strings.Repeat("▮", filled) + output.Reset + strings.Repeat(" ", width-filled)
}

// barLeft renders a horizontal bar that grows toward the left edge — used
// for the bid side, so the bar starts at the right (adjacent to PRICE) and
// extends leftward.
func barLeft(size, maxSize float64, width int, color string) string {
	filled := barLength(size, maxSize, width)
	return strings.Repeat(" ", width-filled) + color + strings.Repeat("▮", filled) + output.Reset
}

// ─── number formatting ─────────────────────────────────────────────────────
//
// formatNumFull is great for tape rows but renders book sizes / prices with
// trailing IEEE float artifacts (5.5779999999999985, 12.001000000000001).
// The book ladder needs tighter rules: prices honor the venue tick, sizes
// drop to ~5 significant figures, cumulatives never show more decimals
// than the underlying.

// formatBookPrice renders a price with at most 2 decimal places when the
// number is large (>= 100), 4 when small (< 100), 6 when tiny (< 1). This
// matches what trading screens do and dodges most float artifacts. Always
// thousand-separated.
func formatBookPrice(v float64) string {
	if v == 0 {
		return "—"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	dec := 2
	switch {
	case abs >= 100:
		dec = 2
	case abs >= 1:
		dec = 4
	case abs >= 0.0001:
		dec = 6
	default:
		dec = 8
	}
	return formatNum(v, dec)
}

// formatBookSize renders a size or cumulative liquidity. We trim to 5
// significant digits — enough for tick-precise sizes on every venue we
// support (Binance lots are 0.001, Deribit 10, Kraken 0.0001, etc.) and
// avoids the IEEE noise from cumulative sums.
//
// For values < 0.001 we fall back to formatNumFull (scientific-ish), since
// 5 sig figs on 1e-7 just looks like zero.
func formatBookSize(v float64) string {
	if v == 0 {
		return "—"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs < 1e-3 {
		return formatNumFull(v)
	}
	// 5 significant figures: pick decimals dynamically.
	dec := 5
	mag := abs
	for mag >= 10 && dec > 0 {
		mag /= 10
		dec--
	}
	return formatNum(v, dec)
}

// orderedPairs is exposed for tests and for the cmd layer's diagnostics; not
// used by the renderer itself but harmless to leave reachable.
func (bt *BookTable) orderedPairs() []string {
	keys := bt.orderedKeys()
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.String()
	}
	sort.Strings(out)
	return out
}
