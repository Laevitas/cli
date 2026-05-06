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
	Exchange            string  `json:"exchange"`
	InstrumentName      string  `json:"instrument_name"`
	MarkPrice           float64 `json:"mark_price"`
	IndexPrice          float64 `json:"index_price"`
	LastPriceClose      float64 `json:"last_price_close"`
	BidPrice            float64 `json:"bid_price"`
	AskPrice            float64 `json:"ask_price"`
	BidAskSpread        float64 `json:"bid_ask_spread"`
	BidAskSpreadClose   float64 `json:"bid_ask_spread_close"`
	Volume24hUSD        float64 `json:"volume_usd_24h"`
	Volume24h           float64 `json:"volume_24h"`
	QuoteVolume24h      float64 `json:"quote_volume_24h"`
	TotalLiquidityClose float64 `json:"total_liquidity_close"`
	OI                  float64 `json:"oi"`
	FundingRate         float64 `json:"funding_rate"`
	DaysToExpiry        float64 `json:"days_to_expiry"`
}

// Spread returns the bid-ask spread in price units. Computed on
// demand rather than stored on the row because it's a derived
// value and the snapshot envelope doesn't carry it.
func (r PerpRow) Spread() float64 {
	if r.BidAskSpread != 0 {
		return r.BidAskSpread
	}
	if r.BidAskSpreadClose != 0 {
		return r.BidAskSpreadClose
	}
	if r.AskPrice <= 0 || r.BidPrice <= 0 {
		return 0
	}
	return r.AskPrice - r.BidPrice
}

// Last returns the row's best available last/mark price for the
// given market. Perps and futures use mark_price; spot snapshots
// carry last_price_close.
func (r PerpRow) Last(market string) float64 {
	if market == "spot" && r.LastPriceClose > 0 {
		return r.LastPriceClose
	}
	if r.MarkPrice > 0 {
		return r.MarkPrice
	}
	return r.LastPriceClose
}

// Basis returns mark - index for futures rows. Missing inputs
// return zero so the column stays blank.
func (r PerpRow) Basis() float64 {
	if r.MarkPrice == 0 || r.IndexPrice == 0 {
		return 0
	}
	return r.MarkPrice - r.IndexPrice
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

// FuturesColumns is the flow screener column set for expiring
// futures. The API carries mark/index/DTE directly, so basis and
// OI USD are local render-time calculations.
var FuturesColumns = []Column[PerpRow]{
	instrumentColumn(),
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
			return formatUSDCompact(r.OI * r.MarkPrice)
		},
	},
	{
		Header: "BASIS",
		Width:  10,
		Extract: func(r PerpRow) string {
			if r.Basis() == 0 {
				return ""
			}
			return fmt.Sprintf("%+.2f", r.Basis())
		},
	},
	{
		Header: "DTE",
		Width:  6,
		Extract: func(r PerpRow) string {
			if r.DaysToExpiry == 0 {
				return ""
			}
			return fmt.Sprintf("%.0f", r.DaysToExpiry)
		},
	},
}

// SpotColumns is the flow screener column set for spot markets.
// It keeps both native/base volume and quote/USD-equivalent volume:
// the former shows market activity in coin units, the latter lets
// users compare depth across instruments.
var SpotColumns = []Column[PerpRow]{
	instrumentColumn(),
	{
		Header: "LAST",
		Width:  12,
		Extract: func(r PerpRow) string {
			return output.FormatBookPrice(r.Last("spot"))
		},
	},
	{
		Header: "SPREAD",
		Width:  10,
		Extract: func(r PerpRow) string {
			return output.FormatSpread(r.Spread())
		},
	},
	{
		Header: "24H VOL",
		Width:  12,
		Extract: func(r PerpRow) string {
			return formatNumberCompact(r.Volume24h)
		},
	},
	{
		Header: "QUOTE VOL",
		Width:  12,
		Extract: func(r PerpRow) string {
			return formatUSDCompact(r.QuoteVolume24h)
		},
	},
	{
		Header: "LIQUIDITY",
		Width:  12,
		Extract: func(r PerpRow) string {
			return formatNumberCompact(r.TotalLiquidityClose)
		},
	},
}

func instrumentColumn() Column[PerpRow] {
	return Column[PerpRow]{
		Header: "INSTRUMENT",
		Width:  28,
		Extract: func(r PerpRow) string {
			return truncOrPad(r.Exchange+":"+r.InstrumentName, 28)
		},
	}
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

func formatNumberCompact(v float64) string {
	switch {
	case v <= 0:
		return ""
	case v < 1_000:
		return fmt.Sprintf("%.2f", v)
	case v < 10_000:
		return fmt.Sprintf("%.1fK", v/1_000)
	case v < 1_000_000:
		return fmt.Sprintf("%.0fK", v/1_000)
	case v < 1_000_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	default:
		return fmt.Sprintf("%.1fB", v/1_000_000_000)
	}
}
