package columns

import (
	"fmt"

	"github.com/laevitas/cli/internal/output"
)

// PerpRow is one row of the perp screener — one instrument on one
// venue. Decoded from the `/api/v1/perpetuals/snapshot` envelope's
// data array; the screener stores a slice of these as its row
// model and re-renders them on every View call.
//
// Field set is deliberately small: just what the v0.10.0 columns
// need plus the identity fields (Exchange, InstrumentName) the
// screener uses to build channel strings and emit selection
// changes. If a future column wants more, add it to PerpRow and
// the wire decoder in screener.go picks it up via the same JSON
// tag.
type PerpRow struct {
	Exchange       string  `json:"exchange"`
	InstrumentName string  `json:"instrument_name"`
	MarkPrice      float64 `json:"mark_price"`
	BidPrice       float64 `json:"bid_price"`
	AskPrice       float64 `json:"ask_price"`
	Volume24hUSD   float64 `json:"volume_usd_24h"`
	OI             float64 `json:"oi"`
	FundingRate    float64 `json:"funding_rate"`
}

// Spread returns the bid-ask spread in price units. Computed on
// demand rather than stored on the row because it's a derived
// value and the snapshot envelope doesn't carry it.
func (r PerpRow) Spread() float64 {
	if r.AskPrice <= 0 || r.BidPrice <= 0 {
		return 0
	}
	return r.AskPrice - r.BidPrice
}

// PerpColumns is the screener column set for perpetuals. Order is
// (instrument, last, spread, 24h vol, OI, funding) per the v0.10.0
// design lock. Widths are tuned for typical 80–120 column terminal
// widths; the screener trims columns from the right if width gets
// too small.
//
// The "INSTRUMENT" column carries the venue:symbol pair so the
// user can scan multiple venues side-by-side without losing track
// of which row is which. 28 cells fits "binance:BTCUSDT" plus a
// few longer venue/symbol combos; the format helper truncates
// over-long instruments to keep the column boundary stable.
var PerpColumns = []Column[PerpRow]{
	{
		Header: "INSTRUMENT",
		Width:  28,
		Extract: func(r PerpRow) string {
			return truncOrPad(r.Exchange+":"+r.InstrumentName, 28)
		},
	},
	{
		Header: "LAST",
		Width:  12,
		Extract: func(r PerpRow) string {
			return output.FormatBookPrice(r.MarkPrice)
		},
	},
	{
		Header: "SPREAD",
		Width:  10,
		Extract: func(r PerpRow) string {
			// Spread is derivative — trailing zeros are noise.
			// FormatSpread emits "0.10" not "0.100000".
			return output.FormatSpread(r.Spread())
		},
	},
	{
		Header: "24H VOL",
		Width:  12,
		Extract: func(r PerpRow) string {
			return formatUSDCompact(r.Volume24hUSD)
		},
	},
	{
		Header: "OI",
		Width:  12,
		Extract: func(r PerpRow) string {
			// API returns OI in the contract's base unit (coins for
			// deribit/hyperliquid BTC perps; coins or contracts for
			// binance/bybit USDT perps depending on contract spec).
			// We render USD because cross-venue OI in coin units is
			// not comparable — a 100k-contract OI on a USD-margined
			// inverse perp and a 100k-coin OI on a USDT-margined
			// linear perp are different magnitudes by the spot
			// price. Multiplying by mark_price gives a single
			// USD-denominated yardstick that ranks venues correctly.
			//
			// We use mark_price rather than index_price so the OI
			// USD reflects the venue's own contract pricing (the
			// same number a trader sees on the venue UI).
			return formatUSDCompact(r.OI * r.MarkPrice)
		},
	},
	{
		Header: "FUNDING",
		Width:  10,
		Extract: func(r PerpRow) string {
			// Funding rate is a fraction (0.0001 = 1bp). Render as a
			// percentage with 4 decimals — covers the typical 8h
			// funding range (-0.05% to +0.05%) without truncating.
			// Sign included so positive vs negative funding is
			// visually obvious without colour.
			return fmt.Sprintf("%+.4f%%", r.FundingRate*100)
		},
	},
}

// truncOrPad returns s truncated or right-padded to exactly width
// visible cells. ASCII-only — instrument names contain no ANSI
// escapes.
func truncOrPad(s string, width int) string {
	if len(s) >= width {
		if width <= 0 {
			return ""
		}
		return s[:width]
	}
	return s + spaces(width-len(s))
}

// spaces returns a string of n spaces. Trivial helper kept here so
// the columns package has zero non-stdlib non-laevitas imports
// beyond `output`.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = ' '
	}
	return string(out)
}

// formatUSDCompact renders a USD magnitude in compact form for
// screener columns. Same shape rules as the liquidations panel's
// formatter (which lives there because it's panel-specific styling
// in that pane); duplicating ~8 lines is cheaper than promoting
// formatUSD to a shared package and risking the two callers
// diverging on rounding rules. If a third caller appears,
// consolidate.
func formatUSDCompact(v float64) string {
	switch {
	case v <= 0:
		return ""
	case v < 1_000:
		return fmt.Sprintf("$%.0f", v)
	case v < 10_000:
		return fmt.Sprintf("$%.1fK", v/1_000)
	case v < 1_000_000:
		return fmt.Sprintf("$%.0fK", v/1_000)
	case v < 1_000_000_000:
		return fmt.Sprintf("$%.1fM", v/1_000_000)
	default:
		return fmt.Sprintf("$%.1fB", v/1_000_000_000)
	}
}
