package output

// Single-venue ladder renderer — the canonical visual for one
// venue's order book. Every surface that wants "the standard book
// ladder" calls this function so flow detail and legacy ws book do
// not drift.
//
// Why this lives in `internal/output`:
//   - Pure rendering, no goroutines, no shared state, no
//     terminal coupling. Same shape as the rest of book_*.go.
//   - The two callers (the legacy `ws book` ladder and the
//     dashboard flow's BOOK card) have nothing else in common —
//     wsrender owns the WS plumbing, the dashboard panel owns
//     bubbletea integration. The renderer is the only piece of
//     code they share, so it sits in a neutral package.
//
// What's NOT here:
//   - The aggregated multi-venue ladder (`dash book`'s consolidated
//     view). That has fundamentally different shape (CONSOLIDATED
//     block, per-venue strip, multi-venue depth merge) and stays
//     in internal/dashboard/panels/book.go.
//   - State machines (depth-tier cycle, group-size cycle, viewport
//     scroll). Callers track their own state and pass the current
//     values in via LadderRenderOpts.
//
// The renderer takes a wide opts struct rather than a long
// parameter list because there are 8+ inputs and adding a new
// optional field (e.g. flash colors per row when v0.11 ships
// per-cell coloring) shouldn't cascade signature changes through
// every caller. New zero-value-safe fields don't break callers.

import (
	"strings"

	"github.com/laevitas/cli/internal/agg"
	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/ladder"
)

// LadderRenderOpts bundles every input the renderer needs.
//
// Snapshot is required (no nil-snap rendering — callers should
// detect that case and render a "waiting for snapshot…" line at
// their own layer, since the wording varies by surface).
//
// All other fields are zero-value-safe:
//   - DepthTier 0 → no per-side cap (render every level the
//     snapshot carries).
//   - GroupTickSize 0 → no price grouping (raw venue ticks).
//   - Viewport zero value → no scroll, ladder anchors on the
//     spread.
//   - Flashes nil → no level flashes (fresh-just-changed arrows).
//   - Sparkline "" → no sparkline next to MID in the stats line.
//   - BarWidth 0 → defaults to 18 cells (matches the legacy
//     renderer; tweak per-caller for narrow surfaces).
//   - Width 0 → unbounded; the table renderer pads as needed but
//     does not enforce a max. Pass the surface's actual width to
//     get truncation when rows overflow.
//   - Height 0 → unbounded; combined with viewport.Apply this
//     means "show everything that fits the data."
type LadderRenderOpts struct {
	// Snapshot is the order book to render. Must be non-nil; the
	// renderer panics on nil rather than rendering an empty
	// ladder, because nil-snap is a caller-side bug (the caller
	// should have rendered a placeholder).
	Snapshot *api.BookSnapshot

	// DepthTier caps the number of rows per side considered for
	// rendering. 0 = no cap. Tier-but-not-fitting rows become
	// reachable via the Viewport scroll path.
	DepthTier int

	// GroupTickSize buckets adjacent levels into wider price
	// bins. 0 = no grouping (raw levels). Cycled by the caller's
	// `+/-` keybinding via ladder.NextGroupTick / PrevGroupTick.
	GroupTickSize float64

	// Viewport tracks scroll position when the rendered ladder
	// is taller than the available height. Zero value is fine —
	// it means "anchor on the spread, no scroll." Pass a non-zero
	// viewport to support `↑↓/jk` scrolling within the ladder.
	Viewport ladder.Viewport

	// Flashes maps a price-string → flash level (0–N) for the
	// "this level just changed" arrow. Caller computes flashes
	// based on its own diff state; we just render them. nil =
	// no flashes.
	Flashes map[string]int

	// Sparkline is a pre-rendered glyph row plotted next to the
	// MID value in the stats line. Empty = no sparkline. Caller
	// computes via output.SparklineMicro from a microprice ring.
	Sparkline string

	// BarWidth is the width in cells of the per-row depth bars.
	// 0 → 18 (legacy default). Narrower surfaces (e.g. flow
	// detail's BOOK card at ~50 cells) can pass a smaller value
	// to keep the bars from dominating the row.
	BarWidth int

	// Width is the total render width. Used by the table layout
	// to right-truncate columns that overflow.
	Width int

	// Height is the total render height (rows). Drives the
	// per-side row budget via ladder.RowCap. 0 = unbounded — the
	// renderer emits as many rows as the data carries, suitable
	// for non-pane surfaces. Pane surfaces (flow detail's BOOK
	// card) should pass the card's interior height so the row cap
	// fits on screen without scrolling off.
	Height int

	// Paused tells the renderer to suppress flashes — when the
	// snapshot is frozen on `p`, "this level just moved" arrows
	// would be misleading. Stats line is unchanged; only the
	// flash overlay is gated by this field.
	Paused bool
}

// RenderSingleVenueLadder renders one venue's book ladder in the
// canonical CLI visual. Output is a multi-line string suitable for
// embedding in a TUI surface; trailing newline included so the
// caller can join with subsequent rows.
//
// The output shape is:
//
//	<stats line>
//	<blank>
//	     PRICE     SIZE      CUM
//	 80,964.40    0.012   10.002  ███
//	 80,964.30    0.038    9.990  █████
//	 ...
//	 ── MID 80,963.10 / spread 0.100000 ──
//	 80,962.80    5.166    5.166  █████████
//	 80,962.70    0.578    5.744  ██
//	 ...
//
// Asks descend (worst at top, best just above the spread); bids
// descend from the spread (best at top, worst at bottom). Both
// sides share the same PRICE / SIZE / CUM / bar columns; colour
// and vertical position identify side. This is the normal trading
// ladder shape and uses terminal width better than the old
// bid-left / ask-right table.
//
// Whale ▲ marker fires when a level holds ≥30% of its side's
// tier-cumulative liquidity.
func RenderSingleVenueLadder(opts LadderRenderOpts) string {
	snap := opts.Snapshot
	if snap == nil {
		// Caller bug — should have rendered a placeholder.
		// Return empty rather than panic so a misuse doesn't
		// crash the TUI; the caller will notice the empty
		// surface and fix.
		return ""
	}

	barWidth := opts.BarWidth
	if barWidth <= 0 {
		barWidth = 18
	}

	// Stats line: pair, MID + sparkline, spread, IMB, LIQ,
	// DEPTH, GROUP. Same shape as legacy ws book + dash book.
	bid, ask := snap.BestLevels()
	spread := 0.0
	bps := 0.0
	if ask.Price > 0 && bid.Price > 0 {
		spread = ask.Price - bid.Price
		if snap.Microprice > 0 {
			bps = (spread / snap.Microprice) * 10_000
		}
	}
	bidLiq, askLiq, imb := snap.LiquidityForTier(opts.DepthTier)
	stats := ladder.StatsLine(ladder.StatsInfo{
		Mid:       snap.Microprice,
		BpsSpread: bps,
		Spread:    spread,
		ArbPx:     0, // single-venue: no cross-venue arb
		BidLiq:    bidLiq,
		AskLiq:    askLiq,
		Imb:       imb,
		DepthTier: opts.DepthTier,
		GroupTick: opts.GroupTickSize,
		Sparkline: opts.Sparkline,
	}, ladder.HeaderStyle{
		Bold:   Bold,
		Accent: BrandGreen,
		Grey:   BrandGreyMid,
		Warn:   Yellow,
		Reset:  Reset,
	}, ladder.StatsFormatter{
		Price:     FormatBookPrice,
		Size:      FormatBookSize,
		Num:       FormatNum,
		Imbalance: ColorImbalance,
	})

	// Pipeline: tier-cap → bucket → viewport → render.
	asks := bookLevelsToAggLevels(snap.Asks, snap.Exchange)
	bids := bookLevelsToAggLevels(snap.Bids, snap.Exchange)
	if opts.DepthTier > 0 {
		if len(asks) > opts.DepthTier {
			asks = asks[:opts.DepthTier]
		}
		if len(bids) > opts.DepthTier {
			bids = bids[:opts.DepthTier]
		}
	}
	if opts.GroupTickSize > 0 {
		asks = ladder.BucketLevels(asks, opts.GroupTickSize, true)
		bids = ladder.BucketLevels(bids, opts.GroupTickSize, false)
	}
	// Allocate actual visible data rows. Unlike the old side-by-side
	// table, the stacked ladder shares one vertical budget across asks
	// and bids, then reallocates spare rows from a short side to the
	// other side so a tall pane does not end with artificial blank
	// space.
	askRows, bidRows := stackedRowAllocation(len(asks), len(bids), opts.Height, opts.Viewport.Offset)
	asks = asks[:askRows]
	bids = bids[:bidRows]

	// Find the maximum size across both sides so the per-row
	// bars are scaled consistently. (Per-side scaling makes the
	// shorter side's bars look artificially heavy.)
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

	// Per-side cumulative totals — for the cum_ask / cum_bid
	// columns and for the whale-marker denominator.
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

	// Flashes go quiet on pause — frozen snapshot, "level moved"
	// arrows would be misleading.
	flashes := opts.Flashes
	if opts.Paused {
		flashes = nil
	}

	headers := []string{"PRICE", "SIZE", "CUM", ""}
	aligns := []ladderColAlign{ladderAlignRight, ladderAlignRight, ladderAlignRight, ladderAlignLeft}
	rows := make([][]string, 0, len(asks)+len(bids))

	// Asks block — worst-price at top, best-price at bottom.
	for i := len(asks) - 1; i >= 0; i-- {
		l := asks[i]
		ps := FormatBookPrice(l.Price)
		whale := askTotal > 0 && l.Size/askTotal >= whaleThreshold
		rows = append(rows, []string{
			styleLevelPriceLocal(ps, Red, flashes[ps]),
			StyleLevelSize(FormatBookSize(l.Size), whale),
			FormatBookSize(askCums[i]),
			BarLeft(l.Size, maxSize, barWidth, Red),
		})
	}

	// Bids block — best bid at top, worst at bottom.
	bidRowsOut := make([][]string, 0, len(bids))
	for i, l := range bids {
		ps := FormatBookPrice(l.Price)
		whale := bidTotal > 0 && l.Size/bidTotal >= whaleThreshold
		bidRowsOut = append(bidRowsOut, []string{
			styleLevelPriceLocal(ps, BrandGreen, flashes[ps]),
			StyleLevelSize(FormatBookSize(l.Size), whale),
			FormatBookSize(bidCums[i]),
			BarLeft(l.Size, maxSize, barWidth, BrandGreen),
		})
	}

	table := renderStackedLadderTable(headers, rows, bidRowsOut, aligns, opts.Width, spread, snap.Microprice)
	combined := stats + "\n\n" + table

	// Width clamp: every output line must fit in opts.Width so an
	// overflow never bleeds into the neighbouring pane. The stats
	// line (ladder.StatsLine) and the table renderer don't enforce
	// width on their own — they pad-but-don't-truncate, which is
	// fine on a full-screen surface but eats adjacent panes when
	// the BOOK card is squeezed to ~30 cells in flow detail mode.
	//
	// We post-process line-by-line via TruncateAnsi so ANSI SGR
	// state (red prices, green prices, brand-grey separators)
	// stays balanced after the cut. Lines under width are passed
	// through unchanged.
	if opts.Width <= 0 {
		return combined
	}
	lines := strings.Split(combined, "\n")
	for i, line := range lines {
		if VisibleWidth(line) > opts.Width {
			lines[i] = TruncateAnsi(line, opts.Width)
		}
	}
	return strings.Join(lines, "\n")
}

func stackedRowAllocation(askLen, bidLen, height, offset int) (askRows, bidRows int) {
	if height <= 0 {
		return askLen, bidLen
	}
	// Stats line + blank + table header + spread row.
	const chrome = 4
	budget := height - chrome
	if budget < 2 {
		budget = 2
	}
	askRows = budget/2 + offset
	bidRows = budget - askRows
	if askRows < 0 {
		askRows = 0
	}
	if bidRows < 0 {
		bidRows = 0
	}
	if askRows > askLen {
		askRows = askLen
	}
	if bidRows > bidLen {
		bidRows = bidLen
	}
	spare := budget - askRows - bidRows
	for spare > 0 && (askRows < askLen || bidRows < bidLen) {
		if offset >= 0 {
			if askRows < askLen {
				askRows++
			} else if bidRows < bidLen {
				bidRows++
			}
		} else {
			if bidRows < bidLen {
				bidRows++
			} else if askRows < askLen {
				askRows++
			}
		}
		spare--
	}
	return askRows, bidRows
}

// bookLevelsToAggLevels adapts api.BookLevel → agg.AggregatedLevel
// for the shared ladder helpers (BucketLevels, Viewport.Apply).
// Single-venue ladder: every level carries the same venue tag in
// Sources. Drops zero-size levels on the way through — they would
// inflate the bucketing pass without adding rendered content.
func bookLevelsToAggLevels(levels []api.BookLevel, venue string) []agg.AggregatedLevel {
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

// styleLevelPriceLocal wraps the price string in the side's color
// and overlays the flash arrow when the level just changed. We
// duplicate StyleLevelPrice's contract here as a thin wrapper
// because the public name didn't follow the lift; rather than
// rename and risk breaking unrelated callers, we keep a local
// alias.
func styleLevelPriceLocal(price, base string, flash int) string {
	return StyleLevelPrice(price, base, flash)
}

// ─── private table renderer ────────────────────────────────────────────────
//
// Lifted from internal/wsrender/wsrender.go's renderTable + colAlign.
// Duplicated rather than moved because moving would touch every
// wsrender call site and risks regressing the legacy renderer's
// observable behavior. The two copies are small (~60 lines) and
// the logic is mature; if we ever lift the rest of wsrender's
// table machinery to internal/output, this duplicate goes away.

type ladderColAlign int

const (
	ladderAlignLeft ladderColAlign = iota
	ladderAlignRight
)

func renderLadderTable(headers []string, rows [][]string, aligns []ladderColAlign, width int) string {
	if len(headers) == 0 {
		return ""
	}
	cols := len(headers)
	if aligns == nil {
		aligns = make([]ladderColAlign, cols)
	}

	widths := make([]int, cols)
	for c := 0; c < cols; c++ {
		widths[c] = VisibleWidth(headers[c])
	}
	for _, row := range rows {
		for c := 0; c < cols && c < len(row); c++ {
			if w := VisibleWidth(row[c]); w > widths[c] {
				widths[c] = w
			}
		}
	}

	const gutter = 2

	var b strings.Builder
	b.WriteString(Bold + BrandGreyLight)
	for c := 0; c < cols; c++ {
		if c > 0 {
			b.WriteString(strings.Repeat(" ", gutter))
		}
		b.WriteString(padLadderCell(headers[c], widths[c], aligns[c]))
	}
	b.WriteString(Reset)
	b.WriteByte('\n')

	for _, row := range rows {
		for c := 0; c < cols; c++ {
			if c > 0 {
				b.WriteString(strings.Repeat(" ", gutter))
			}
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			b.WriteString(padLadderCell(cell, widths[c], aligns[c]))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func renderStackedLadderTable(headers []string, askRows, bidRows [][]string, aligns []ladderColAlign, width int, spread, mid float64) string {
	if len(headers) == 0 {
		return ""
	}
	cols := len(headers)
	if aligns == nil {
		aligns = make([]ladderColAlign, cols)
	}

	widths := make([]int, cols)
	for c := 0; c < cols; c++ {
		widths[c] = VisibleWidth(headers[c])
	}
	allRows := append([][]string{}, askRows...)
	allRows = append(allRows, bidRows...)
	for _, row := range allRows {
		for c := 0; c < cols && c < len(row); c++ {
			if w := VisibleWidth(row[c]); w > widths[c] {
				widths[c] = w
			}
		}
	}

	const gutter = 2
	tableW := 0
	for c, w := range widths {
		if c > 0 {
			tableW += gutter
		}
		tableW += w
	}
	if width > 0 && tableW > width {
		tableW = width
	}

	var b strings.Builder
	b.WriteString(Bold + BrandGreyLight)
	for c := 0; c < cols; c++ {
		if c > 0 {
			b.WriteString(strings.Repeat(" ", gutter))
		}
		b.WriteString(padLadderCell(headers[c], widths[c], aligns[c]))
	}
	b.WriteString(Reset)
	b.WriteByte('\n')

	writeRow := func(row []string) {
		for c := 0; c < cols; c++ {
			if c > 0 {
				b.WriteString(strings.Repeat(" ", gutter))
			}
			cell := ""
			if c < len(row) {
				cell = row[c]
			}
			b.WriteString(padLadderCell(cell, widths[c], aligns[c]))
		}
		b.WriteByte('\n')
	}
	for _, row := range askRows {
		writeRow(row)
	}

	spreadLabel := "── MID " + FormatBookPrice(mid) + " / spread " + FormatBookPrice(spread) + " ──"
	b.WriteString(BrandGreyMid + PadRightAnsi(TruncateAnsi(spreadLabel, tableW), tableW) + Reset)
	b.WriteByte('\n')

	for _, row := range bidRows {
		writeRow(row)
	}
	return b.String()
}

func padLadderCell(s string, width int, align ladderColAlign) string {
	pad := width - VisibleWidth(s)
	if pad <= 0 {
		return s
	}
	spaces := strings.Repeat(" ", pad)
	if align == ladderAlignRight {
		return spaces + s
	}
	return s + spaces
}
