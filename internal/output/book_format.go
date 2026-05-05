package output

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Book-specific formatting helpers, shared by every renderer that
// shows L2 order-book data: the rolling-tape book renderer
// (internal/wsrender), the dashboard book panel
// (internal/dashboard/panels), and any future view that needs to
// format prices, sizes, depth bars, or microprice sparklines.
//
// Lives in internal/output because it's all about how to render
// these values; the underlying types are in internal/api/book.go.
//
// Every helper here is a pure function — no shared state, no
// goroutines — so it's safe to call from any Bubble Tea View().

// ─── number formatting ─────────────────────────────────────────────────────

// FormatBookPrice renders a price with venue-tick-respecting
// precision: 2 decimals for prices ≥ 100, 4 for ≥ 1, 6 for ≥ 0.0001,
// 8 for tiny prices. Always uses the brand thousand separator.
//
// Returns "-" for zero so empty cells read clearly. Any non-zero
// negative price (rare but possible on funding-rate-style metrics)
// is formatted with the same rules.
func FormatBookPrice(v float64) string {
	if v == 0 {
		return "-"
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

// FormatSpread renders a price-difference (best-ask − best-bid) at
// the same precision as FormatBookPrice would use, then strips
// trailing zeros so a clean tick like 0.10 doesn't render as
// 0.100000. Used by the screener's SPREAD column and the book
// pane's mid-price separator — both are derivative quantities
// where trailing zeros are noise, not column-alignment glue.
//
// Differs from FormatBookPrice in two ways:
//   - Trailing zeros stripped after the decimal point.
//   - Trailing decimal point stripped if the result reduces to a
//     whole number (e.g. spread of 1.0 → "1", not "1." nor "1.0").
//
// Don't use this for ladder rows — those need fixed-width decimals
// for column alignment, which is what FormatBookPrice is for.
func FormatSpread(v float64) string {
	if v == 0 {
		return "-"
	}
	formatted := FormatBookPrice(v)
	// FormatBookPrice's output is "<int part>.<dec part>" with the
	// brand thousand separator in the int part. Strip the trailing
	// zeros + dangling decimal point. We only touch the part after
	// the LAST '.' so a number like "1,234.5000" trims to
	// "1,234.5" without touching the integer side.
	dot := strings.LastIndexByte(formatted, '.')
	if dot < 0 {
		return formatted
	}
	formatted = strings.TrimRight(formatted, "0")
	if strings.HasSuffix(formatted, ".") {
		formatted = formatted[:len(formatted)-1]
	}
	return formatted
}

// FormatBookSize renders a size or cumulative liquidity with 5
// significant digits — enough for tick-precise sizes on every venue
// we support, while dodging the IEEE noise that creeps into
// cumulative sums (e.g. 12.001000000000001).
//
// Values below 0.001 fall back to FormatNumFull (which uses scientific-
// adjacent precision); 5 sig figs on 1e-7 just looks like zero.
func FormatBookSize(v float64) string {
	if v == 0 {
		return "-"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs < 1e-3 {
		return FormatNumFull(v)
	}
	dec := 5
	mag := abs
	for mag >= 10 && dec > 0 {
		mag /= 10
		dec--
	}
	return formatNum(v, dec)
}

// FormatNumFull renders a float preserving as much API precision as
// the wire payload carries, while stripping the trailing-9s and
// trailing-0s artifacts that appear when a server-side computation
// reconstructs a decimal value through float64. Used by tape rows,
// liquidation amounts, and anywhere precision matters more than
// width.
//
// Adds thousand separators for any integer-magnitude part. Returns
// "-" for zero.
func FormatNumFull(v float64) string {
	if v == 0 {
		return "-"
	}
	raw := strconv.FormatFloat(v, 'f', -1, 64)
	if isFloatArtifact(raw) {
		rounded := strconv.FormatFloat(v, 'f', 8, 64)
		rounded = trimTrailingZeros(rounded)
		raw = rounded
	}

	negative := false
	if strings.HasPrefix(raw, "-") {
		negative = true
		raw = raw[1:]
	}
	intPart, fracPart, hasFrac := strings.Cut(raw, ".")
	intPart = addThousandSeparators(intPart)
	out := intPart
	if hasFrac {
		out = out + "." + fracPart
	}
	if negative {
		out = "-" + out
	}
	return out
}

// formatNum is the lower-level "exactly N decimals, with separators"
// formatter. Used internally by FormatBookPrice and FormatBookSize;
// exported externally as FormatNum for callers who want a fixed
// precision (e.g. spread bps with 2 decimals).
func formatNum(v float64, decimals int) string {
	if decimals < 0 {
		decimals = 0
	}
	negative := false
	if v < 0 {
		negative = true
		v = -v
	}
	str := strconv.FormatFloat(v, 'f', decimals, 64)
	intPart, fracPart, hasFrac := strings.Cut(str, ".")
	intPart = addThousandSeparators(intPart)
	out := intPart
	if hasFrac {
		out = out + "." + fracPart
	}
	if negative {
		out = "-" + out
	}
	return out
}

// FormatNum is the exported alias for the fixed-decimal formatter
// — kept lowercase internally so external callers always see the
// thousand-separated form.
func FormatNum(v float64, decimals int) string { return formatNum(v, decimals) }

// addThousandSeparators inserts comma separators into the integer
// portion of a numeric string. Operates on bytes (ASCII digits only)
// for speed; the caller passes a sign-stripped, decimal-stripped
// digit run.
func addThousandSeparators(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	first := n % 3
	if first == 0 {
		first = 3
	}
	out := make([]byte, 0, n+(n-1)/3)
	out = append(out, s[:first]...)
	for i := first; i < n; i += 3 {
		out = append(out, ',')
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}

// isFloatArtifact detects the trailing "999999" or "000000" runs
// that appear when a float64 round-trips through a server-side
// decimal. The heuristic only fires on strings with at least 12
// fractional digits — anything shorter is unlikely to be artifact.
func isFloatArtifact(raw string) bool {
	idx := strings.IndexByte(raw, '.')
	if idx < 0 {
		return false
	}
	decimals := raw[idx+1:]
	if len(decimals) < 12 {
		return false
	}
	tail := decimals
	if len(tail) > 6 {
		tail = tail[len(tail)-6:]
	}
	return strings.Contains(tail, "999999") || strings.Contains(tail, "000000")
}

func trimTrailingZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// ─── horizontal bar charts (book depth visualisation) ──────────────────────

// BarLength returns the number of cells filled, log-scaled. Linear
// scaling flattens every small level when one big level exists (e.g.
// one 5 BTC quote next to dozens of 0.001 BTC quotes — the small
// ones round to 0 and read as "no liquidity," which is wrong).
//
// We use log1p so size 0 → 0 cells, size > 0 → at least 1 cell, and
// the curve still distinguishes "tiny" from "huge" without being
// dominated by the largest. ceil(1) for any positive size guarantees
// a visible mark when there's any liquidity at all.
func BarLength(size, maxSize float64, width int) int {
	if maxSize <= 0 || size <= 0 {
		return 0
	}
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

// BarRight renders a horizontal bar that grows toward the right
// edge of its cell — used for the ask side, so the bar starts
// adjacent to the centre PRICE column and extends outward to the
// right.
//
// color is the ANSI escape applied to the filled cells; reset is
// emitted automatically. Empty cells are rendered as plain spaces
// so terminals with weird background colour treatments don't show
// stray fill in the empty portion.
func BarRight(size, maxSize float64, width int, color string) string {
	filled := BarLength(size, maxSize, width)
	return color + strings.Repeat("▮", filled) + Reset + strings.Repeat(" ", width-filled)
}

// BarLeft renders a horizontal bar that grows toward the left edge
// — used for the bid side, so the bar starts at the right (adjacent
// to PRICE) and extends leftward.
func BarLeft(size, maxSize float64, width int, color string) string {
	filled := BarLength(size, maxSize, width)
	return strings.Repeat(" ", width-filled) + color + strings.Repeat("▮", filled) + Reset
}

// SegmentedBarRight is the multi-venue variant of BarRight: the
// bar is split into per-venue colour segments proportional to each
// venue's contribution at that price level. Used by the aggregated
// ladder dashboard so the eye reads "this wall is mostly binance"
// or "this wall is even between bybit and okx" at a glance.
//
// segments must sum to (size). Each segment carries its own colour;
// rendering walks them left-to-right at the right edge. If the
// total bar length doesn't divide evenly across segments, the
// remaining cells are absorbed into the largest segment.
type BarSegment struct {
	Size  float64
	Color string
}

func SegmentedBarRight(segments []BarSegment, maxSize float64, width int) string {
	totalSize := 0.0
	for _, s := range segments {
		totalSize += s.Size
	}
	if totalSize <= 0 {
		return strings.Repeat(" ", width)
	}
	totalCells := BarLength(totalSize, maxSize, width)
	if totalCells == 0 {
		return strings.Repeat(" ", width)
	}

	// Distribute cells proportionally; round to ints; absorb
	// rounding remainder into the largest segment so the bar
	// length matches totalCells exactly.
	cells := make([]int, len(segments))
	allocated := 0
	largest := 0
	for i, s := range segments {
		c := int(math.Round((s.Size / totalSize) * float64(totalCells)))
		cells[i] = c
		allocated += c
		if s.Size > segments[largest].Size {
			largest = i
		}
	}
	cells[largest] += totalCells - allocated

	var b strings.Builder
	for i, c := range cells {
		if c <= 0 {
			continue
		}
		b.WriteString(segments[i].Color)
		b.WriteString(strings.Repeat("▮", c))
		b.WriteString(Reset)
	}
	pad := width - totalCells
	if pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	return b.String()
}

// SegmentedBarLeft mirrors SegmentedBarRight for the bid side: the
// bar grows leftward, so the leading pad goes first and segments
// render in the same left-to-right order (largest-contributor first
// is conventional but the caller controls ordering).
func SegmentedBarLeft(segments []BarSegment, maxSize float64, width int) string {
	totalSize := 0.0
	for _, s := range segments {
		totalSize += s.Size
	}
	if totalSize <= 0 {
		return strings.Repeat(" ", width)
	}
	totalCells := BarLength(totalSize, maxSize, width)
	if totalCells == 0 {
		return strings.Repeat(" ", width)
	}

	cells := make([]int, len(segments))
	allocated := 0
	largest := 0
	for i, s := range segments {
		c := int(math.Round((s.Size / totalSize) * float64(totalCells)))
		cells[i] = c
		allocated += c
		if s.Size > segments[largest].Size {
			largest = i
		}
	}
	cells[largest] += totalCells - allocated

	var b strings.Builder
	pad := width - totalCells
	if pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	for i, c := range cells {
		if c <= 0 {
			continue
		}
		b.WriteString(segments[i].Color)
		b.WriteString(strings.Repeat("▮", c))
		b.WriteString(Reset)
	}
	return b.String()
}

// ─── styling helpers ───────────────────────────────────────────────────────

// ColorImbalance maps a -1..1 imbalance ratio to a green/red
// percentage badge. Positive (more bid liquidity) → BrandGreen,
// negative (more ask liquidity) → Red. Caller passes the raw imb
// value; this helper handles formatting and sign.
func ColorImbalance(imb float64) string {
	pct := imb * 100
	sign := "+"
	color := BrandGreen
	if imb < 0 {
		sign = ""
		color = Red
	}
	return color + fmt.Sprintf("%s%.1f%%", sign, pct) + Reset
}

// StyleLevelPrice renders a price string with its side colour,
// prefixed with a direction glyph when the level recently changed:
// `↑` if the level grew (liquidity arrived — usually a market-maker
// stacking), `↓` if it shrank or vanished (eaten by an aggressor or
// pulled).
//
// dir == 0 means "no recent change" — render plain (with two
// leading spaces so the price column stays vertically aligned
// regardless of glyph presence). baseColor is the side colour
// (Red for asks, BrandGreen for bids); the glyph itself is
// coloured by direction so it reads independent of which side
// it's on.
func StyleLevelPrice(price, baseColor string, dir int) string {
	switch dir {
	case +1:
		return BrandGreen + "↑" + Reset + " " + baseColor + price + Reset
	case -1:
		return Red + "↓" + Reset + " " + baseColor + price + Reset
	default:
		return "  " + baseColor + price + Reset
	}
}

// StyleLevelSize renders a size cell, prefixing a "▲" marker and
// bumping to bold when the level qualifies as a "whale" (≥30% of
// its side's cumulative tier liquidity). The marker is rendered
// without colour so it reads against any side — the bar already
// carries the side colour.
func StyleLevelSize(size string, whale bool) string {
	if whale {
		return Bold + "▲ " + size + Reset
	}
	return size
}

// SparklineMicro renders a 1-line unicode-block sparkline of recent
// microprice ticks. Empty input → empty string (so the header strip
// doesn't get an awkward "▁▁▁▁" before any data has arrived). Width
// renders as min(8, len(values)) — short enough to live next to
// MID without dominating the strip.
//
// Colour by net direction over the window — green if up, red if
// down, grey if flat. Gives an instant "drifting up vs down" cue
// without having to read the line.
func SparklineMicro(values []float64) string {
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
	blocks := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	color := BrandGreyMid
	if v[len(v)-1] > v[0] {
		color = BrandGreen
	} else if v[len(v)-1] < v[0] {
		color = Red
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
	b.WriteString(Reset)
	return b.String()
}

// ColorPositionSide tags a row by which side of the book got
// liquidated (long vs short). Re-exported here so non-rendering
// packages (e.g. a future analytics command) can colour-code their
// output the same way the live tape does.
func ColorPositionSide(side string) string {
	switch lower(side) {
	case "long":
		return Red + "LONG " + Reset
	case "short":
		return BrandGreen + "SHORT" + Reset
	default:
		return BrandGreyMid + side + Reset
	}
}
