package panels

// Split-mode ladder rendering — the alternate presentation toggled
// by `m` (keymap.ActLadderMode). Same data the aggregated ladder
// uses, laid out differently:
//
//   - Aggregated (default): one centre-price ladder, segmented bars
//     coloured by venue contribution. Best for "what's the
//     consolidated book?".
//   - Split (this file): one narrow per-venue ladder column,
//     side-by-side. Best for "which venue has a wall here?".
//
// Layout per visible venue column (width ~ w / N):
//
//	BINANCE
//	───────────
//	76,200  1.20
//	76,180  0.50
//	── 76,170 ──   ← per-venue spread separator
//	76,160  0.80
//	76,140  2.00
//
// Constraints driving the design:
//   - 6+ venues × 78-cell ladder ≈ 13 cells/column. Just enough for
//     `76,200.0  0.001`. No room for bars; size text alone carries
//     the magnitude signal.
//   - Hidden venues (via `v` picker) drop out — orderedVenues already
//     filters them, so we just render whatever the caller passed.
//   - Per-venue spread separator instead of the global one: each
//     venue has its own spread, and showing per-venue mid where the
//     consolidated mid would normally sit gives the user the
//     comparison they came for.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/output"
)

// renderSplitLadder produces the per-venue side-by-side layout for
// the centre pane. Returns the rendered string ready to be glued
// next to the venue strip via output.JoinSideBySide (handled by
// View() — this function returns content only).
//
// The header (HeaderLine + StatsLine) renders once at the top and
// spans the full pane width — same shape as aggregated mode, so a
// user toggling between modes sees the same identity/stats line in
// the same place. Below it, columns of per-venue ladders.
func (p *BookPanel) renderSplitLadder(w, h int, books map[string]*api.BookSnapshot, venues []string, ctx dashboard.PanelContext) string {
	if len(venues) == 0 {
		// Caller's outer layout already handles the empty state via
		// renderWaiting; this branch fires only if every venue was
		// hidden via the picker. Show a hint rather than crashing.
		return output.BrandGreyMid + "all venues hidden — press v to pick" + output.Reset
	}

	// Column geometry. Two cells of inter-column gutter so adjacent
	// columns don't visually run together. Minimum column width of
	// 12 keeps `76,200.0 1.234` legible; below that we drop venues
	// to fit (rightmost first — preserves order users expect).
	const gutter = 2
	const minColW = 12
	colW := (w - gutter*(len(venues)-1)) / len(venues)
	if colW < minColW {
		// Re-fit: how many venues can we show at minColW?
		fit := (w + gutter) / (minColW + gutter)
		if fit < 1 {
			fit = 1
		}
		venues = venues[:fit]
		colW = (w - gutter*(len(venues)-1)) / len(venues)
	}

	// rowsPerSide derived from terminal height with the same chrome
	// budget the aggregated path uses, so toggling between modes
	// feels stable on a fixed-size window.
	const chrome = 3
	rowCap := (h - chrome) / 2
	if rowCap < 1 {
		rowCap = 1
	}
	if rowCap > 60 {
		rowCap = 60
	}
	rowsPerSide := rowCap
	if p.depthTier > 0 && p.depthTier < rowsPerSide {
		rowsPerSide = p.depthTier
	}

	// Build per-venue rendered columns, then JoinHorizontal them.
	// Each column is a multi-line string (asks block + separator +
	// bids block); JoinHorizontal handles unequal heights by padding
	// shorter columns with spaces — important when one venue has
	// fewer levels than another at the requested depth.
	cols := make([]string, 0, len(venues))
	for i, v := range venues {
		snap := books[v]
		if snap == nil {
			cols = append(cols, p.renderSplitColumnPlaceholder(v, colW))
		} else {
			cols = append(cols, p.renderSplitColumn(v, snap, colW, rowsPerSide))
		}
		// Insert a gutter column between venues (not after the last).
		if i < len(venues)-1 {
			cols = append(cols, strings.Repeat(" ", gutter))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

// renderSplitColumn produces one venue's narrow ladder. Title row +
// horizontal rule + asks (top, worst→best top-down) + spread row +
// bids (best→worst top-down). Width is fixed by the caller; rows
// pad-right so JoinHorizontal sees a rectangular block.
func (p *BookPanel) renderSplitColumn(venue string, snap *api.BookSnapshot, w, rowsPerSide int) string {
	vc, _ := output.VenueColor(venue)
	venueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(vc.Hex)).Bold(true)
	greyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreyMidHex))
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreenHex))
	redStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandRedHex))

	bb, ba := snap.BestLevels()
	spread := 0.0
	if ba.Price > 0 && bb.Price > 0 {
		spread = ba.Price - bb.Price
	}

	var lines []string

	// Title: venue name in brand colour, padded right to column width.
	title := venueStyle.Render(strings.ToUpper(venue))
	lines = append(lines, padCellLeft(title, w))

	// Underline rule — visually separates the title from the data.
	lines = append(lines, greyStyle.Render(strings.Repeat("-", w)))

	// Asks block — print worst-price first (top), best-price last
	// (just above the spread separator). Same direction the
	// aggregated ladder uses so reading top-down is consistent.
	asks := snap.Asks
	if len(asks) > rowsPerSide {
		asks = asks[:rowsPerSide]
	}
	// Pad with blank lines if the venue has fewer asks than
	// rowsPerSide so the spread separator lines up across columns.
	for i := 0; i < rowsPerSide-len(asks); i++ {
		lines = append(lines, padCellLeft("", w))
	}
	for i := len(asks) - 1; i >= 0; i-- {
		lvl := asks[i]
		lines = append(lines, splitLadderRow(lvl.Price, lvl.Size, w, redStyle))
	}

	// Spread separator — `── 76,170.50 ──` centred. Lets the user
	// glance across columns and see who's tightest at a glance.
	lines = append(lines, splitSpreadRow(spread, w, greyStyle))

	// Bids block — best-bid first (top), worst-bid last (bottom).
	bids := snap.Bids
	if len(bids) > rowsPerSide {
		bids = bids[:rowsPerSide]
	}
	for _, lvl := range bids {
		lines = append(lines, splitLadderRow(lvl.Price, lvl.Size, w, greenStyle))
	}
	// Pad with blank lines if the venue has fewer bids.
	for i := 0; i < rowsPerSide-len(bids); i++ {
		lines = append(lines, padCellLeft("", w))
	}

	return strings.Join(lines, "\n")
}

// renderSplitColumnPlaceholder renders a "waiting for data" stub
// for a venue we expect snapshots from but haven't received any
// yet. Same width as a real column so JoinHorizontal keeps the
// layout rectangular.
func (p *BookPanel) renderSplitColumnPlaceholder(venue string, w int) string {
	greyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreyMidHex))
	venueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreyMidHex)).Bold(true)
	title := venueStyle.Render(strings.ToUpper(venue))
	hint := greyStyle.Render("waiting…")
	return padCellLeft(title, w) + "\n" + padCellLeft(hint, w)
}

// splitLadderRow formats one (price, size) cell in a per-venue
// column. Layout: `PRICE  size`, both right-aligned with a single
// space between for readability. Total width matches the column
// budget set by the caller.
//
// Price uses the venue side's colour (red asks / green bids) so
// the column visually segments above/below the spread row even
// without explicit borders.
func splitLadderRow(price, size float64, w int, priceStyle lipgloss.Style) string {
	priceStr := output.FormatBookPrice(price)
	sizeStr := output.FormatBookSize(size)
	// 2-cell internal gutter between price and size so digits don't
	// touch. Right-align size so the units column lines up across
	// rows (helps the eye spot whales).
	priceCell := priceStyle.Render(priceStr)
	available := w - lipgloss.Width(priceStr) - lipgloss.Width(sizeStr)
	if available < 1 {
		available = 1
	}
	return priceCell + strings.Repeat(" ", available) + sizeStr
}

// splitSpreadRow formats the per-venue spread row in a column. Width
// matches the column budget so JoinHorizontal sees a rectangular
// block. Reads `── 0.10 ──` with the spread value centred.
func splitSpreadRow(spread float64, w int, style lipgloss.Style) string {
	label := output.FormatBookPrice(spread)
	visible := lipgloss.Width(label) + 2 // +2 for surrounding spaces
	dashes := w - visible
	if dashes < 2 {
		// Column too narrow for dashes; just centre the label.
		return padCellLeft(style.Render(label), w)
	}
	left := dashes / 2
	right := dashes - left
	return style.Render(strings.Repeat("─", left)) + " " + label + " " + style.Render(strings.Repeat("─", right))
}

