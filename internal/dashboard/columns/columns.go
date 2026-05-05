// Package columns provides declarative column definitions for the
// flow screener. Kept tiny on purpose: enough to render a perp
// screener for v0.10.0, with a shape that lets options/futures/spot
// add their own column sets later as separate (Column) slices.
//
// A Column is a (header label, extractor function, width) triple.
// The screener panel iterates columns in order, calls each
// extractor against a typed row, and concatenates the formatted
// cells into a screener line. No sort comparators, no flash-on-
// change animations, no per-column styling — those are deferred to
// v0.10.1 if the panel actually needs them.
//
// Adding a new product family means defining a new typed row
// struct and a new Column slice in this package. The screener
// itself is generic over the row type (via a type parameter) so
// the dispatcher in FlowPanel picks the right column set per
// market.
package columns

// Column declares one screener column. Extractor returns the
// pre-formatted cell string; the screener pads/truncates to Width
// before joining with spaces. Width is a fixed number of visible
// cells — variable-width columns would zigzag prices/sizes
// between rows, the exact thing column-based rendering exists to
// avoid.
type Column[Row any] struct {
	// Header is the column's text label (uppercase by convention).
	// Truncated to Width if longer.
	Header string

	// Width is the column's visible cell width. Cells are right-
	// padded with spaces if shorter; truncated to fit if longer.
	Width int

	// Extract returns the formatted cell string for one row. The
	// returned string should not contain ANSI escapes (the screener
	// applies styling at the row level, not the cell level, in
	// v0.10.0 — colour-per-cell is v0.10.1+).
	Extract func(Row) string
}
