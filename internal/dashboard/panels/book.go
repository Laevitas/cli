// Package panels holds dashboard.Panel implementations — the actual
// rendered surfaces that compose into a dashboard. Each panel is
// independent (no cross-panel imports), implements dashboard.Panel,
// and pulls every kernel-supplied capability through PanelContext.
//
// File layout: one file per panel implementation. book.go renders
// the multi-venue order book for `laevitas dash book`; future panels
// (chart.go, tape.go, chain.go, screener.go) will land alongside.
package panels

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
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/ladder"
	"github.com/laevitas/cli/internal/output"
)

// ─── public constructor ────────────────────────────────────────────────────

// BookConfig configures a BookPanel.
//
// Two subscription modes:
//
//  1. Literal mode (legacy) — caller passes Market + Instrument.
//     Panel builds a single wildcard channel
//     "book.{Market}.*.{Instrument}" and lets the gateway resolve
//     it to concrete per-venue subscriptions. Header pair label is
//     just Instrument.
//
//  2. Resolved mode (currency-driven) — caller passes Market +
//     ResolvedChannels (pre-built per-venue channel list from the
//     instruments resolver). Panel subscribes to exactly those
//     channels; the wildcard form is bypassed because each venue
//     names its BTC perp differently (BTCUSDT, BTC-USDT-SWAP,
//     BTC-PERP, …) and a single glob can't match them all.
//     PairLabel is what the StatsLine header shows in the pair slot
//     (typically the user-facing currency, e.g. "BTC perp-linear").
//
// DepthTier defaults to 10 if zero — matches the rolling-tape book
// renderer's default and keeps UX consistent across surfaces.
type BookConfig struct {
	Market     string
	Instrument string // literal-mode symbol (e.g. BTCUSDT) — empty in resolved mode
	DepthTier  int

	// ResolvedChannels is the per-venue WS channel list from the
	// instruments resolver. Non-nil triggers resolved mode.
	ResolvedChannels []string

	// ResolvedVenues is the set of venues the resolver said list
	// this product. Used by the "waiting on …" footer so we don't
	// surface curated venues that simply don't carry the contract
	// (e.g. coinbase has no USDT perp). Empty in literal mode.
	ResolvedVenues []string

	// PairLabel is the header pair slot text. Used in both modes; in
	// literal mode it usually equals Instrument, in resolved mode it
	// reads as a friendly product label like "BTC perp-linear".
	PairLabel string
}

// NewBookPanel constructs the multi-venue book panel. The returned
// panel is dashboard.Panel-compatible and stores its own state; the
// caller (cmd/dash/book.go) hands it to the kernel via Config.Panels.
func NewBookPanel(cfg BookConfig) *BookPanel {
	if cfg.DepthTier == 0 {
		cfg.DepthTier = 10
	}
	pairLabel := cfg.PairLabel
	if pairLabel == "" {
		pairLabel = cfg.Instrument
	}
	var expected map[string]struct{}
	if len(cfg.ResolvedVenues) > 0 {
		expected = make(map[string]struct{}, len(cfg.ResolvedVenues))
		for _, v := range cfg.ResolvedVenues {
			expected[strings.ToLower(v)] = struct{}{}
		}
	}
	return &BookPanel{
		market:           strings.ToLower(cfg.Market),
		symbol:           cfg.Instrument,
		pairLabel:        pairLabel,
		resolvedChannels: cfg.ResolvedChannels,
		expectedVenues:   expected,
		depthTier:        cfg.DepthTier,
		books:            make(map[string]*api.BookSnapshot),
		hiddenVenues:     make(map[string]struct{}),
	}
}

// ─── BookPanel state ───────────────────────────────────────────────────────

// BookPanel renders the aggregated multi-venue book + per-venue
// strip + consolidated block. State is keyed by venue (lowercase
// exchange tag) — the panel discovers venues from arriving events
// rather than from a static list, so wildcard subscriptions
// (book.perpetuals.*.BTCUSDT) populate naturally.
//
// Goroutine safety: the panel only mutates state inside Update() —
// which Bubble Tea guarantees is single-threaded — so the books
// map needs no mutex. The mu field guards the few read paths that
// might fire from a goroutine if a future feature wants to read
// snapshots from outside the model loop; today it's unused but
// keeps the contract explicit.
type BookPanel struct {
	market string
	symbol string

	// pairLabel is what the StatsLine header shows in the pair slot.
	// Equals symbol in literal mode; equals a friendly label like
	// "BTC perp-linear" in resolved mode. Stored separately because
	// the symbol is empty in resolved mode (each venue has its own).
	pairLabel string

	// resolvedChannels, when non-nil, are the explicit per-venue
	// channels the resolver produced. Bypasses the wildcard form
	// because no single glob can match BTCUSDT, BTC-USDT-SWAP, and
	// BTC-PERP at the same time.
	resolvedChannels []string

	// expectedVenues is the set of venues we should see snapshots
	// from — used by the "waiting on …" footer to avoid telling the
	// user we're waiting on a venue that doesn't list this product
	// (e.g. coinbase has no USDT perp; hyperliquid has no inverse
	// perp). In resolved mode this comes from the resolver's per-
	// venue output. In literal mode it's empty, and the footer
	// falls back to the curated palette.
	expectedVenues map[string]struct{}

	// depthTier is the STATS tier (10/20/50). Drives which
	// pre-computed liquidity / imbalance numbers the strip and
	// CONSOLIDATED block read from each venue's snapshot. Cycled
	// by `d` via keymap.ActDepthCycle. Independent of viewport
	// row count — that's derived from terminal height.
	depthTier int

	// groupTickSize is the price-bucket width used to bucket
	// adjacent ladder levels. 0 means "use the venue's native tick
	// (no aggregation)." Doubled by `+`/halved by `-` via
	// keymap.ActGroupUp/Down.
	groupTickSize float64

	// viewport tracks where in the (potentially long) ladder we're
	// looking. Shared with the legacy ws book ladder via
	// internal/ladder so both surfaces use identical scroll/page/
	// recenter semantics.
	viewport ladder.Viewport

	mu    sync.RWMutex
	books map[string]*api.BookSnapshot // keyed by venue (lowercase)

	// hiddenVenues tracks venues the user has toggled off via `v`.
	// Hidden venues stop contributing to the aggregated ladder + the
	// venue strip + CONSOLIDATED math; the data is still ingested
	// (we just filter it out at render time so toggling back is
	// instant).
	hiddenVenues map[string]struct{}

	// venuePickerOpen + venuePickerCursor track the inline venue-
	// toggle picker. When open, ↑↓/jk navigate the picker; Enter
	// flips the highlighted venue's hidden state; v or Esc closes
	// the picker. Other keys are intercepted while the picker is
	// open so the user can't accidentally scroll the ladder while
	// trying to pick a venue.
	venuePickerOpen   bool
	venuePickerCursor int

	// width cache, refreshed on every WindowSizeMsg
	width, height int

	// paused freezes book replacement: while paused, ingest() drops
	// incoming snapshots so the visible state stays stable. Toggled
	// by the kernel on `p` (the action arrives as a tea.KeyMsg with
	// keymap.ActPause; we honour it because we declare Pause: true
	// in Capabilities). Unpause resumes ingestion at the next tick.
	paused bool

	// updates / startAt feed the shared ladder.HeaderLine renderer.
	// Counted across every venue's snapshot so the rate matches what
	// the user sees on screen — combined book-update throughput, not
	// per-venue. startAt is set on the first ingested snapshot so the
	// rate isn't artificially low while waiting on the first event.
	updates int64
	startAt time.Time

	// micro is the consolidated microprice ring buffer powering the
	// sparkline shown after MID on the StatsLine. Implementation
	// lives in internal/ladder so the legacy ws book ladder and any
	// future ladder-style dashboard reuse the same buffer + tuning.
	micro ladder.MicroRing

	// ladderMode chooses the centre-pane presentation:
	//   0 = aggregated (default) — one merged ladder, segmented bars
	//       coloured by venue contribution
	//   1 = split — one narrow per-venue ladder column, side-by-side,
	//       so the user can compare individual venue depth at a glance
	// Toggled by `m` (keymap.ActLadderMode). Same data feeds both;
	// the toggle is purely a render switch.
	ladderMode int
}

// Ladder-mode constants. Named here rather than as bare ints so the
// renderer's branch reads as `if p.ladderMode == ladderModeSplit`.
const (
	ladderModeAggregated = 0
	ladderModeSplit      = 1
)

// microValues returns a snapshot of the consolidated microprice
// ring in oldest-to-newest order. Lock-protected so the renderer
// path can read concurrently with ingest.
func (p *BookPanel) microValues() []float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.micro.Values()
}

// ─── dashboard.Panel implementation ────────────────────────────────────────

func (p *BookPanel) Init() tea.Cmd { return nil }

// Subscriptions builds the channel list the kernel should subscribe
// to.
//
// Resolved mode: if the panel was constructed with a pre-built per-
// venue channel list (currency-driven path: `dash book perpetuals
// BTC --margin linear`), return those channels verbatim. Each venue
// names its BTC perp-linear differently, so a single wildcard glob
// would miss venues — the resolver handles the per-venue lookup
// upstream.
//
// Literal mode: caller passed Market + Instrument (legacy path:
// `dash book perpetuals BTCUSDT`). Build the wildcard pattern
// `book.{Market}.*.{Instrument}` and let the gateway resolve which
// venues carry that exact symbol.
func (p *BookPanel) Subscriptions(_ dashboard.Selection) dashboard.FeedSpec {
	if len(p.resolvedChannels) > 0 {
		return dashboard.FeedSpec{Channels: p.resolvedChannels}
	}
	if p.symbol == "" || p.market == "" {
		return dashboard.FeedSpec{}
	}
	return dashboard.FeedSpec{
		Channels: []string{
			fmt.Sprintf("book.%s.*.%s", p.market, p.symbol),
		},
	}
}

func (p *BookPanel) Title() string {
	return fmt.Sprintf("book — %s · %s", p.symbol, p.market)
}

// Capabilities declares which keymap features the book panel honors.
// The kernel ORs this with its layout-derived flags (MultiPane) to
// build the effective surface capabilities used by the footer + help
// overlay. Adding a new key globally means editing keymap.go; opting
// into it on this panel means flipping a flag here.
//
// Set:
//   - ListNav:     ↑↓/jk/PgUp/PgDn/g/G + wheel scroll the viewport
//                  through the ladder (panel implements scroll
//                  itself in its Update).
//   - Group:       +/- widen / narrow price grouping (bucket
//                  adjacent ticks).
//   - DepthCycle:  d cycles stats depth (10 → 20 → 50). Affects
//                  strip / CONSOLIDATED math; doesn't change
//                  rendered row count.
//   - VenueToggle: v opens an inline picker to hide / show venues.
//   - Pause:       p freezes book replacement.
//   - Help:        ? brings up the overlay.
func (p *BookPanel) Capabilities() keymap.Capabilities {
	return keymap.Capabilities{
		ListNav:     true,
		Group:       true,
		DepthCycle:  true,
		Recenter:    true,
		LadderMode:  true,
		VenueToggle: true,
		Pause:       true,
		Help:        true,
	}
}

// Update consumes one Bubble Tea message and mutates state. Three
// message classes matter:
//
//   - dashboard.FeedTickMsg → decode the book snapshot and store it
//     under its venue key.
//   - tea.WindowSizeMsg     → cache size for layout decisions in View().
//   - tea.KeyMsg            → handle panel-specific keys (v toggle,
//     +/- depth tier). Global keys are intercepted by the kernel
//     before they reach us.
func (p *BookPanel) Update(msg tea.Msg) (dashboard.Panel, tea.Cmd) {
	switch m := msg.(type) {
	case dashboard.FeedTickMsg:
		p.ingest(m.Event.Channel, m.Event.Data)
		return p, nil

	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		return p, nil

	case tea.KeyMsg:
		// Route panel keys through the shared keymap vocabulary.
		// Three orthogonal control surfaces:
		//   ListNav (↑↓/jk/PgUp/PgDn/g/G) → viewport scroll
		//   Group   (+/-)                 → price bucket size
		//   Depth   (d)                   → stats tier (10/20/50)
		//   Venue   (v)                   → toggle venue picker
		//   Pause   (p)                   → freeze book replacement
		//
		// While the venue picker is open, ListNav keys navigate the
		// picker instead of the ladder, and Enter flips the
		// selected venue. v or Esc closes the picker.
		action := keymap.ClassifyKey(m.String())
		if p.venuePickerOpen {
			return p, p.handlePickerKey(action)
		}
		// Every action routes through internal/ladder helpers so the
		// dashboard panel and the legacy ws book ladder behave
		// identically. Adding a new action here means adding it
		// to ladder.Viewport (or its sibling helpers) and
		// declaring the matching capability flag — never copying
		// scroll math.
		rowCap := ladder.RowCap(p.height)
		switch action {
		case keymap.ActUp:
			p.viewport.ScrollUp(rowCap)
		case keymap.ActDown:
			p.viewport.ScrollDown(rowCap)
		case keymap.ActPageUp:
			p.viewport.PageUp(rowCap)
		case keymap.ActPageDown:
			p.viewport.PageDown(rowCap)
		case keymap.ActTop:
			// "Top" snaps the viewport so the worst-price ask sits
			// at the top of the visible window. We pass a generous
			// upper bound; render-time clamp in viewport.Apply
			// trims to actual data length.
			p.viewport.SnapTop(1 << 20)
		case keymap.ActBottom:
			p.viewport.SnapBottom(1 << 20)
		case keymap.ActRecenter:
			p.viewport.Recenter()
		case keymap.ActLadderMode:
			// Cycle aggregated → split → aggregated. Two modes
			// today; expanding to a third (heatmap) in v0.9.0
			// will rotate through the same switch.
			if p.ladderMode == ladderModeAggregated {
				p.ladderMode = ladderModeSplit
			} else {
				p.ladderMode = ladderModeAggregated
			}
			// Reset viewport when changing layouts so split mode
			// starts centred and the user isn't scrolled deep into
			// a different rendering they no longer have a frame
			// of reference for.
			p.viewport.Recenter()
		case keymap.ActGroupUp:
			p.groupTickSize = ladder.NextGroupTick(p.groupTickSize)
		case keymap.ActGroupDown:
			p.groupTickSize = ladder.PrevGroupTick(p.groupTickSize)
		case keymap.ActDepthCycle:
			p.depthTier = ladder.NextDepthTier(p.depthTier)
		case keymap.ActVenueToggle:
			p.venuePickerOpen = true
			p.venuePickerCursor = 0
		case keymap.ActPause:
			p.paused = !p.paused
		}
		return p, nil
	}
	return p, nil
}

// handlePickerKey routes keystrokes while the venue picker is open.
// Returns nil because the picker handles everything synchronously
// in-state; nothing to schedule via Cmd.
func (p *BookPanel) handlePickerKey(action keymap.Action) tea.Cmd {
	keys := p.orderedVenuesAll()
	switch action {
	case keymap.ActUp:
		if p.venuePickerCursor > 0 {
			p.venuePickerCursor--
		}
	case keymap.ActDown:
		if p.venuePickerCursor < len(keys)-1 {
			p.venuePickerCursor++
		}
	case keymap.ActEnter:
		if p.venuePickerCursor >= 0 && p.venuePickerCursor < len(keys) {
			v := keys[p.venuePickerCursor]
			if _, hidden := p.hiddenVenues[v]; hidden {
				delete(p.hiddenVenues, v)
			} else {
				p.hiddenVenues[v] = struct{}{}
			}
		}
	case keymap.ActVenueToggle, keymap.ActEsc:
		p.venuePickerOpen = false
	}
	return nil
}

// orderedVenuesAll returns every discovered venue (regardless of
// hidden state) in display order, used by the venue picker so the
// user can toggle hidden venues back on.
func (p *BookPanel) orderedVenuesAll() []string {
	p.mu.RLock()
	books := make(map[string]*api.BookSnapshot, len(p.books))
	for k, v := range p.books {
		books[k] = v
	}
	p.mu.RUnlock()
	return p.orderedVenues(books)
}

// ingest decodes a book.* event payload and stores it under the
// venue key. Soft-fails on malformed payloads — they get dropped
// silently rather than crashing the panel; the user notices the
// venue is missing via its still-spinning placeholder row.
//
// While paused (toggled by `p`), incoming snapshots are dropped
// instead of replacing the cached state. Resume by pressing `p`
// again — the next tick replaces with current data, no
// reconciliation needed since each snapshot is full-state.
func (p *BookPanel) ingest(channel string, data json.RawMessage) {
	if p.paused {
		return
	}
	venue := venueFromChannel(channel)
	if venue == "" {
		return
	}
	var snap api.BookSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	snap.Channel = channel
	snap.ReceivedAt = time.Now()

	p.mu.Lock()
	p.books[venue] = &snap
	if p.startAt.IsZero() {
		p.startAt = snap.ReceivedAt
	}
	p.updates++
	// Recompute consolidated microprice on every tick and push to
	// the sparkline ring. We rebuild VenueBooks from the current
	// `p.books` map; cost is O(venues × top-N) per tick which is
	// trivial at our update rate.
	venues := make([]string, 0, len(p.books))
	for v := range p.books {
		venues = append(venues, v)
	}
	vbs := buildVenueBooks(p.books, venues, p.depthTier)
	p.micro.Push(agg.VolumeWeightedMid(vbs))
	p.mu.Unlock()
}

// venueFromChannel extracts the lowercase exchange tag from a
// "book.{market}.{exchange}.{instrument}" channel string. Returns
// empty string on malformed input.
func venueFromChannel(channel string) string {
	parts := strings.Split(channel, ".")
	if len(parts) < 4 || parts[0] != "book" {
		return ""
	}
	return parts[2]
}

// snapshot returns a stable copy of the books map, filtered to
// exclude any venues the user has toggled off via `v`. Used by
// View() so the rendering pass sees a consistent picture even if
// Update() fires between map reads.
//
// Hidden venues are dropped here rather than at every render call
// site so the ladder + strip + consolidated math all see the same
// filtered set automatically. The picker itself uses
// orderedVenuesAll() which bypasses this filter so hidden venues
// remain togglable back on.
func (p *BookPanel) snapshot() map[string]*api.BookSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[string]*api.BookSnapshot, len(p.books))
	for k, v := range p.books {
		if _, hidden := p.hiddenVenues[k]; hidden {
			continue
		}
		out[k] = v
	}
	return out
}

// orderedVenues returns the discovered venues in stable display
// order — first by curated palette ranking (read from
// output.CuratedVenueNames so the panel and palette never drift),
// then alphabetically for long-tail venues. Stable across renders
// so rows don't jump around as new venues come online.
func (p *BookPanel) orderedVenues(books map[string]*api.BookSnapshot) []string {
	curated := output.CuratedVenueNames()
	curatedSet := make(map[string]struct{}, len(curated))
	for _, v := range curated {
		curatedSet[v] = struct{}{}
	}

	out := []string{}
	for _, v := range curated {
		if _, has := books[v]; has {
			out = append(out, v)
		}
	}

	tail := []string{}
	for v := range books {
		if _, isCurated := curatedSet[v]; !isCurated {
			tail = append(tail, v)
		}
	}
	sort.Strings(tail)
	return append(out, tail...)
}

// ─── View ──────────────────────────────────────────────────────────────────

// View renders the panel into width × height cells. Layout is the
// 60/40 split we agreed on: aggregated ladder left, per-venue strip
// + consolidated block right. Falls back to a single-pane "waiting"
// placeholder until the first snapshot lands so the user knows the
// system is alive.
func (p *BookPanel) View(width, height int, ctx dashboard.PanelContext) string {
	books := p.snapshot()
	venues := p.orderedVenues(books)

	// Venue picker overlays everything else when open. We render
	// the picker into the full panel area so the user clearly sees
	// they're in a different mode; pressing v / Esc closes it back
	// to the regular view.
	if p.venuePickerOpen {
		return p.renderVenuePicker(width, height, ctx)
	}

	// Empty state: no venues have fired yet. Show a centred loading
	// indicator using the kernel-supplied spinner frame; FeedState
	// disambiguates "we're connecting" from "we're connected, just
	// waiting for the first snapshot."
	if len(venues) == 0 {
		return p.renderWaiting(width, height, ctx)
	}

	// Decide layout based on width. On narrow terminals we collapse
	// the venue strip to a single-line summary at the bottom of the
	// ladder pane — same idea as the existing book scan view's
	// terminal-too-narrow fallback.
	const narrowThreshold = 100
	if width < narrowThreshold {
		return p.renderNarrow(width, height, books, venues, ctx)
	}

	// Layout budget: cap ladder + strip at their natural content
	// widths instead of letting each grab a fixed % of the screen.
	// Earlier versions reserved 60% for the ladder, which on a wide
	// terminal pushed the strip far to the right with a huge empty
	// band in between. The ladder's content (2 × bars + 2 × sizes
	// + price + gutters) tops out around 80 cells; past that, the
	// extra space is just whitespace. Strip cards cap at cardCapW
	// (42) plus a 2-cell gutter on each side.
	//
	// On narrow-but-not-tiny terminals (≤ ladderMax + stripMax) we
	// still split proportionally so neither pane gets squashed.
	// Layout budget. Aggregated mode: ladder content tops out around
	// 80 cells, so cap ladderW there and give the rest to whitespace
	// after the strip — past that, extra width is just empty band.
	//
	// Split mode: every additional cell of ladderW lets one more
	// venue fit without dropping. Take whatever the terminal gives,
	// minus the strip's needs. Same shared header / footer chrome,
	// the centre pane just claims more horizontal real estate.
	// Aggregated mode now carries cumulative-liquidity columns on
	// each side (CUM_BID … PRICE … CUM_ASK), so the natural
	// content width grew from ~78 to ~100 cells. Past that, extra
	// width is empty band again.
	const aggLadderMax = 100
	const stripMax = 54 // cardCapW(50) + a little breathing room
	const gap = 4
	stripW := stripMax
	var ladderW int
	if p.ladderMode == ladderModeSplit {
		// Strip stays at its content cap; ladder takes the rest.
		ladderW = width - stripW - gap
		if ladderW < 30 {
			ladderW = 30
		}
	} else {
		ladderW = aggLadderMax
		if width < aggLadderMax+stripMax+gap {
			ladderW = (width - gap) * 60 / 100
			stripW = width - ladderW - gap
			if stripW < 30 {
				stripW = 30
				ladderW = width - stripW - gap
			}
		}
	}

	// Render the two-line top header (HeaderLine + StatsLine) at full
	// terminal width — separately from the ladder pane below it. The
	// stats line is ~92 cells, wider than ladderW (78), so embedding
	// it inside renderLadder would let joinSideBySide truncate it.
	// Keeping it on its own line above the side-by-side block lets
	// every field render regardless of how the ladder/strip split is
	// chosen.
	header := p.renderTopHeader(books, venues)
	// Two header lines + a blank — subtract from the body height so
	// the ladder + strip fit within the panel.
	const topChrome = 3
	bodyH := height - topChrome
	if bodyH < 4 {
		bodyH = 4
	}

	ladder := p.renderLadder(ladderW, bodyH, books, venues, ctx)
	strip := p.renderStrip(stripW, bodyH, books, venues, ctx)

	return header + "\n" + output.JoinSideBySide(ladder, ladderW, strip)
}

// renderTopHeader builds the two-line top header (HeaderLine +
// StatsLine) that spans the FULL terminal width — rendered by View()
// above the ladder + strip block so neither the ladder column cap
// nor joinSideBySide can truncate it.
//
// The two lines are the same shape every book surface uses (see
// internal/ladder.HeaderLine / StatsLine): a user moving between
// `laevitas ws book` and `laevitas dash book` reads identical fields in the
// same order.
func (p *BookPanel) renderTopHeader(books map[string]*api.BookSnapshot, venues []string) string {
	vbs := buildVenueBooks(books, venues, 100)
	q := agg.CrossVenueSpread(vbs)
	microPx := agg.VolumeWeightedMid(vbs)

	// Aggregate to compute imbalance / liquidity at the configured
	// depth tier — same convention as the ladder body uses.
	asks, bids := agg.AggregatedDepth(vbs)
	if p.groupTickSize > 0 {
		asks = ladder.BucketLevels(asks, p.groupTickSize, true)
		bids = ladder.BucketLevels(bids, p.groupTickSize, false)
	}
	tierForLiq := p.depthTier
	bidLiqAgg := 0.0
	for i, l := range bids {
		if i >= tierForLiq {
			break
		}
		bidLiqAgg += l.Size
	}
	askLiqAgg := 0.0
	for i, l := range asks {
		if i >= tierForLiq {
			break
		}
		askLiqAgg += l.Size
	}
	imbAgg := 0.0
	if total := bidLiqAgg + askLiqAgg; total > 0 {
		imbAgg = (bidLiqAgg - askLiqAgg) / total
	}

	rate := 0.0
	if !p.startAt.IsZero() {
		if elapsed := time.Since(p.startAt).Seconds(); elapsed > 0 {
			rate = float64(p.updates) / elapsed
		}
	}
	hStyle := ladder.HeaderStyle{
		Bold:   output.Bold,
		Accent: output.BrandGreen,
		Grey:   output.BrandGreyMid,
		Warn:   output.Yellow,
		Reset:  output.Reset,
	}
	line1 := ladder.HeaderLine(ladder.HeaderInfo{
		Surface:  "aggregated ladder",
		Pair:     p.pairLabel,
		Updates:  p.updates,
		RatePerS: rate,
		Paused:   p.paused,
	}, hStyle)
	line2 := ladder.StatsLine(ladder.StatsInfo{
		Mid:       microPx,
		BpsSpread: q.SpreadBps,
		Spread:    q.Spread,
		ArbPx:     q.Arb,
		BidLiq:    bidLiqAgg,
		AskLiq:    askLiqAgg,
		Imb:       imbAgg,
		DepthTier: p.depthTier,
		GroupTick: p.groupTickSize,
		Sparkline: output.SparklineMicro(p.microValues()),
	}, hStyle, ladder.StatsFormatter{
		Price:     output.FormatBookPrice,
		Size:      output.FormatBookSize,
		Num:       output.FormatNum,
		Imbalance: output.ColorImbalance,
	})
	return line1 + "\n" + line2
}

// renderVenuePicker draws the inline venue toggle picker. Lists
// every discovered venue (hidden + visible) with a check/cross
// glyph, highlighting the cursor row. Up/down moves the cursor;
// Enter flips the highlighted venue's hidden state; v or Esc
// closes the picker.
func (p *BookPanel) renderVenuePicker(width, height int, ctx dashboard.PanelContext) string {
	keys := p.orderedVenuesAll()
	bold := output.Bold
	green := output.BrandGreen
	grey := output.BrandGreyMid
	red := output.Red
	reset := output.Reset

	var b strings.Builder
	b.WriteString(bold + green + "▲ venue visibility" + reset + grey +
		" — " + p.symbol + " · " + p.market + reset + "\n\n")

	if len(keys) == 0 {
		b.WriteString(grey + "no venues discovered yet" + reset)
		return b.String()
	}

	for i, v := range keys {
		vc, _ := output.VenueColor(v)
		marker := "  "
		if i == p.venuePickerCursor {
			marker = green + "▸ " + reset
		}
		state := green + "✓ shown" + reset
		if _, hidden := p.hiddenVenues[v]; hidden {
			state = red + "✗ hidden" + reset
		}
		row := marker + vc.FG + vc.Icon + " " + strings.ToUpper(v) + reset +
			"   " + state
		b.WriteString(row + "\n")
	}

	b.WriteString("\n" + grey + "↑↓/jk move   enter toggle   v / esc close" + reset)
	return b.String()
}

// renderWaiting paints the empty-state placeholder. Uses the
// kernel's spinner frame so the animation matches the connection
// pill and any future panel-level spinners.
func (p *BookPanel) renderWaiting(w, h int, ctx dashboard.PanelContext) string {
	grey := output.BrandGreyMid
	bold := output.Bold
	green := output.BrandGreen
	reset := output.Reset

	subline := "subscribed · waiting for first snapshot…"
	switch ctx.FeedState {
	case dashboard.FeedDialing:
		subline = "connecting to gateway…"
	case dashboard.FeedReconnecting:
		subline = "reconnecting…"
	case dashboard.FeedFatal:
		subline = "disconnected"
		if ctx.LastError != "" {
			subline = "disconnected · " + ctx.LastError
		}
	}

	body := bold + green + "▲ book" + reset + grey + " · " + p.pairLabel + " · " + p.market + reset +
		"\n\n" +
		grey + ctx.SpinnerFrame + " " + subline + reset
	return body
}

// renderNarrow is the < 100-col fallback. Drops the venue strip;
// the consolidated block goes on a single line under the ladder.
func (p *BookPanel) renderNarrow(w, h int, books map[string]*api.BookSnapshot, venues []string, ctx dashboard.PanelContext) string {
	ladder := p.renderLadder(w, h-2, books, venues, ctx)
	consol := p.renderConsolidatedLine(w, books, venues)
	return ladder + "\n" + consol
}

// ─── aggregated ladder ─────────────────────────────────────────────────────

// renderLadder draws the centre-price ladder with segmented bars
// coloured by venue contribution. Bloomberg DEPT layout:
//
//   ▲ aggregated ladder    MID 76,082.05  SPREAD 0.10 (0.13 bps)  DEPTH 10
//   bar | size | PRICE                       (asks, worst→best top→down)
//   ───── spread 0.10 ─────                  (separator)
//                              PRICE | size | bar       (bids, best→worst)
//
// Important contract:
//   * p.depthTier is the STATS depth (10/20/50). It tells the strip
//     and consolidated block which pre-computed liquidity sums to
//     read off the wire. It does NOT control how many rows render —
//     that's derived from terminal height so the ladder always fits
//     the viewport. Cycling tier via +/- changes the math that
//     drives imbalance/liquidity numbers, not the rendered count.
//
//   * rowCap = (h - chrome) / 2 per side. Chrome is the header row,
//     spread separator, and one row of breathing space (3 lines
//     total). Each side gets half the remaining height; uneven
//     remainder goes to the asks side so best-ask sits closest to
//     the spread separator regardless of parity.
//
//   * Header SPREAD is computed via agg.CrossVenueSpread so it
//     swaps to "ARB +X" when the consolidated book is crossed
//     instead of lying with a negative number.
func (p *BookPanel) renderLadder(w, h int, books map[string]*api.BookSnapshot, venues []string, ctx dashboard.PanelContext) string {
	// Split mode is a different presentation of the same data; it
	// lives in book_render_split.go so the aggregated path here
	// stays focused. The shared header + spread/arb separator are
	// re-emitted in split form because the layout differs.
	if p.ladderMode == ladderModeSplit {
		return p.renderSplitLadder(w, h, books, venues, ctx)
	}

	// Build per-venue VenueBook slice from the FULL book (server caps
	// at ~100 levels) — we bucket + viewport-slice after aggregation,
	// not before, so consolidated levels beyond the rendered window
	// don't disappear from the agg result.
	vbs := buildVenueBooks(books, venues, 100)

	// Aggregated depth across all venues, sorted asks-asc / bids-desc.
	asks, bids := agg.AggregatedDepth(vbs)

	// Apply price-grouping bucket if the user has zoomed out via `+`.
	// Bucket size 0 means "use venue native tick" — pass-through.
	if p.groupTickSize > 0 {
		asks = ladder.BucketLevels(asks, p.groupTickSize, true)
		bids = ladder.BucketLevels(bids, p.groupTickSize, false)
	}

	// rowsPerSide is the smaller of:
	//   1. terminal height minus chrome, halved (rowCap)
	//   2. depth tier (10 / 20 / 50)
	//
	// Earlier dev removed the tier cap when fixing the conflation
	// between "stats depth" and "rendered row count" — that swung
	// too far the other way. Tier should still cap rows, just no
	// longer drive them. Cap = `min(rowCap, tier)` is the right
	// shape: a tall terminal at tier=10 shows 10 levels per side;
	// at tier=50, shows 50 (or rowCap if the terminal is short).
	const chrome = 3
	rowCap := (h - chrome) / 2
	if rowCap < 1 {
		rowCap = 1
	}
	if rowCap > 60 {
		rowCap = 60 // safety cap on absurdly tall windows
	}
	rowsPerSide := rowCap
	if p.depthTier > 0 && p.depthTier < rowsPerSide {
		rowsPerSide = p.depthTier
	}

	// Apply viewport offset, then slice to rowsPerSide. positive
	// offset = scrolled up (showing deeper asks, fewer bids);
	// negative = scrolled down. Clamp so the user can't scroll
	// into negative-row territory on either side.
	asks, bids = p.viewport.Apply(asks, bids, rowsPerSide)

	// Bar scaling reads max size across the visible window only —
	// using the full 100-level max would compress the visible bars
	// into invisible specs when one deep level is huge.
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

	// Column widths derived once and reused so asks + bids align on
	// the same price column. The ladder is a 7-column grid:
	//   cum | size | bar | PRICE | bar | size | cum
	// Bid side fills [cum | size | bar | PRICE]; ask side fills
	// [PRICE | bar | size | cum]. Cumulative columns explode
	// outward from the spread — each one the running total of the
	// liquidity between the user and that price level. Lets the
	// trader eyeball "how much do I walk through to fill X size".
	const sizeW = 10
	const priceW = 12
	const cumW = 9
	const gutter = 1
	barW := (w - 2*sizeW - 2*cumW - priceW - 6*gutter) / 2
	if barW < 6 {
		barW = 6
	}
	if barW > 26 {
		barW = 26
	}

	q := agg.CrossVenueSpread(vbs)

	green := output.BrandGreen
	red := output.Red
	grey := output.BrandGreyMid
	reset := output.Reset

	var b strings.Builder

	// Header (HeaderLine + StatsLine) is rendered separately in
	// View() at full terminal width — see renderTopHeader. Past
	// versions emitted it inline here, but at ladderW=78 the stats
	// line (~92 cells) got truncated by joinSideBySide.

	// Pre-compute per-side cumulative liquidity. asks[0] is best-ask
	// (lowest price) — so askCum[i] = sum of asks[0..i] (inclusive).
	// Reading top-down through the asks block (worst → best), the
	// cumulative shrinks; reading the cell next to a price tells the
	// user "to fill at this price-or-better, you'd consume this much
	// liquidity". Symmetric on the bids side.
	askCum := make([]float64, len(asks))
	{
		acc := 0.0
		for i, l := range asks {
			acc += l.Size
			askCum[i] = acc
		}
	}
	bidCum := make([]float64, len(bids))
	{
		acc := 0.0
		for i, l := range bids {
			acc += l.Size
			bidCum[i] = acc
		}
	}

	// ─── asks block: worst-price top, best-price bottom ───────────
	// Layout per row:
	//   [left-half-pad] [PRICE] [gutter] [bar] [gutter] [size] [gutter] [CUM]
	// Left half pad fills the columns where bid content will sit
	// below, so the centre PRICE column lines up across both blocks.
	leftHalfPad := strings.Repeat(" ", cumW+gutter+sizeW+gutter+barW+gutter)
	for i := len(asks) - 1; i >= 0; i-- {
		lvl := asks[i]
		segments := segmentsForLevel(lvl, venues, vbs, true)
		priceCell := padCellRight(red+output.FormatBookPrice(lvl.Price)+reset, priceW)
		bar := output.SegmentedBarRight(segments, maxSize, barW)
		sizeCell := padCellRight(output.FormatBookSize(lvl.Size), sizeW)
		cumCell := padCellRight(grey+output.FormatBookSize(askCum[i])+reset, cumW)
		b.WriteString(leftHalfPad + priceCell + " " + bar + " " + sizeCell + " " + cumCell + "\n")
	}

	// ─── spread / arb separator ───────────────────────────────────
	// Centre the separator under the price column so it visually
	// links the asks block (above) to the bids block (below). Left
	// pad equals the bar+size columns on the bid side; that puts
	// the dashes on either side of the spread/arb label flush with
	// the price column above and below.
	sepLabel := "spread " + output.FormatBookPrice(q.Spread)
	if q.Arb > 0 {
		sepLabel = "ARB +" + output.FormatBookPrice(q.Arb)
	}
	sepDashes := strings.Repeat("─", 8)
	// Land the leading dash run so its trailing space sits flush
	// with the start of the PRICE column. PRICE column starts at
	// column (cumW + gutter + sizeW + gutter + barW + gutter); we
	// subtract the 8-char dash run + its 1-char trailing space.
	sepPadW := cumW + gutter + sizeW + gutter + barW + gutter - len(sepDashes) - 1
	if sepPadW < 0 {
		sepPadW = 0
	}
	b.WriteString(strings.Repeat(" ", sepPadW) + grey + sepDashes + " " + sepLabel + " " + sepDashes + reset + "\n")

	// ─── bids block: best-price top, worst-price bottom ───────────
	// Layout per row:
	//   [CUM] [gutter] [size] [gutter] [bar] [gutter] [PRICE] [right-half-pad]
	// Mirror of the asks block: cumulative is the outermost column
	// on each side, growing as the user reads away from the spread.
	rightHalfPad := strings.Repeat(" ", gutter+barW+gutter+sizeW+gutter+cumW)
	for i, lvl := range bids {
		segments := segmentsForLevel(lvl, venues, vbs, false)
		cumCell := padCellLeft(grey+output.FormatBookSize(bidCum[i])+reset, cumW)
		sizeCell := padCellRight(output.FormatBookSize(lvl.Size), sizeW)
		bar := output.SegmentedBarLeft(segments, maxSize, barW)
		priceCell := padCellRight(green+output.FormatBookPrice(lvl.Price)+reset, priceW)
		b.WriteString(cumCell + " " + sizeCell + " " + bar + " " + priceCell + rightHalfPad + "\n")
	}

	return b.String()
}

// padCellRight right-aligns content within a cell of `width`
// visible columns. Visible width is measured via output.VisibleWidth
// so ANSI escapes and unicode-cell glyphs both count correctly.
func padCellRight(s string, width int) string {
	visible := output.VisibleWidth(s)
	if visible >= width {
		return s
	}
	return strings.Repeat(" ", width-visible) + s
}

// padCellLeft left-aligns content within a cell of `width`. Same
// width measurement as padCellRight.
func padCellLeft(s string, width int) string {
	visible := output.VisibleWidth(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

// segmentsForLevel turns one agg.AggregatedLevel into the per-venue
// segments output.SegmentedBar* expects, ordered by venuePalette
// rank so the bars render with the same colour ordering across
// every level.
//
// asks bool selects which side's per-venue contribution to look up
// in the original VenueBook slice — agg.AggregatedDepth merges by
// price but loses per-venue size attribution, so we re-walk the
// per-venue books to recover it.
func segmentsForLevel(lvl agg.AggregatedLevel, venues []string, vbs []agg.VenueBook, asks bool) []output.BarSegment {
	out := make([]output.BarSegment, 0, len(lvl.Sources))
	for _, v := range venues {
		// Find this venue's contribution to this price level.
		var levels []agg.VenueLevel
		for i := range vbs {
			if vbs[i].Venue == v {
				if asks {
					levels = vbs[i].Asks
				} else {
					levels = vbs[i].Bids
				}
				break
			}
		}
		for _, l := range levels {
			if l.Price == lvl.Price && l.Size > 0 {
				vc, _ := output.VenueColor(v)
				out = append(out, output.BarSegment{Size: l.Size, Color: vc.FG})
				break
			}
		}
	}
	return out
}

// ─── venue strip + consolidated block ──────────────────────────────────────
// Implementation lives in book_strip.go — renderStrip,
// renderVenueCard, renderConsolidatedCard, renderConsolidatedBlock,
// renderConsolidatedLine, missingCuratedVenues, cardStyle,
// colorImbalanceLG, and the brand hex constants. Split out in
// v0.8.3 so the next dashboard (perp screener, vol surface) can
// reuse the bordered-card pattern by reading one focused file.

// buildVenueBooks is the common adapter from our internal map →
// agg.VenueBook slice. Centralised here so the ladder, strip, and
// consolidated-block all see the same trimmed top-N data.
func buildVenueBooks(books map[string]*api.BookSnapshot, venues []string, tier int) []agg.VenueBook {
	vbs := make([]agg.VenueBook, 0, len(venues))
	for _, v := range venues {
		snap := books[v]
		if snap == nil {
			continue
		}
		vb := agg.VenueBook{Venue: v}
		bidN := minInt(tier, len(snap.Bids))
		askN := minInt(tier, len(snap.Asks))
		vb.Bids = make([]agg.VenueLevel, bidN)
		vb.Asks = make([]agg.VenueLevel, askN)
		for i := 0; i < bidN; i++ {
			vb.Bids[i] = agg.VenueLevel{Price: snap.Bids[i].Price, Size: snap.Bids[i].Size}
		}
		for i := 0; i < askN; i++ {
			vb.Asks[i] = agg.VenueLevel{Price: snap.Asks[i].Price, Size: snap.Asks[i].Size}
		}
		vbs = append(vbs, vb)
	}
	return vbs
}

// ─── small helpers ─────────────────────────────────────────────────────────

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// (groupLabel, bucketLevels, applyViewOffset, nextDepthTier,
// prevDepthTier all moved to internal/ladder so the legacy
// ws book ladder shares the exact same implementation. Local
// copies removed in v0.8.3 to enforce DRY.)

// Width / padding / truncate / side-by-side helpers moved to
// internal/output (layout.go) so every dashboard surface and the
// legacy ws renderers measure widths through one ANSI- and
// unicode-aware path. Local copies removed in v0.8.3.

// Compile-time check that BookPanel satisfies dashboard.Panel.
// Catches signature drift on the interface side at build time.
var _ dashboard.Panel = (*BookPanel)(nil)
