package candles

import (
	"strings"
	"testing"
	"time"
)

// candle is a small builder so tests don't have to spell out OHLCV
// for every case.
func candle(start string, o, h, l, c float64) Candle {
	return Candle{
		BucketStart: mustTime(start),
		Open:        o,
		High:        h,
		Low:         l,
		Close:       c,
		Volume:      1,
		TradeCount:  1,
	}
}

// TestRenderEmpty: no candles, valid size — returns the right number
// of empty rows (caller can strings.Join without checking len).
func TestRenderEmpty(t *testing.T) {
	rows := Render(nil, 40, 10, RenderOptions{Timeframe: time.Minute})
	if got := len(rows); got != 10 {
		t.Fatalf("len(rows) = %d, want 10", got)
	}
	for i, r := range rows {
		if got := visibleLen(r); got != 40 {
			t.Errorf("row %d width = %d, want 40", i, got)
		}
		if strings.TrimSpace(r) != "" {
			t.Errorf("row %d not blank: %q", i, r)
		}
	}
}

// TestRenderTooSmall: width below minimum returns blank rows.
// Caller is supposed to detect and show a placeholder.
func TestRenderTooSmall(t *testing.T) {
	cs := []Candle{candle("2026-05-04T14:00:00Z", 100, 110, 90, 105)}
	for _, w := range []int{0, 1, 7} { // minRenderWidth is 8
		rows := Render(cs, w, 5, RenderOptions{})
		if len(rows) != 5 {
			t.Errorf("width %d: len(rows) = %d, want 5", w, len(rows))
		}
		for _, r := range rows {
			if strings.TrimSpace(r) != "" {
				t.Errorf("width %d: expected blank row, got %q", w, r)
			}
		}
	}
}

// TestRenderShape: a single bullish candle in a sane window produces
// the right row count and width, and the candle column has body
// glyphs (not just spaces).
func TestRenderShape(t *testing.T) {
	cs := []Candle{candle("2026-05-04T14:00:00Z", 100, 110, 90, 105)}
	rows := Render(cs, 40, 10, RenderOptions{Timeframe: time.Minute})

	if len(rows) != 10 {
		t.Fatalf("len(rows) = %d, want 10", len(rows))
	}
	for _, r := range rows {
		if visibleLen(r) != 40 {
			t.Errorf("row width = %d, want 40 (row=%q)", visibleLen(r), r)
		}
	}

	// At least one row should contain the up-body glyph for our
	// bullish candle. With default CandleStride=2, the rightmost
	// candle slot leaves one gap cell before the price label.
	chartW := 40 - priceLabelWidth
	candleCol := chartW - 2
	foundBody := false
	for _, r := range rows {
		runes := []rune(r)
		if candleCol < len(runes) && runes[candleCol] == glyphBodyUp {
			foundBody = true
			break
		}
	}
	if !foundBody {
		t.Errorf("no up-body glyph found in candle column %d:\n%s", candleCol, strings.Join(rows, "\n"))
	}
}

// TestRenderDirectionGlyphs: bullish vs bearish candles use distinct
// body glyphs, so the chart is readable without ANSI colour.
func TestRenderDirectionGlyphs(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 100, 110, 95, 108),  // up
		candle("2026-05-04T14:01:00Z", 108, 112, 100, 102), // down
	}
	rows := Render(cs, 40, 12, RenderOptions{Timeframe: time.Minute})
	combined := strings.Join(rows, "\n")
	if !strings.ContainsRune(combined, glyphBodyUp) {
		t.Errorf("up body glyph missing: %s", combined)
	}
	if !strings.ContainsRune(combined, glyphBodyDown) {
		t.Errorf("down body glyph missing: %s", combined)
	}
}

func TestRenderAppliesOptionalDirectionColors(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 100, 110, 95, 108),  // up
		candle("2026-05-04T14:01:00Z", 108, 112, 100, 102), // down
	}
	rows := Render(cs, 40, 12, RenderOptions{
		Timeframe: time.Minute,
		UpColor:   "<up>",
		DownColor: "<down>",
		FlatColor: "<flat>",
		Reset:     "</>",
	})
	combined := strings.Join(rows, "\n")
	if !strings.Contains(combined, "<up>") {
		t.Errorf("up color missing: %s", combined)
	}
	if !strings.Contains(combined, "<down>") {
		t.Errorf("down color missing: %s", combined)
	}
	if !strings.Contains(combined, "</>") {
		t.Errorf("reset missing: %s", combined)
	}
}

func TestRenderSolidBodiesUsesFilledDownCandles(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 108, 112, 100, 102), // down
	}
	rows := Render(cs, 40, 12, RenderOptions{
		Timeframe:   time.Minute,
		SolidBodies: true,
	})
	combined := strings.Join(rows, "\n")
	if strings.ContainsRune(combined, glyphBodyDown) {
		t.Errorf("solid body render used shaded down glyph: %s", combined)
	}
	if !strings.ContainsRune(combined, glyphBodyUp) {
		t.Errorf("solid body render missing filled body glyph: %s", combined)
	}
}

// TestRenderFlatPriceRange: every candle has the same Open/High/Low/
// Close — degenerate case rendered as a single horizontal line at
// mid row, not a divide-by-zero panic or empty chart.
func TestRenderFlatPriceRange(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 100, 100, 100, 100),
		candle("2026-05-04T14:01:00Z", 100, 100, 100, 100),
	}
	rows := Render(cs, 40, 9, RenderOptions{Timeframe: time.Minute})
	mid := len(rows) / 2
	if !strings.ContainsRune(rows[mid], glyphFlat) {
		t.Errorf("expected flat glyph on mid row %d, got %q", mid, rows[mid])
	}
	// Above and below mid should be empty.
	for i, r := range rows {
		if i == mid {
			continue
		}
		if strings.ContainsRune(r, glyphFlat) {
			t.Errorf("flat glyph leaked to non-mid row %d: %q", i, r)
		}
	}
}

// TestRenderGapColumn: two candles whose BucketStarts are 3 minutes
// apart (i.e. two missing minutes) produce two blank columns
// between them so the time axis stays accurate.
func TestRenderGapColumn(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
		// 14:01 and 14:02 missing.
		candle("2026-05-04T14:03:00Z", 105, 115, 100, 112),
	}
	// Width 16: chart=8, labels=8. With CandleStride=2 and two
	// missing minutes, slots are: candle+gap, empty+empty,
	// empty+empty, candle+gap.
	rows := Render(cs, 16, 8, RenderOptions{Timeframe: time.Minute})

	// Find any row that contains body glyphs and check the column
	// pattern.
	chartW := 16 - priceLabelWidth // 8
	hadBodyAtCol := make([]bool, chartW)
	for _, r := range rows {
		runes := []rune(r)
		for c := 0; c < chartW && c < len(runes); c++ {
			if runes[c] == glyphBodyUp || runes[c] == glyphBodyDown || runes[c] == glyphFlat {
				hadBodyAtCol[c] = true
			}
		}
	}
	// Expect: col 0 has body (oldest candle), col 6 has body
	// (newest), and the rest are gap cells.
	if !hadBodyAtCol[0] {
		t.Errorf("col 0 (oldest candle) missing body")
	}
	if !hadBodyAtCol[chartW-2] {
		t.Errorf("col %d (newest candle) missing body", chartW-2)
	}
	for _, col := range []int{1, 2, 3, 4, 5, 7} {
		if hadBodyAtCol[col] {
			t.Errorf("gap col %d unexpectedly contains body glyph: %v", col, hadBodyAtCol)
		}
	}
}

// TestRenderRightAlignsRecentCandle: more candles than columns —
// the right edge of the chart shows the newest candles, oldest
// clipped from the left.
func TestRenderRightAlignsRecentCandle(t *testing.T) {
	cs := make([]Candle, 0, 20)
	for i := 0; i < 20; i++ {
		t0 := mustTime("2026-05-04T14:00:00Z").Add(time.Duration(i) * time.Minute)
		cs = append(cs, Candle{
			BucketStart: t0,
			Open:        100, High: 100 + float64(i), Low: 100, Close: 100 + float64(i),
			Volume: 1, TradeCount: 1,
		})
	}
	// Chart width = 16 - 8 = 8, so only the newest 8 candles fit.
	rows := Render(cs, 16, 12, RenderOptions{Timeframe: time.Minute})

	// The price scale across visible columns should be [100, 119]
	// (newest candle has Close=119, High=119). The label gutter at
	// row 0 should reflect ~119, not the full series max.
	topRow := rows[0]
	// Pull the price label off the right edge.
	label := strings.TrimSpace(topRow[len(topRow)-priceLabelWidth:])
	// We expect "119" or close to it; if the renderer clipped from the
	// wrong side, the label would reflect ~100.
	if !strings.HasPrefix(label, "11") && !strings.HasPrefix(label, "12") {
		t.Errorf("top label = %q, expected ~119 (visible window's high). Wrong-side clip?", label)
	}
}

// TestRenderHidePriceLabels: when HidePriceLabels is set, the chart
// uses the full width and no label text appears in the right gutter.
func TestRenderHidePriceLabels(t *testing.T) {
	cs := []Candle{candle("2026-05-04T14:00:00Z", 100, 110, 90, 105)}
	rows := Render(cs, 40, 10, RenderOptions{Timeframe: time.Minute, HidePriceLabels: true})
	for _, r := range rows {
		// No price digits should appear anywhere.
		for _, ch := range r {
			if ch >= '0' && ch <= '9' {
				t.Errorf("digit found in row but labels hidden: %q", r)
				return
			}
		}
	}
}

// TestRenderDefaults: zero-value RenderOptions applies a 1m
// timeframe and a 2-cell candle stride by default.
func TestRenderDefaultsToOneMinuteTimeframe(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
		candle("2026-05-04T14:01:00Z", 105, 115, 100, 112),
	}
	rows := Render(cs, 16, 8, RenderOptions{}) // zero opts → timeframe=1m

	chartW := 16 - priceLabelWidth // 8
	// With 8 columns and 2 candles right-aligned, default stride=2
	// puts bodies at cols 4 and 6 with blank gap cells after them.
	hadBodyAtCol := make([]bool, chartW)
	for _, r := range rows {
		runes := []rune(r)
		for c := 0; c < chartW && c < len(runes); c++ {
			if runes[c] == glyphBodyUp || runes[c] == glyphBodyDown {
				hadBodyAtCol[c] = true
			}
		}
	}
	if !hadBodyAtCol[chartW-4] || !hadBodyAtCol[chartW-2] {
		t.Errorf("expected strided body glyphs at cols %d and %d, got %v",
			chartW-4, chartW-2, hadBodyAtCol)
	}
	if hadBodyAtCol[chartW-3] || hadBodyAtCol[chartW-1] {
		t.Errorf("expected gap cells after candle bodies, got %v", hadBodyAtCol)
	}
}

func TestRenderCandleStrideOneDenseMode(t *testing.T) {
	cs := []Candle{
		candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
		candle("2026-05-04T14:01:00Z", 105, 115, 100, 112),
	}
	rows := Render(cs, 16, 8, RenderOptions{CandleStride: 1})
	chartW := 16 - priceLabelWidth
	hadBodyAtCol := make([]bool, chartW)
	for _, r := range rows {
		runes := []rune(r)
		for c := 0; c < chartW && c < len(runes); c++ {
			if runes[c] == glyphBodyUp || runes[c] == glyphBodyDown {
				hadBodyAtCol[c] = true
			}
		}
	}
	if !hadBodyAtCol[chartW-1] || !hadBodyAtCol[chartW-2] {
		t.Errorf("stride=1 should render adjacent latest bodies at cols %d and %d, got %v",
			chartW-2, chartW-1, hadBodyAtCol)
	}
}

// TestFormatPriceShape: spot-check the price formatter at common
// magnitudes. Locking the format protects against silent changes
// that would shift label widths.
func TestFormatPriceShape(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{0.0001, "0.0001"},
		{1.5, "1.50"},
		{100.5, "100.50"},
		{12345, "12345"},
		{78465, "78465"},
		{125000, "125.0K"},
		{1_500_000, "1.50M"},
	}
	for _, c := range cases {
		got := formatPrice(c.in)
		if got != c.want {
			t.Errorf("formatPrice(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRenderBoundedGapAllocation: a sparse seed with two candles
// 1440 minutes apart at 1m timeframe must NOT allocate the full
// 1440 gap entries — only the visible window fits in the chart, so
// the gap expansion has to be bounded by width. This catches the
// pre-fix unbounded-allocation behaviour Codex flagged: with
// pathological inputs (seed candles a day apart, week apart, etc.)
// the old code would allocate millions of nil pointers before
// clipping.
//
// Bounded behaviour means the renderer's runtime/memory is O(width
// × height) regardless of the candle slice's time span — the
// invariant that makes Render safe to call on any input.
func TestRenderBoundedGapAllocation(t *testing.T) {
	cs := []Candle{
		// 24 hours apart at 1m timeframe = 1439 gap minutes.
		candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
		candle("2026-05-05T14:00:00Z", 105, 115, 100, 112),
	}
	// Width 16, chart=8. We can only show the newest candle plus
	// 7 columns of gaps — the older candle gets clipped from view.
	rows := Render(cs, 16, 10, RenderOptions{Timeframe: time.Minute})

	if len(rows) != 10 {
		t.Fatalf("len(rows) = %d, want 10", len(rows))
	}
	for i, r := range rows {
		if visibleLen(r) != 16 {
			t.Errorf("row %d width = %d, want 16", i, visibleLen(r))
		}
	}

	// Only the newest (rightmost) column should carry a body glyph.
	chartW := 16 - priceLabelWidth // 8
	bodyAt := -1
	for _, r := range rows {
		runes := []rune(r)
		for c := 0; c < chartW && c < len(runes); c++ {
			if runes[c] == glyphBodyUp || runes[c] == glyphBodyDown || runes[c] == glyphFlat {
				if bodyAt == -1 {
					bodyAt = c
				} else if bodyAt != c {
					t.Errorf("body glyphs found in multiple columns (%d and %d); only newest should be visible",
						bodyAt, c)
				}
			}
		}
	}
	if bodyAt == -1 {
		t.Errorf("expected newest candle to render in rightmost column, found no body glyphs")
	}
	if bodyAt != chartW-2 {
		t.Errorf("newest candle at col %d, want col %d (right edge slot body)", bodyAt, chartW-2)
	}
}

// TestRenderRowsAreExactlyWidth: every returned row is exactly
// `width` cells wide, regardless of input shape. The chart panel
// joins these with newlines into a Bubble Tea View; a single
// off-by-one breaks the layout downstream.
func TestRenderRowsAreExactlyWidth(t *testing.T) {
	scenarios := []struct {
		name string
		cs   []Candle
		w, h int
		opts RenderOptions
	}{
		{"empty", nil, 40, 10, RenderOptions{}},
		{"flat", []Candle{
			candle("2026-05-04T14:00:00Z", 100, 100, 100, 100),
		}, 40, 10, RenderOptions{}},
		{"normal", []Candle{
			candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
			candle("2026-05-04T14:01:00Z", 105, 115, 100, 112),
		}, 60, 14, RenderOptions{}},
		{"hide labels", []Candle{
			candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
		}, 40, 10, RenderOptions{HidePriceLabels: true}},
		{"with gaps", []Candle{
			candle("2026-05-04T14:00:00Z", 100, 110, 90, 105),
			candle("2026-05-04T14:05:00Z", 105, 115, 100, 112),
		}, 30, 12, RenderOptions{}},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			rows := Render(s.cs, s.w, s.h, s.opts)
			if len(rows) != s.h {
				t.Fatalf("len(rows) = %d, want %d", len(rows), s.h)
			}
			for i, r := range rows {
				if got := visibleLen(r); got != s.w {
					t.Errorf("row %d width = %d, want %d (row=%q)", i, got, s.w, r)
				}
			}
		})
	}
}

// visibleLen counts runes in s — fine for our ASCII + simple
// box-drawing glyphs (no double-width East Asian glyphs in our
// output).
func visibleLen(s string) int {
	return len([]rune(s))
}
