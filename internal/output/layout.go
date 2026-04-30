// Package output — TUI layout primitives shared across every
// dashboard surface and live-stream renderer.
//
// These helpers solve one recurring problem in our multi-pane TUIs:
// stitching together pre-styled (ANSI-containing) strings into a
// rectangular grid where each cell's *visible* width matches the
// layout's expectation, regardless of the byte-length or
// East-Asian-Width of the underlying runes.
//
// Why one home? Earlier versions had a near-identical copy of every
// helper here in `internal/dashboard/panels/book.go` and another
// silently doing the same job in `internal/wsrender`. Each diverged
// over time — one counted bytes, another counted runes, a third was
// ANSI-aware but unicode-blind. The drift produced "boxes shift
// when the orderbook is thin on one side" — different rows measured
// at different widths and the side-by-side join was off-by-N per
// row. Lifting these to one place keeps every dashboard surface
// honest.
//
// Usage rule of thumb: never roll your own width measurement.
// Always go through `output.VisibleWidth` (which delegates to
// `lipgloss.Width`, which delegates to `uniseg.StringWidth`). Same
// for padding and truncation: use `output.PadRightAnsi` /
// `output.TruncateAnsi` so every surface measures consistently with
// what the terminal will actually render.
package output

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// VisibleWidth returns the visible cell width of s — ANSI-aware and
// unicode-cell-aware. Strips ANSI SGR escapes, then runs the remaining
// runes through `uniseg.StringWidth` (via lipgloss) so wide / narrow
// glyphs get their true terminal cell count.
//
// Use this in place of `len(s)` whenever the result feeds layout
// math: column padding, table widths, side-by-side pane joins.
// `len(s)` counts bytes; a single `▮` (U+25AE) is 3 bytes UTF-8 and
// renders as 1 cell. Past dashboards had byte-based pad helpers and
// the resulting per-row width drift was the worst class of bug to
// diagnose.
func VisibleWidth(s string) int {
	return lipgloss.Width(s)
}

// PadRightAnsi pads s with spaces on the right so its visible width
// reaches `width`. ANSI-aware — escapes pass through unchanged. If
// s is already wider, it gets truncated via TruncateAnsi so it
// can't bleed into adjacent panes.
//
// The truncation path is what keeps multi-pane layouts rectangular
// when one renderer drifts a cell past its budget (e.g. ladder bar
// math rounding). Without it, the next pane shifts right by however
// much the offender ran over.
func PadRightAnsi(s string, width int) string {
	visible := VisibleWidth(s)
	if visible == width {
		return s
	}
	if visible < width {
		return s + strings.Repeat(" ", width-visible)
	}
	return TruncateAnsi(s, width)
}

// TruncateAnsi clips s to `width` visible columns. ANSI SGR escapes
// pass through verbatim so colour state is maintained right up to
// the cut point; a final `\x1b[0m` reset is always appended so any
// open colour state can't leak into adjacent content.
//
// Width counts visible runes via the byte-decode below, NOT
// `lipgloss.Width`, because we need to track the cut position
// rune-by-rune as we emit the slice. Multi-byte UTF-8 sequences are
// emitted whole; we never split a rune. Width-2 East Asian glyphs
// would still be counted as one cell here — a known shortcoming
// the renderers in this codebase work around by avoiding ambiguous
// glyphs in measured contexts (see CLAUDE.md / lipgloss memory).
func TruncateAnsi(s string, width int) string {
	if width <= 0 {
		return ""
	}
	var b strings.Builder
	visible := 0
	i := 0
	for i < len(s) {
		// Pass-through ANSI SGR (\x1b[...m).
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				b.WriteString(s[i : j+1])
				i = j + 1
				continue
			}
			break
		}
		if visible >= width {
			break
		}
		_, size := decodeRune(s[i:])
		if size == 0 {
			size = 1
		}
		b.WriteString(s[i : i+size])
		i += size
		visible++
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// JoinSideBySide stitches two multi-line panes horizontally with a
// two-cell gutter. The left pane is padded to `leftWidth` cells per
// line (ANSI-aware) so the right pane always starts at the same
// column. Lines are zipped row-by-row; missing lines on either side
// render as blank padding so the panes stay rectangular even with
// different line counts.
//
// This is the canonical "ladder + strip" join used by every book
// dashboard. Future dashboards (perp screener, vol surface, chain
// view) should reach for this rather than rolling their own —
// per-pane width drift bugs come from each rendering its own glue.
func JoinSideBySide(left string, leftWidth int, right string) string {
	ll := strings.Split(left, "\n")
	rl := strings.Split(right, "\n")
	max := len(ll)
	if len(rl) > max {
		max = len(rl)
	}
	var b strings.Builder
	for i := 0; i < max; i++ {
		var leftLine string
		if i < len(ll) {
			leftLine = ll[i]
		}
		b.WriteString(PadRightAnsi(leftLine, leftWidth))
		b.WriteString("  ")
		if i < len(rl) {
			b.WriteString(rl[i])
		}
		if i < max-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// decodeRune is a tiny `utf8.DecodeRuneInString` shim that returns
// only the size of the leading rune (we don't need the rune value
// for our truncate path). Keeps the file's import surface tight.
func decodeRune(s string) (r int32, size int) {
	if len(s) == 0 {
		return 0, 0
	}
	c := s[0]
	switch {
	case c < 0x80:
		return int32(c), 1
	case c < 0xc0:
		return 0xfffd, 1
	case c < 0xe0:
		return 0xfffd, 2
	case c < 0xf0:
		return 0xfffd, 3
	default:
		return 0xfffd, 4
	}
}
