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
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/agg"
	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/ladder"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

// BookSnapshot is re-exported here as a convenience alias so existing
// internal callers don't need to update their imports. The canonical
// type lives in internal/api/book.go and is shared with the dashboard
// panels.
type BookSnapshot = api.BookSnapshot

// bookLevel kept as an unexported alias for the same reason — the
// older code in this file refers to the unexported name throughout
// and the alias avoids a noisy rename.
type bookLevel = api.BookLevel

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

	// micro is a per-pair ring buffer of recent microprices. Used by
	// the header sparkline. The ring implementation now lives in
	// internal/ladder so this renderer and the dashboard book panel
	// share one buffer + tuning; map values are pointers so the
	// underlying array isn't copied on every read.
	micro map[pairKey]*ladder.MicroRing

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

// NewBookTable creates a renderer for the given subscription set. layout
// chooses the default view ("scan" for multi-pair, "ladder" for single).
func NewBookTable(channels []string, layout string) *BookTable {
	return &BookTable{
		channels: channels,
		layout:   layout,
		books:    make(map[pairKey]*BookSnapshot, len(channels)),
		changes:  make(map[flashKey]levelChange),
		micro:    make(map[pairKey]*ladder.MicroRing, len(channels)),
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

	// Update microprice ring. Push() guards against NaN / non-positive
	// internally so we don't need a wrapper check here.
	ring := bt.micro[key]
	if ring == nil {
		ring = &ladder.MicroRing{}
		bt.micro[key] = ring
	}
	ring.Push(snap.Microprice)
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
	return r.Values()
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

	// Depth tier the ladder shows: 10, 20, 50. Cycled with `d`.
	// Legacy versions cycled this with `+/-`; in v0.8.3 the keymap
	// was unified across surfaces so `+/-` is now grouping and
	// `d` cycles tier — matches the dashboard book panel.
	depthTier int

	// groupTickSize buckets adjacent ladder levels into wider price
	// bins. 0 = native venue tick. Cycled by `+/-` via
	// internal/ladder.NextGroupTick / PrevGroupTick.
	groupTickSize float64

	// viewport tracks scroll position when the rendered ladder is
	// taller than the terminal viewport. Shared with the dashboard
	// book panel via internal/ladder so both surfaces have
	// identical scroll/page/recenter behaviour.
	viewport ladder.Viewport

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
			} else if m.mode == viewLadder {
				m.viewport.ScrollUp(ladder.RowCap(m.height))
			}
			return m, nil
		case actDown:
			if m.mode == viewScan {
				keys := m.bt.orderedKeys()
				if m.cursor < len(keys)-1 {
					m.cursor++
				}
			} else if m.mode == viewLadder {
				m.viewport.ScrollDown(ladder.RowCap(m.height))
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
			} else if m.mode == viewLadder {
				m.viewport.PageUp(ladder.RowCap(m.height))
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
			} else if m.mode == viewLadder {
				m.viewport.PageDown(ladder.RowCap(m.height))
			}
			return m, nil
		case actTop:
			if m.mode == viewScan {
				m.cursor = 0
				m.scrollTop = 0
			} else if m.mode == viewLadder {
				m.viewport.SnapTop(1 << 20)
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
			} else if m.mode == viewLadder {
				m.viewport.SnapBottom(1 << 20)
			}
			return m, nil
		case actDepthUp:
			// `+` widens price grouping (zoom out) — same semantic
			// as the dashboard book panel. Old behaviour was depth-
			// tier cycle on `+/-`; in v0.8.3 the keymap unified so
			// `+/-` is grouping everywhere, `d` is depth tier.
			if m.mode == viewLadder {
				m.groupTickSize = ladder.NextGroupTick(m.groupTickSize)
			}
			return m, nil
		case actDepthDown:
			if m.mode == viewLadder {
				m.groupTickSize = ladder.PrevGroupTick(m.groupTickSize)
			}
			return m, nil
		case keymap.ActDepthCycle:
			// `d` cycles stats depth tier 10 → 20 → 50.
			if m.mode == viewLadder {
				m.depthTier = ladder.NextDepthTier(m.depthTier)
			}
			return m, nil
		case keymap.ActRecenter:
			// `c` snaps the viewport back to centred-on-spread.
			if m.mode == viewLadder {
				m.viewport.Recenter()
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

	// Stats line via shared ladder.StatsLine — identical shape to
	// the dashboard aggregated ladder. ArbPx is always 0 here:
	// single-venue books can't cross themselves. Sparkline is
	// fetched from the per-pair microprice ring buffer so the
	// "is the mid moving?" signal stays inline with the MID value.
	spark := sparklineMicro(m.bt.microValuesForPair(key))
	strip := ladder.StatsLine(ladder.StatsInfo{
		Mid:       snap.Microprice,
		BpsSpread: bps,
		Spread:    spread,
		ArbPx:     0,
		BidLiq:    bidLiq,
		AskLiq:    askLiq,
		Imb:       imb,
		DepthTier: m.depthTier,
		GroupTick: m.groupTickSize,
		Sparkline: spark,
	}, ladderHeaderStyle(), ladderStatsFormatter())

	// Layout: cum_bid | bid_size | bid_bar | PRICE | ask_bar | ask_size | cum_ask
	// Asks descend from top of frame (worst price at top, best price just
	// above the spread separator); bids descend from spread separator
	// (best price at top of bid block, worst at bottom). That puts the
	// best bid and best ask physically adjacent to the spread row.
	//
	// Pipeline: tier-cap → bucket (group) → viewport apply → render.
	// Same shape as the dashboard book panel uses; both surfaces lean
	// on internal/ladder helpers so the math is identical.
	asks := bookLevelsToAgg(snap.Asks, snap.Exchange)
	bids := bookLevelsToAgg(snap.Bids, snap.Exchange)

	// Tier sets the data window — render up to N rows per side.
	// Default is to consider all of tier; rowCap (terminal height)
	// further limits, with viewport scroll allowing access to rows
	// that don't fit on screen at once.
	tier := m.depthTier
	if len(asks) > tier {
		asks = asks[:tier]
	}
	if len(bids) > tier {
		bids = bids[:tier]
	}

	// Apply price grouping if user has zoomed out via `+`.
	if m.groupTickSize > 0 {
		asks = ladder.BucketLevels(asks, m.groupTickSize, true)
		bids = ladder.BucketLevels(bids, m.groupTickSize, false)
	}

	// rowCap = how many rows per side we can fit on screen. Then
	// the viewport carves out a window of {asks, bids} of that
	// size. Tier-but-not-fitting rows become reachable via scroll.
	rowCap := ladder.RowCap(m.height)
	asks, bids = m.viewport.Apply(asks, bids, rowCap)

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

	// Flashes go quiet on pause — the user has frozen the snapshot, so
	// arrows tagging "this level just moved" are misleading: nothing
	// can move while paused. Live mode keeps the 250ms flash window.
	var flashes map[string]int
	if !m.paused {
		flashes = m.bt.flashesForPair(key, 250*time.Millisecond)
	}

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

// renderHeader delegates to the shared ladder.HeaderLine so the
// legacy book surfaces (scan + ladder) and the dashboard book panel
// emit the exact same top line — surface name, pair, snapshot
// count, rate, PAUSED tag. One source of truth: change the format
// in internal/ladder and every surface picks it up.
func (m bookModel) renderHeader(viewName string, updates int64, elapsed time.Duration) string {
	rate := 0.0
	if elapsed.Seconds() > 0 {
		rate = float64(updates) / elapsed.Seconds()
	}
	// Pair label is only meaningful when the user has drilled into a
	// specific pair (ladder mode) or launched directly into ladder
	// for a single pair. In scan mode we're showing many pairs at
	// once, so the header stays pair-less.
	pair := ""
	if m.mode == viewLadder {
		keys := m.bt.orderedKeys()
		if m.cursor < len(keys) && m.cursor >= 0 {
			pair = keys[m.cursor].String()
		}
	}
	return ladder.HeaderLine(ladder.HeaderInfo{
		Surface:  "book " + viewName,
		Pair:     pair,
		Updates:  updates,
		RatePerS: rate,
		Paused:   m.paused,
	}, ladderHeaderStyle())
}

// ladderHeaderStyle returns the wsrender book surface's palette for
// the shared ladder.HeaderLine helper. Brand-green accent, mid-grey
// labels, yellow PAUSED — same colours every other ladder surface
// uses so the top line looks identical wherever the user is.
//
// Named ladderHeaderStyle (not headerStyle) because there's already
// an unrelated headerStyle in wsrender.go that styles single-string
// section headers — keeping both names distinct avoids accidental
// collisions when grep-extending the helper.
func ladderHeaderStyle() ladder.HeaderStyle {
	return ladder.HeaderStyle{
		Bold:   output.Bold,
		Accent: output.BrandGreen,
		Grey:   output.BrandGreyMid,
		Warn:   output.Yellow,
		Reset:  output.Reset,
	}
}

// ladderStatsFormatter wires the wsrender book surface's formatting
// helpers into the shared ladder.StatsLine renderer. The package
// keeps zero internal/output imports, so each caller passes thin
// wrappers around the formatters its surface already uses. Same
// formatters the legacy ladder used inline before extraction —
// numbers and styling stay identical.
func ladderStatsFormatter() ladder.StatsFormatter {
	return ladder.StatsFormatter{
		Price:     output.FormatBookPrice,
		Size:      output.FormatBookSize,
		Num:       output.FormatNum,
		Imbalance: output.ColorImbalance,
	}
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

// ─── thin shims to the shared output package ───────────────────────────────
//
// Every formatting helper this renderer used to define inline now lives in
// internal/output (book_format.go). The shims below preserve the local
// names so existing call sites in this file don't all need editing in the
// same commit; new code (the dashboard book panel, future renderers)
// should call output.* directly.
//
// Once the dashboard panels stabilise we can sweep this file's call sites
// over to output.* and delete the shims entirely.

func bestLevels(snap *BookSnapshot) (bid, ask bookLevel) { return snap.BestLevels() }

func liquidityForTier(snap *BookSnapshot, tier int) (bidLiq, askLiq, imb float64) {
	return snap.LiquidityForTier(tier)
}

func colorImbalance(imb float64) string         { return output.ColorImbalance(imb) }
func styleLevelPrice(price, base string, d int) string {
	return output.StyleLevelPrice(price, base, d)
}
func styleLevelSize(size string, whale bool) string { return output.StyleLevelSize(size, whale) }
func sparklineMicro(values []float64) string        { return output.SparklineMicro(values) }
func barLength(size, maxSize float64, w int) int    { return output.BarLength(size, maxSize, w) }
func barRight(size, maxSize float64, w int, c string) string {
	return output.BarRight(size, maxSize, w, c)
}
func barLeft(size, maxSize float64, w int, c string) string {
	return output.BarLeft(size, maxSize, w, c)
}
func formatBookPrice(v float64) string { return output.FormatBookPrice(v) }
func formatBookSize(v float64) string  { return output.FormatBookSize(v) }

// bookLevelsToAgg adapts a single-venue api.BookLevel slice to the
// agg.AggregatedLevel form the shared ladder helpers expect. Each
// level becomes a degenerate aggregate with one source (the venue's
// exchange tag), so ladder.BucketLevels and ladder.Viewport.Apply
// work identically across single-venue (this file) and multi-venue
// (dashboard book panel) surfaces. Zero-size levels are dropped on
// the way through — they are never rendered and would only inflate
// the bucketing pass.
func bookLevelsToAgg(levels []bookLevel, venue string) []agg.AggregatedLevel {
	out := make([]agg.AggregatedLevel, 0, len(levels))
	for _, l := range levels {
		if l.Size <= 0 {
			continue
		}
		out = append(out, agg.AggregatedLevel{
			Price:   l.Price,
			Size:    l.Size,
			Sources: []string{venue},
		})
	}
	return out
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
