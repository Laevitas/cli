// Package ladder is the shared rendering primitives for every L2
// order-book ladder surface in the CLI: the legacy single-venue
// renderer (internal/wsrender), the multi-venue dashboard panel
// (internal/dashboard/panels), and any future ladder UI.
//
// One implementation, two callers, identical behaviour. When we
// added price grouping + viewport scrolling to the dashboard book
// panel and then needed the same on the legacy ladder, the choice
// was "duplicate or extract." Duplicating creates two parallel
// implementations that have to stay in sync; extracting puts the
// shared logic here and lets each caller wire it through their
// own model.
//
// This package owns:
//   - Group-tick cycling (`+/-` widens / narrows price buckets)
//   - Depth-tier cycling (`d` rotates 10 → 20 → 50 → 100)
//   - Bucketing levels into price-grouped rows
//   - Viewport state + scroll/page/recenter ops
//   - Row-cap math (terminal-height → max visible rows per side)
//
// It deliberately does NOT own:
//   - The actual lipgloss / ANSI rendering of bars, prices, sizes.
//     That's renderer-specific and stays per-package.
//   - Key dispatch — callers translate keymap.Action values into
//     calls on the helpers here.
package ladder

import (
	"github.com/laevitas/cli/internal/agg"
)

// ─── group-tick cycle ──────────────────────────────────────────────────────

// NextGroupTick widens the price-bucket size by ~2x. The 0 → 0.01
// transition is the user's first `+` press from "native venue
// tick" mode; 0.01 is a reasonable starting bucket for crypto perp
// prices and matches every venue's minimum tick we've seen.
//
// Cycle stops at 50.00 — past that the buckets are coarser than
// any meaningful price action on a single visible viewport.
func NextGroupTick(g float64) float64 {
	switch g {
	case 0:
		return 0.01
	case 0.01:
		return 0.05
	case 0.05:
		return 0.10
	case 0.10:
		return 0.50
	case 0.50:
		return 1.00
	case 1.00:
		return 5.00
	case 5.00:
		return 10.00
	case 10.00:
		return 50.00
	}
	return g
}

// PrevGroupTick narrows the price-bucket size. Bottoms out at 0
// (native tick) — pressing `-` past that is a no-op.
func PrevGroupTick(g float64) float64 {
	switch g {
	case 50.00:
		return 10.00
	case 10.00:
		return 5.00
	case 5.00:
		return 1.00
	case 1.00:
		return 0.50
	case 0.50:
		return 0.10
	case 0.10:
		return 0.05
	case 0.05:
		return 0.01
	case 0.01:
		return 0
	}
	return 0
}

// GroupLabel renders the active grouping bucket size for a header
// label. `0` reads as "tick" — i.e., the venue's native tick size,
// no extra bucketing applied. That reads better than "native" or
// "off" at the strip level: traders know "tick" means "as the venue
// quotes it." Once `+` is pressed the label switches to the actual
// bucket dollar size (e.g. "0.10", "5.00").
func GroupLabel(g float64) string {
	if g <= 0 {
		return "tick"
	}
	// Format with up to 2 decimals so 0.10 reads as "0.10" and 50
	// reads as "50". Anything finer (0.01, 0.05) is rendered with
	// its native precision.
	if g >= 1 {
		return floatString(g, 2)
	}
	return floatString(g, 4)
}

// ─── depth-tier cycle ──────────────────────────────────────────────────────

// NextDepthTier rotates 10 → 20 → 50 → 100 → 10. The wire payload
// exposes pre-computed liquidity stats at exactly these four tiers
// (ask_liquidity_10/20/50/100, bid_liquidity_*, imbalance_*), so
// the cycle matches what's available without re-deriving anything
// client-side.
//
// 100 is included as of v0.8.4 — earlier the cycle stopped at 50
// on the (correct) reasoning that 100 rows × 2 sides won't fit in
// a typical terminal. Now that the renderer enforces a hard chrome
// budget (RowCap + View()'s body clip in wsrender), 100 just shows
// the per-side data window deeper than the visible viewport — the
// user scrolls (j/k, PgUp/PgDn) to reach the bottom rows. Same UX
// as 50, just more data behind the scroll.
func NextDepthTier(d int) int {
	switch d {
	case 10:
		return 20
	case 20:
		return 50
	case 50:
		return 100
	default:
		return 10
	}
}

// PrevDepthTier rotates the cycle backwards: 100 → 50 → 20 → 10 → 100.
func PrevDepthTier(d int) int {
	switch d {
	case 100:
		return 50
	case 50:
		return 20
	case 20:
		return 10
	default:
		return 100
	}
}

// ─── bucketing ─────────────────────────────────────────────────────────────

// BucketLevels groups adjacent agg.AggregatedLevel entries into
// price buckets of width `bucket`. Each output level represents a
// price range; size is summed across every contributing level;
// Sources merges contributors (deduped). Bucket boundaries align
// to multiples of `bucket` so a $0.10 group always lands on
// .00, .10, .20 etc. regardless of where the actual levels sit.
//
// ascending == true bucketizes ask side (price ascending); false
// for bids (price descending). When bucket <= 0, returns input
// unchanged (caller is in "native tick" mode).
func BucketLevels(levels []agg.AggregatedLevel, bucket float64, ascending bool) []agg.AggregatedLevel {
	if bucket <= 0 || len(levels) == 0 {
		return levels
	}
	type entry struct {
		bucketKey int64
		level     agg.AggregatedLevel
	}
	keyed := make([]entry, 0, len(levels))
	for _, l := range levels {
		// Floor to a multiple of bucket. We use the integer-rounded
		// price/bucket as the key so floating-point doesn't fragment
		// otherwise-equal buckets across IEEE noise.
		var k int64
		if ascending {
			k = int64(l.Price / bucket)
		} else {
			k = int64(l.Price / bucket)
			if l.Price < float64(k)*bucket {
				k--
			}
		}
		keyed = append(keyed, entry{bucketKey: k, level: l})
	}

	type bucketAcc struct {
		key      int64
		size     float64
		sources  map[string]struct{}
		repPrice float64 // representative price (best level in bucket)
	}
	order := []*bucketAcc{}
	byKey := make(map[int64]*bucketAcc)

	for _, e := range keyed {
		acc, ok := byKey[e.bucketKey]
		if !ok {
			acc = &bucketAcc{
				key:      e.bucketKey,
				sources:  make(map[string]struct{}),
				repPrice: e.level.Price,
			}
			byKey[e.bucketKey] = acc
			order = append(order, acc)
		} else {
			if ascending && e.level.Price < acc.repPrice {
				acc.repPrice = e.level.Price
			} else if !ascending && e.level.Price > acc.repPrice {
				acc.repPrice = e.level.Price
			}
		}
		acc.size += e.level.Size
		for _, s := range e.level.Sources {
			acc.sources[s] = struct{}{}
		}
	}

	out := make([]agg.AggregatedLevel, 0, len(order))
	for _, acc := range order {
		// Render the representative price at the bucket boundary
		// so the price column lines up cleanly. e.g. $76,012.37 in
		// a $0.10 bucket renders as 76,012.30.
		bucketBase := float64(acc.key) * bucket
		sources := make([]string, 0, len(acc.sources))
		for s := range acc.sources {
			sources = append(sources, s)
		}
		out = append(out, agg.AggregatedLevel{
			Price:   bucketBase,
			Size:    acc.size,
			Sources: sources,
		})
	}
	return out
}

// ─── viewport ──────────────────────────────────────────────────────────────

// Viewport tracks where in the (potentially long) ladder we're
// looking. The ladder always renders up to `len(asks) + len(bids)`
// rows of data; the viewport carves out a window of that data so
// the rendered ladder never exceeds terminal height.
//
// Offset semantics:
//
//	offset = 0   → centred on the spread (default)
//	offset > 0   → scrolled UP (showing deeper asks, fewer bids)
//	offset < 0   → scrolled DOWN (showing deeper bids, fewer asks)
//
// Tier separately controls how much DATA we render at all (e.g.
// tier 50 = render up to 50 levels per side); the viewport then
// shows whatever fits in the terminal at the current offset.
//
// Callers mutate the viewport via the methods below in response to
// keymap.Action events.
type Viewport struct {
	Offset int
}

// ScrollUp moves the viewport up one row (showing deeper asks).
func (v *Viewport) ScrollUp(rowCap int) {
	v.Offset++
	v.clamp(rowCap)
}

// ScrollDown moves the viewport down one row (showing deeper bids).
func (v *Viewport) ScrollDown(rowCap int) {
	v.Offset--
	v.clamp(rowCap)
}

// PageUp jumps the viewport up by half the visible page so the
// user keeps a row of context across the page boundary.
func (v *Viewport) PageUp(rowCap int) {
	v.Offset += pageStep(rowCap)
	v.clamp(rowCap)
}

// PageDown jumps the viewport down by half the visible page.
func (v *Viewport) PageDown(rowCap int) {
	v.Offset -= pageStep(rowCap)
	v.clamp(rowCap)
}

// SnapTop sets the viewport so the worst-price ask is at the top
// of the visible window. Used by `g`.
func (v *Viewport) SnapTop(maxAsks int) {
	v.Offset = maxAsks
}

// SnapBottom sets the viewport so the worst-price bid is at the
// bottom of the visible window. Used by `G`.
func (v *Viewport) SnapBottom(maxBids int) {
	v.Offset = -maxBids
}

// Recenter sets the viewport to 0 — back to centred on the spread.
// Used by `c`.
func (v *Viewport) Recenter() {
	v.Offset = 0
}

// clamp keeps Offset within reasonable bounds. We don't have access
// to actual data length here so the bound is the row cap × 2;
// Apply() does final clamping against the actual asks/bids slices.
func (v *Viewport) clamp(rowCap int) {
	maxOffset := rowCap * 4 // generous; final clamp is in Apply()
	if v.Offset > maxOffset {
		v.Offset = maxOffset
	}
	if v.Offset < -maxOffset {
		v.Offset = -maxOffset
	}
}

// pageStep is half the visible viewport — the standard "page" feel
// (PgUp/PgDn keeps a row of context across the jump rather than a
// hard reset).
func pageStep(rowCap int) int {
	step := rowCap / 2
	if step < 4 {
		step = 4
	}
	return step
}

// Apply slices the asks / bids slices to the rows that should be
// visible at the current viewport offset, given the row cap.
//
// At offset = 0 (centred on spread) we show min(rowCap, len(asks))
// asks and min(rowCap, len(bids)) bids. Positive offset shifts the
// window up: more asks, fewer bids. Negative shifts down. Bounds
// are clamped so the user can't scroll past the end of the data
// on either side.
//
// Returns the trimmed slices in the same orientation they came in
// (asks ascending, bids descending) and the total visible rows
// per side after clamping.
func (v *Viewport) Apply(asks, bids []agg.AggregatedLevel, rowCap int) (visAsks, visBids []agg.AggregatedLevel) {
	askRows := rowCap + v.Offset
	bidRows := rowCap - v.Offset
	if askRows < 0 {
		askRows = 0
	}
	if bidRows < 0 {
		bidRows = 0
	}
	if askRows > len(asks) {
		askRows = len(asks)
	}
	if bidRows > len(bids) {
		bidRows = len(bids)
	}
	return asks[:askRows], bids[:bidRows]
}

// ─── row-cap math ──────────────────────────────────────────────────────────

// RowCap returns the per-side row budget for a given terminal
// height, accounting for the standard chrome (header, spread
// separator, footer, breathing). Renderers call this so the row
// budget calculation is consistent across surfaces.
func RowCap(termHeight int) int {
	// Real chrome budget for the legacy ws ladder + dashboard book
	// ladder: HeaderLine (1) + StatsLine (1) + blank (1) + table
	// header row (1) + spread separator inside the ladder (1) +
	// footer hint (1) + blank breathing (1) + safety (1) = 8.
	// Earlier versions used 5, which under-counted by 3 — at deep
	// depth tiers the rendered ladder pushed the StatsLine and
	// HeaderLine off the top of the alt-screen.
	const chrome = 8
	cap := (termHeight - chrome) / 2
	if cap < 4 {
		cap = 4
	}
	if cap > 60 {
		cap = 60 // safety cap on absurdly tall windows
	}
	return cap
}

// ─── tiny helpers ──────────────────────────────────────────────────────────

// floatString formats v with `prec` decimals and trims trailing
// zeros so 0.1000 renders as 0.1, not "0.1000". Used by
// GroupLabel to keep the header strip tidy across every bucket
// size in the cycle.
func floatString(v float64, prec int) string {
	// Quick-and-dirty: fmt.Sprintf with the prec, then trim.
	// Tested: matches what FormatBookPrice would give for the
	// price-column format.
	out := ""
	abs := v
	if abs < 0 {
		abs = -abs
	}
	whole := int64(abs)
	frac := abs - float64(whole)
	out = ""
	if v < 0 {
		out += "-"
	}
	out += int64ToString(whole)
	if prec > 0 {
		out += "."
		for i := 0; i < prec; i++ {
			frac *= 10
			d := int(frac)
			if d < 0 {
				d = 0
			} else if d > 9 {
				d = 9
			}
			out += string(rune('0' + d))
			frac -= float64(d)
		}
		// Trim trailing zeros.
		for len(out) > 0 && out[len(out)-1] == '0' {
			out = out[:len(out)-1]
		}
		if len(out) > 0 && out[len(out)-1] == '.' {
			out = out[:len(out)-1]
		}
	}
	return out
}

// int64ToString avoids importing strconv for a single call. ASCII
// digits, base 10.
func int64ToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ─── shared header line ────────────────────────────────────────────────────

// HeaderStyle bundles the ANSI escape strings the shared header line
// needs. We pass them in instead of importing internal/output so this
// package stays UI-agnostic (same pattern as keymap.HelpStyle). Each
// caller passes its surface's colour palette; the rendering math is
// identical across surfaces.
type HeaderStyle struct {
	Bold   string
	Accent string // surface accent — brand green by convention
	Grey   string // dimmed labels
	Warn   string // PAUSED / warning highlight
	Reset  string
}

// HeaderInfo is the data portion of the shared top header. Every
// book surface (legacy single-venue ladder, legacy scan, dashboard
// aggregated ladder) renders the same shape so users moving between
// them see one signal: surface name, what they're looking at, how
// many snapshots, current rate, paused state.
//
// Surface is the human-friendly label — "ladder", "scan",
// "aggregated ladder". Pair is the identity of the subscription:
// "binance:BTCUSDT" for single-venue, just "BTCUSDT" for the
// aggregated dashboard view (where multiple venues feed the same
// symbol).
type HeaderInfo struct {
	Surface  string
	Pair     string
	Updates  int64
	RatePerS float64
	Paused   bool
}

// HeaderLine renders the shared top-line header. Format:
//
//	▲ <surface>  <pair>  <N> snapshots  <rate>/s  [PAUSED]
//
// Returns the styled string ready to write to the renderer. PAUSED
// only appears when info.Paused is true; otherwise the field is
// omitted entirely (no empty placeholder — keeps live mode tidy).
//
// The triangle glyph and pluralisation match the pre-extraction
// legacy ladder header so users coming from `ws book` to `dash book`
// see the exact same top line.
func HeaderLine(info HeaderInfo, style HeaderStyle) string {
	plural := "s"
	if info.Updates == 1 {
		plural = ""
	}
	pausedTag := ""
	if info.Paused {
		pausedTag = "  " + style.Warn + "PAUSED" + style.Reset
	}
	pair := info.Pair
	if pair != "" {
		pair = "  " + style.Grey + pair + style.Reset
	}
	// Rate is shown with one decimal — anything finer is jitter on
	// a 100ms tick; anything coarser hides "is the feed alive?"
	// signal at low rates.
	return style.Bold + style.Accent + "▲ " + info.Surface + style.Reset +
		pair +
		"  " + int64ToString(info.Updates) + " snapshot" + plural +
		"  " + formatRate(info.RatePerS) + "/s" +
		pausedTag
}

// StatsInfo is the data portion of the shared second header line —
// the live metrics every ladder surface displays in the same shape:
//
//	MID <px>   SPREAD <px> (<bps> bps)|ARB +<px>   IMB<N> <pct>
//	BIDLIQ<N> <sz>   ASKLIQ<N> <sz>   DEPTH <N>   GROUP <label>
//
// Surfaces fill the fields they have. Single-venue ladders never
// have ARB > 0 (one book can't cross itself) — pass 0 and SPREAD
// renders. Multi-venue surfaces compute ARB from cross-venue best
// bid/ask and pass it when crossed; SPREAD is suppressed in favour
// of ARB so the header never reports a negative number.
//
// Mid, Spread, BidLiq, AskLiq, Imb, BpsSpread, ArbPx, DepthTier,
// GroupTick are all in their natural numeric units. The renderer
// formats them via output.* — passing pre-formatted strings would
// drag a chain of formatter dependencies into this package.
type StatsInfo struct {
	Mid       float64
	BpsSpread float64
	Spread    float64
	ArbPx     float64 // > 0 → renders ARB tag instead of SPREAD
	BidLiq    float64
	AskLiq    float64
	Imb       float64
	DepthTier int
	GroupTick float64

	// Sparkline is a pre-rendered micro-chart of recent microprice
	// ticks (~10 cells, ANSI-coloured). Empty string skips the
	// sparkline. Built outside this package via output.SparklineMicro
	// so the colour and glyph choices stay with the renderer.
	Sparkline string
}

// StatsFormatter abstracts the renderer's formatting helpers so this
// package doesn't import internal/output. Callers pass thin wrappers
// over output.FormatBookPrice / output.FormatBookSize / etc.
//
// Imbalance is its own callback because the colour treatment lives
// downstream — the shared header just delegates and lets the caller
// emit the right ANSI for its surface.
type StatsFormatter struct {
	Price     func(float64) string
	Size      func(float64) string
	Num       func(float64, int) string
	Imbalance func(float64) string
}

// StatsLine renders the shared second-header line. Every ladder
// surface (legacy single-venue, dashboard aggregated, future ones)
// calls this with its own StatsInfo + style + formatters; the
// vocabulary, ordering, and spacing are identical so a user moving
// between surfaces reads the same thing in the same place.
//
// One source of truth: tweaks to spacing, label casing, or field
// ordering happen here once.
func StatsLine(info StatsInfo, style HeaderStyle, fmtr StatsFormatter) string {
	var b []byte
	// MID
	b = append(b, style.Grey...)
	b = append(b, "MID "...)
	b = append(b, style.Reset...)
	b = append(b, fmtr.Price(info.Mid)...)
	// Optional sparkline of recent microprice — placed right after
	// the MID value so the eye reads "current price + recent shape"
	// as one unit. Caller pre-renders to a coloured glyph run; an
	// empty string leaves no gap.
	if info.Sparkline != "" {
		b = append(b, ' ')
		b = append(b, info.Sparkline...)
	}
	// SPREAD or ARB
	b = append(b, "  "...)
	if info.ArbPx > 0 {
		b = append(b, style.Grey...)
		b = append(b, "ARB "...)
		b = append(b, style.Reset...)
		b = append(b, style.Accent...)
		b = append(b, '+')
		b = append(b, fmtr.Price(info.ArbPx)...)
		b = append(b, style.Reset...)
	} else {
		b = append(b, style.Grey...)
		b = append(b, "SPREAD "...)
		b = append(b, style.Reset...)
		b = append(b, fmtr.Price(info.Spread)...)
		b = append(b, ' ')
		b = append(b, style.Grey...)
		b = append(b, '(')
		b = append(b, fmtr.Num(info.BpsSpread, 2)...)
		b = append(b, " bps)"...)
		b = append(b, style.Reset...)
	}
	// IMB<tier>
	b = append(b, "  "...)
	b = append(b, style.Grey...)
	b = append(b, "IMB"...)
	b = append(b, int64ToString(int64(info.DepthTier))...)
	b = append(b, ' ')
	b = append(b, style.Reset...)
	b = append(b, fmtr.Imbalance(info.Imb)...)
	// BIDLIQ<tier>
	b = append(b, "  "...)
	b = append(b, style.Grey...)
	b = append(b, "BIDLIQ"...)
	b = append(b, int64ToString(int64(info.DepthTier))...)
	b = append(b, ' ')
	b = append(b, style.Reset...)
	b = append(b, fmtr.Size(info.BidLiq)...)
	// ASKLIQ<tier>
	b = append(b, "  "...)
	b = append(b, style.Grey...)
	b = append(b, "ASKLIQ"...)
	b = append(b, int64ToString(int64(info.DepthTier))...)
	b = append(b, ' ')
	b = append(b, style.Reset...)
	b = append(b, fmtr.Size(info.AskLiq)...)
	// DEPTH
	b = append(b, "  "...)
	b = append(b, style.Grey...)
	b = append(b, "DEPTH "...)
	b = append(b, style.Reset...)
	b = append(b, int64ToString(int64(info.DepthTier))...)
	// GROUP
	b = append(b, "  "...)
	b = append(b, style.Grey...)
	b = append(b, "GROUP "...)
	b = append(b, style.Reset...)
	b = append(b, GroupLabel(info.GroupTick)...)
	return string(b)
}

// ─── microprice ring buffer ────────────────────────────────────────────────

// MicroRingSize is the fixed length of MicroRing's backing array —
// 60 ticks at the panel's ~10 Hz tick budget gives ~6 seconds of
// recent context, which lines up with the sparkline's ~10-cell
// footprint while still picking up sub-second moves. Tuned once
// here so every surface that calls Push/Values measures the same
// window of history.
const MicroRingSize = 60

// MicroRing is a fixed-capacity ring buffer of recent microprice
// samples. Oldest at Head, newest at Head-1. Two surfaces feed it:
// the legacy single-venue book ladder (one ring per pairKey) and
// the dashboard book panel (one ring for the consolidated mid).
// Both render the same sparkline in the same column of the shared
// StatsLine — so the buffer lives here rather than duplicated per
// renderer. Future ladder-style dashboards (perp screener, futures
// curve) get the sparkline path for free by embedding a MicroRing.
//
// Not safe for concurrent use; callers are expected to hold their
// own lock (the wsrender BookTable holds its mu, the BookPanel holds
// its mu).
type MicroRing struct {
	data [MicroRingSize]float64
	head int
	full bool
}

// Push appends one sample. NaN and non-positive values are dropped
// so a transient empty book or float artifact can't spike the
// sparkline. Returning early on bad input means callers don't have
// to gate every push site themselves.
func (r *MicroRing) Push(v float64) {
	if v <= 0 || v != v { // NaN guard
		return
	}
	r.data[r.head] = v
	r.head = (r.head + 1) % len(r.data)
	if r.head == 0 {
		r.full = true
	}
}

// Values returns the ring contents in oldest-to-newest order. Empty
// slice when nothing has been pushed yet — sparkline renderers
// handle that by drawing leading blanks.
func (r *MicroRing) Values() []float64 {
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

// formatRate renders a per-second rate with one decimal. Avoids
// importing fmt to keep the package's dependency surface minimal.
func formatRate(r float64) string {
	if r < 0 {
		r = 0
	}
	whole := int64(r)
	frac := int64((r - float64(whole)) * 10)
	if frac < 0 {
		frac = 0
	}
	return int64ToString(whole) + "." + string(rune('0'+frac))
}
