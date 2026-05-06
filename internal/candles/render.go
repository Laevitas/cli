package candles

// ASCII candle rendering. Pure: takes a slice of Candle plus width
// × height plus a few rendering options, returns the chart as a
// slice of strings (one per row, top-down). The zero-value renderer
// emits plain text; callers may opt into ANSI colour by providing
// colour escape strings in RenderOptions.
//
// Design:
//
//   - One column per candle, leftmost = oldest. If the visible
//     window holds fewer candles than columns, the chart is
//     left-padded with empty space; if more, the leftmost candles
//     are clipped (newest-rightmost wins).
//   - Y axis is price linearly scaled across the visible window's
//     [min Low, max High]. Right edge carries price labels.
//   - Gaps in the time series (non-contiguous BucketStarts) are
//     rendered as empty columns so the time axis stays accurate.
//     Caller supplies the canonical timeframe so we know the
//     expected stride.
//   - Up candles (Close >= Open): body glyph '█', wick '│'.
//     Down candles (Close < Open): body glyph '░', wick '│'.
//     Direction is encoded in the body glyph so colour-blind /
//     plain-text consumers can still tell them apart; the panel
//     layer adds red/green ANSI on top.
//
// Bounds:
//
//   - Render returns nil/empty rows for invalid sizes (width < 8 or
//     height < 3) — callers should detect and show a "chart too
//     small" placeholder rather than letting Render produce
//     nonsense.
//   - Empty candle slice returns the right number of empty-row
//     strings so the caller can join with newlines without special
//     casing.

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	// minRenderWidth is the smallest width below which Render
	// returns a blank chart. Below this we can't show meaningful
	// candle detail at any settings:
	//   - With labels on:   chart=width-priceLabelWidth=0, useless.
	//   - With labels off:  width<8 leaves <8 chart columns, useless.
	// Callers wanting a chart at narrower widths should use
	// HidePriceLabels and accept the floor of 8 cells. Below that
	// the panel layer should degrade to a single-line sparkline
	// (out of scope for this package).
	minRenderWidth = 8
	// minRenderHeight is the smallest row count below which the
	// y-axis resolution drops to ≤1 cell per price level — useless.
	minRenderHeight = 3
	// priceLabelWidth is reserved on the right edge for price
	// numbers. Eight visible cells fits a six-digit price plus a
	// space and a leading space. Callers wanting different label
	// widths should call with HidePriceLabels and render labels
	// themselves; threading a width through RenderOptions is
	// reserved for if/when a real caller needs it.
	priceLabelWidth = 8

	// Glyphs used by the renderer. Documented here so any future
	// alternate glyph set (e.g. half-width chars for narrow
	// terminals) lands in one place.
	glyphBodyUp   = '█'
	glyphBodyDown = '░'
	glyphWick     = '│'
	glyphEmpty    = ' '
	glyphFlat     = '─' // candle whose Open == Close at the resolution we can render
)

// RenderOptions controls non-data aspects of the chart. Zero value
// is acceptable (defaults: 1m timeframe, no labels suppressed).
type RenderOptions struct {
	// Timeframe is the expected stride between consecutive
	// candles, used to detect gaps. Defaults to 1m if zero.
	Timeframe time.Duration
	// HidePriceLabels suppresses the right-edge price labels.
	// Useful for very narrow charts where every column counts; the
	// panel may want to render labels separately or omit them.
	HidePriceLabels bool
	// CandleStride is the visible-cell width of one candle time
	// slot. The candle glyph is drawn in the leftmost cell of the
	// slot; the remaining cells are left blank as spacing. Defaults
	// to 2 (body + one gap), which matches the usual terminal
	// candlestick look. Set to 1 for dense one-cell-per-minute
	// rendering.
	CandleStride int
	// UpColor, DownColor, and FlatColor are optional ANSI SGR
	// sequences applied to candle glyphs. Reset is appended after
	// each coloured cell; if Reset is empty but any colour is set,
	// the standard SGR reset is used. Zero values preserve the
	// historical plain-text output.
	UpColor     string
	DownColor   string
	FlatColor   string
	Reset       string
	SolidBodies bool // use solid bodies for down candles too; intended for coloured renderers
}

// Render returns the chart as a slice of strings, one per row,
// top-down. Each row is exactly `width` visible cells wide. ANSI
// escapes are present only when colour options are supplied; rows
// never contain embedded newlines.
//
// `candles` must be sorted oldest-first by BucketStart. The
// canonical Aggregator output (Candles(), Downsample()) satisfies
// this. Unsorted input would produce nonsensical gap counts and
// reversed price-direction inference; we don't sort defensively
// because every realistic caller already has a sorted source.
//
// The returned slice has exactly `height` entries. Empty/invalid
// inputs return a slice of `height` empty-padded strings rather
// than nil so callers can always strings.Join without checking.
func Render(candles []Candle, width, height int, opts RenderOptions) []string {
	if width < minRenderWidth || height < minRenderHeight {
		return blankRows(width, height)
	}
	if opts.Timeframe <= 0 {
		opts.Timeframe = time.Minute
	}
	if opts.CandleStride <= 0 {
		opts.CandleStride = 2
	}

	// Reserve space for price labels on the right edge unless
	// suppressed. Chart columns are width minus the label gutter.
	labelW := priceLabelWidth
	if opts.HidePriceLabels {
		labelW = 0
	}
	chartW := width - labelW
	if chartW < 4 {
		// Width allotted to chart is too small once we deduct
		// labels. Caller asked for an impossible layout — return
		// blank rather than rendering garbage.
		return blankRows(width, height)
	}

	// Walk the candle series and emit one column per slot. Slots
	// include explicit gap columns so non-contiguous BucketStarts
	// don't compress visually.
	cols := candleColumns(candles, chartW, opts.Timeframe, opts.CandleStride)
	if len(cols) == 0 {
		return blankRows(width, height)
	}

	// Establish the price scale across visible columns only — using
	// every candle in `candles` would compress the visible window
	// when older candles had wildly different ranges.
	lo, hi := priceRange(cols)
	if hi <= lo {
		// Degenerate: all visible candles at the same price (or no
		// data). Render a single horizontal line at the middle row
		// for visual continuity, with the price label.
		return flatRow(cols, lo, width, height, labelW, opts)
	}

	// Build the 2D rune grid. rows[0] is the top of the chart
	// (highest price); rows[height-1] is the bottom (lowest).
	grid := make([][]rune, height)
	colors := make([][]string, height)
	for i := range grid {
		grid[i] = make([]rune, chartW)
		colors[i] = make([]string, chartW)
		for j := range grid[i] {
			grid[i][j] = glyphEmpty
		}
	}

	for col, c := range cols {
		if col >= chartW {
			break
		}
		if c == nil {
			continue // gap column; leave blank
		}
		drawCandle(grid, colors, col, *c, lo, hi, height, opts)
	}

	// Compose final rows: chart cells, then a label gutter on the
	// right showing prices at top, mid, bottom.
	out := make([]string, height)
	for i := 0; i < height; i++ {
		var b strings.Builder
		b.Grow(width)
		for col, ch := range grid[i] {
			if colors[i][col] != "" && ch != glyphEmpty {
				b.WriteString(colors[i][col])
				b.WriteRune(ch)
				b.WriteString(renderReset(opts))
				continue
			}
			b.WriteRune(ch)
		}
		if labelW > 0 {
			b.WriteString(priceLabelForRow(i, height, lo, hi, labelW))
		}
		out[i] = b.String()
	}
	return out
}

// candleColumns produces a per-column slice of *Candle entries,
// inserting nil placeholders for gaps. The result is right-aligned
// to width: if there are more candles (or candle+gap slots) than
// columns, the leftmost (oldest) ones are clipped; if fewer, the
// right side has the candles and the left is left-padded with nil.
//
// timeframe is used to detect gaps: any pair of consecutive
// BucketStarts that differ by more than one stride is treated as
// having (delta/stride - 1) gap columns between them.
//
// Iteration is right-to-left, capped at `width` slots emitted, so a
// sparse seed with two candles far apart never allocates gap entries
// the renderer would clip away. With seed candles 1 day apart at 1m
// timeframe (1440 gap slots), the old left-to-right approach would
// allocate ~1440 *Candle pointers before clipping; the right-to-left
// path stops as soon as it has filled `width` slots.
//
// `candles` is expected to be sorted oldest-first by BucketStart;
// the canonical Aggregator output is. Unsorted input would produce
// nonsensical gap counts.
func candleColumns(candles []Candle, width int, timeframe time.Duration, candleStride int) []*Candle {
	if len(candles) == 0 || width <= 0 {
		return nil
	}
	if candleStride <= 0 {
		candleStride = 2
	}

	// Walk newest → oldest, accumulating into out (which we'll
	// reverse at the end). Stop as soon as we've filled `width`
	// slots — older candles and any gaps before them aren't going to
	// be visible.
	rev := make([]*Candle, 0, width)
	for i := len(candles) - 1; i >= 0 && len(rev) < width; i-- {
		// Emit this candle's slot right-to-left: gap cells first,
		// then the body cell. After reversing below, the body sits
		// at the left edge of its slot and the remaining cells form
		// visible spacing before the next candle.
		for s := 1; s < candleStride && len(rev) < width; s++ {
			rev = append(rev, nil)
		}
		if len(rev) >= width {
			break
		}
		// Capture the pointer to the slice element. Safe because
		// `candles` is the caller's snapshot; we don't mutate.
		rev = append(rev, &candles[i])
		if i == 0 {
			break
		}
		// Compute the gap between this candle and the previous one
		// (older). If the previous candle is more than one stride
		// behind, emit gap nils between them — but only as many as
		// we still have room for.
		delta := candles[i].BucketStart.Sub(candles[i-1].BucketStart)
		gaps := int(delta/timeframe) - 1
		if gaps < 0 {
			gaps = 0
		}
		gaps *= candleStride
		// Only emit up to the remaining width.
		room := width - len(rev)
		if gaps > room {
			gaps = room
		}
		for g := 0; g < gaps; g++ {
			rev = append(rev, nil)
		}
	}

	// Reverse to get oldest → newest left-to-right.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}

	// Right-anchor: when there are fewer candles than visible
	// columns, pad the LEFT with nils so the newest candle pins
	// to the right edge of the chart. This is the legacy chart
	// convention (newest-rightmost) — the chart "scrolls" left
	// as new candles arrive, which is what every trading UI
	// does.
	//
	// We deliberately do NOT stretch fewer-than-width candles
	// across the pane. An earlier attempt at that (repeating
	// the same Candle pointer N times) caused drawCandle to
	// paint the same wick/body in N adjacent columns, producing
	// a solid grey rectangle instead of a chart. Honest visual:
	// 2 candles in a 100-cell pane reads as two thin bars at
	// the right with empty space to the left — that's correct;
	// the chart just hasn't accumulated history yet.
	if len(rev) < width {
		padded := make([]*Candle, width)
		copy(padded[width-len(rev):], rev)
		return padded
	}
	return rev
}

// priceRange scans the visible columns and returns the (low, high)
// envelope of candle High/Low values. Skips nil (gap) columns.
func priceRange(cols []*Candle) (lo, hi float64) {
	lo = math.Inf(1)
	hi = math.Inf(-1)
	for _, c := range cols {
		if c == nil {
			continue
		}
		if c.Low < lo {
			lo = c.Low
		}
		if c.High > hi {
			hi = c.High
		}
	}
	if math.IsInf(lo, 1) {
		// No real data in any column.
		return 0, 0
	}
	return lo, hi
}

// drawCandle plots one candle into the grid at column `col`. The
// price scale is linear across [lo, hi] mapped to [height-1, 0]
// (top of grid is highest price). Wick covers high→low; body
// covers open→close. Direction-encoded glyphs.
func drawCandle(grid [][]rune, colors [][]string, col int, c Candle, lo, hi float64, height int, opts RenderOptions) {
	// Map prices to row indices. Row 0 is the top of the grid
	// (highest price); row height-1 is the bottom.
	priceToRow := func(p float64) int {
		if hi == lo {
			return height / 2
		}
		// Linear interpolation, clamped.
		frac := (hi - p) / (hi - lo)
		row := int(math.Round(frac * float64(height-1)))
		if row < 0 {
			row = 0
		}
		if row > height-1 {
			row = height - 1
		}
		return row
	}

	rowHigh := priceToRow(c.High)
	rowLow := priceToRow(c.Low)
	rowOpen := priceToRow(c.Open)
	rowClose := priceToRow(c.Close)

	// Wick: vertical line from high to low. Drawn first so the body
	// overdraws it where they overlap.
	color := candleColor(c, opts)
	for r := rowHigh; r <= rowLow; r++ {
		grid[r][col] = glyphWick
		colors[r][col] = color
	}

	// Body: from open row to close row. If open and close land on
	// the same row at our resolution, draw a flat marker.
	bodyTop, bodyBot := rowOpen, rowClose
	if bodyTop > bodyBot {
		bodyTop, bodyBot = bodyBot, bodyTop
	}
	if bodyTop == bodyBot {
		grid[bodyTop][col] = glyphFlat
		colors[bodyTop][col] = candleColor(Candle{Open: c.Open, Close: c.Open}, opts)
		return
	}
	bodyGlyph := glyphBodyDown
	if c.Close >= c.Open {
		bodyGlyph = glyphBodyUp
	} else if opts.SolidBodies {
		bodyGlyph = glyphBodyUp
	}
	for r := bodyTop; r <= bodyBot; r++ {
		grid[r][col] = bodyGlyph
		colors[r][col] = color
	}
}

func candleColor(c Candle, opts RenderOptions) string {
	if c.Close > c.Open {
		return opts.UpColor
	}
	if c.Close < c.Open {
		return opts.DownColor
	}
	if opts.FlatColor != "" {
		return opts.FlatColor
	}
	return opts.UpColor
}

func renderReset(opts RenderOptions) string {
	if opts.Reset != "" {
		return opts.Reset
	}
	if opts.UpColor != "" || opts.DownColor != "" || opts.FlatColor != "" {
		return "\033[0m"
	}
	return ""
}

// priceLabelForRow returns the right-edge label for grid row `i`.
// Labels are emitted at top, middle, and bottom only — putting one
// on every row would create dense vertical clutter, and three is
// enough to read the price scale at a glance.
func priceLabelForRow(rowIdx, height int, lo, hi float64, labelW int) string {
	mid := height / 2
	switch rowIdx {
	case 0:
		return rightAlignLabel(formatPrice(hi), labelW)
	case mid:
		return rightAlignLabel(formatPrice((hi+lo)/2), labelW)
	case height - 1:
		return rightAlignLabel(formatPrice(lo), labelW)
	default:
		return strings.Repeat(" ", labelW)
	}
}

// formatPrice renders a price value into a compact string. Integers
// are emitted without decimals; non-integers get up to two
// decimals. Numbers wider than the label gutter get truncated to
// scientific-style abbreviation (e.g. 78.5K) rather than overflowing.
func formatPrice(p float64) string {
	if math.IsNaN(p) || math.IsInf(p, 0) {
		return "—"
	}
	abs := math.Abs(p)
	switch {
	case abs == 0:
		return "0"
	case abs >= 1_000_000:
		return fmt.Sprintf("%.2fM", p/1_000_000)
	case abs >= 100_000:
		return fmt.Sprintf("%.1fK", p/1_000)
	case abs >= 10_000:
		// Plain integer display — six chars for prices like 78465.
		return fmt.Sprintf("%.0f", p)
	case abs >= 1:
		return fmt.Sprintf("%.2f", p)
	default:
		return fmt.Sprintf("%.4f", p)
	}
}

// rightAlignLabel pads (or truncates) `s` so it occupies exactly
// `width` cells, right-aligned, with a leading space gutter to
// separate from chart content.
func rightAlignLabel(s string, width int) string {
	if width <= 0 {
		return ""
	}
	// Reserve one cell for visual separation from the chart.
	gutter := 1
	contentW := width - gutter
	if contentW <= 0 {
		return strings.Repeat(" ", width)
	}
	if len(s) > contentW {
		s = s[:contentW]
	}
	pad := contentW - len(s)
	return strings.Repeat(" ", gutter) + strings.Repeat(" ", pad) + s
}

// blankRows returns `height` strings of `width` spaces. Used as the
// "graceful blank" return shape for invalid sizes / empty data.
func blankRows(width, height int) []string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	out := make([]string, height)
	row := strings.Repeat(" ", width)
	for i := range out {
		out[i] = row
	}
	return out
}

// flatRow handles the degenerate case where all visible candles
// share a single price (or there's effectively no price range).
// Renders a horizontal line at the middle row, with the (single)
// price labelled.
func flatRow(cols []*Candle, price float64, width, height, labelW int, opts RenderOptions) []string {
	out := make([]string, height)
	chartW := width - labelW
	mid := height / 2
	color := opts.FlatColor
	if color == "" {
		color = opts.UpColor
	}
	reset := renderReset(opts)
	for i := range out {
		var b strings.Builder
		b.Grow(width)
		if i == mid {
			row := make([]rune, chartW)
			for j := range row {
				row[j] = glyphEmpty
			}
			for j, c := range cols {
				if j >= chartW {
					break
				}
				if c != nil {
					row[j] = glyphFlat
				}
			}
			for _, ch := range row {
				if color != "" && ch != glyphEmpty {
					b.WriteString(color)
					b.WriteRune(ch)
					b.WriteString(reset)
				} else {
					b.WriteRune(ch)
				}
			}
		} else {
			b.WriteString(strings.Repeat(" ", chartW))
		}
		if labelW > 0 {
			if i == mid {
				b.WriteString(rightAlignLabel(formatPrice(price), labelW))
			} else {
				b.WriteString(strings.Repeat(" ", labelW))
			}
		}
		out[i] = b.String()
	}
	return out
}
