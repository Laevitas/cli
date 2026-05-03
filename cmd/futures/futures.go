package futures

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/output"
)

var Cmd = &cobra.Command{
	Use:   "futures",
	Short: "Dated futures data — catalog, OHLCVT, OI, carry, trades",
	Long: `Access dated futures data from Deribit and Binance.

Examples:
  laevitas futures catalog
  laevitas futures snapshot --currency BTC
  laevitas futures ohlcvt {{FUT}} -p 24h
  laevitas futures ohlcvt {{FUT}} -p 3d -r 1h
  laevitas futures carry {{FUT}} -p 7d
  laevitas futures oi {{FUT}} -r 1d -n 30`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

// ─── catalog ────────────────────────────────────────────────────────────────

var catalogFlags struct {
	cmdutil.CommonFlags
	Maturity string
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List dated futures instruments (paginated)",
	Example: `  laevitas futures catalog
  laevitas futures catalog --exchange binance
  laevitas futures catalog --currency BTC --maturity {{MAT}}
  laevitas futures catalog -n 50 --cursor <next>`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := catalogFlags.CommonFlags.ToParams()
		// Catalog has no time-series.
		params.Start = ""
		params.End = ""
		params.Resolution = ""
		params.SortDir = ""
		// Cross-exchange registry — only filter by exchange when the
		// user explicitly asked for one. See cmdutil.ExchangeExplicit.
		if cmdutil.ExchangeExplicit {
			params.Exchange = cmdutil.Exchange
		}
		params.Maturity = catalogFlags.Maturity
		cmdutil.RunAndPrint(client, api.FuturesCatalog, params)
	},
}

// ─── snapshot ───────────────────────────────────────────────────────────────

var snapshotFlags struct {
	Currency string
	Date     string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Full market snapshot of ALL dated futures at a point in time",
	Example: `  laevitas futures snapshot --currency BTC
  laevitas futures snapshot --currency ETH --date 2025-02-01T12:00:00Z`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange: cmdutil.Exchange,
			Currency: snapshotFlags.Currency,
			Date:     snapshotFlags.Date,
		}
		cmdutil.RunAndPrint(client, api.FuturesSnapshot, params)
	},
}

// ─── ohlcvt ─────────────────────────────────────────────────────────────────

var ohlcvFlags cmdutil.CommonFlags

var ohlcvCmd = &cobra.Command{
	Use:     "ohlcvt <instrument>",
	Aliases: []string{"ohlcv"},
	Short:   "OHLCVT candle data from trades",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas futures ohlcvt {{FUT}} -p 24h
  laevitas futures ohlcvt {{FUT}} -p 3d -r 1h -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := ohlcvFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesOHLCVT, params)
	},
}

// ─── oi ─────────────────────────────────────────────────────────────────────

var oiFlags cmdutil.CommonFlags

var oiCmd = &cobra.Command{
	Use:     "oi <instrument>",
	Aliases: []string{"open-interest"},
	Short:   "Open interest data over time",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas futures oi {{FUT}} -p 7d
  laevitas futures oi {{FUT}} -p 30d -r 1d`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := oiFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesOpenInterest, params)
	},
}

// ─── carry ──────────────────────────────────────────────────────────────────

var carryFlags cmdutil.CommonFlags

var carryCmd = &cobra.Command{
	Use:     "carry <instrument>",
	Aliases: []string{"basis"},
	Short:   "Basis and annualized carry data",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas futures carry {{FUT}} -p 24h
  laevitas futures carry {{FUT}} -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := carryFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesCarry, params)
	},
}

// ─── trades ─────────────────────────────────────────────────────────────────

var tradesFlags struct {
	cmdutil.CommonFlags
	Direction string
	BlockOnly bool
	MinAmount float64
	Strategy  string
	Maturity  string
	Sort      string
	TopN      int
}

var tradesCmd = &cobra.Command{
	Use:   "trades [instrument]",
	Short: "Individual trade records (by instrument or currency)",
	Long: `Fetch individual trade records. Two modes:
  • Instrument mode: laevitas futures trades {{FUT}} -p 24h
  • Currency mode:   laevitas futures trades --currency BTC --top-n 50`,
	Args: cobra.MaximumNArgs(1),
	Example: `  laevitas futures trades {{FUT}} -p 24h
  laevitas futures trades {{FUT}} -p 1h -n 20
  laevitas futures trades --currency BTC --top-n 50
  laevitas futures trades --currency BTC --direction buy --block-only`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesFlags.CommonFlags.ToParams()
		if len(args) > 0 {
			params.InstrumentName = args[0]
		}
		params.Direction = tradesFlags.Direction
		params.BlockOnly = tradesFlags.BlockOnly
		params.MinAmount = tradesFlags.MinAmount
		params.Strategy = tradesFlags.Strategy
		params.Maturity = tradesFlags.Maturity
		params.Sort = tradesFlags.Sort
		params.TopN = tradesFlags.TopN
		cmdutil.RunAndPrint(client, api.FuturesTrades, params)
	},
}

// ─── volume ─────────────────────────────────────────────────────────────────

var volumeFlags cmdutil.CommonFlags

var volumeCmd = &cobra.Command{
	Use:   "volume <instrument>",
	Short: "24h rolling volume data",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures volume {{FUT}} -p 24h
  laevitas futures volume {{FUT}} -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := volumeFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesVolume, params)
	},
}

// ─── level1 ─────────────────────────────────────────────────────────────────

var level1Flags cmdutil.CommonFlags

var level1Cmd = &cobra.Command{
	Use:   "level1 <instrument>",
	Short: "Best bid/ask (L1) data over time",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures level1 {{FUT}} -p 24h
  laevitas futures level1 {{FUT}} -p 3d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := level1Flags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesLevel1, params)
	},
}

// ─── orderbook ──────────────────────────────────────────────────────────────

var orderbookFlags struct {
	cmdutil.CommonFlags
	output.BookFilterFlags
}

var orderbookCmd = &cobra.Command{
	Use:   "orderbook <instrument>",
	Short: "L2 orderbook depth metrics",
	Long: `Historical L2 orderbook depth metrics.

This REST endpoint returns a wide metrics payload: bid/ask liquidity,
imbalance, and microprice across four depth tiers (10/20/50/100). Table
output shows a compact latest-close view at one tier; use --depth N to
pick which tier the table surfaces. Use -o json or -o csv for the full
payload (all tiers, all OHLC fields).

For an interactive live order book ladder, use:
  laevitas ws futures book <exchange>:<instrument>`,
	Args: cmdutil.SingleInstrumentArg,
	Example: `  # Historical metrics table (compact, default tier 10)
  laevitas futures orderbook {{FUT}} -p 1h -r 1m

  # Pick a deeper tier for the table view
  laevitas futures orderbook {{FUT}} -p 1h -r 1m --depth 100

  # Full metrics payload for agents/scripts (all tiers)
  laevitas futures orderbook {{FUT}} -p 1h -r 1m -o json

  # Live book ladder TUI / NDJSON stream
  laevitas ws futures book deribit:{{FUT}}`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrintFiltered(client, api.FuturesOrderbook, params, orderbookFlags.BookFilterFlags)
	},
}

// ─── orderbook-raw ──────────────────────────────────────────────────────────
//
// Raw L2 snapshot endpoint — sister command to `ws futures book`.
// Same `--depth` / `--compact` filters as every other surface that
// emits the snapshot book shape (perps orderbook-raw, spot
// orderbook-raw, predictions orderbook, ws book) via the shared
// output.AddBookFilterFlags bundle.
var orderbookRawFlags struct {
	cmdutil.CommonFlags
	output.BookFilterFlags
}

var orderbookRawCmd = &cobra.Command{
	Use:     "orderbook-raw <instrument>",
	Aliases: []string{"l2-orderbook-raw", "book"},
	Short:   "Full L2 orderbook snapshots with raw bid/ask arrays",
	Long: `Raw L2 orderbook snapshots — every level on each side at the
requested timestamp.

For a continuous live stream of the same shape, use the WebSocket form:
  laevitas ws futures book <exchange>:<instrument>

Both transports accept --depth and --compact for agent-friendly trimming.`,
	Args: cobra.ExactArgs(1),
	Example: `  laevitas futures orderbook-raw {{FUT}} --exchange deribit -p 1h
  laevitas futures orderbook-raw {{FUT}} --exchange deribit --depth 10
  laevitas futures orderbook-raw {{FUT}} --exchange deribit --depth 10 --compact -o json`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookRawFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		// raw orderbook has no resolution param
		params.Resolution = ""
		cmdutil.ApplySnapshotDefaults(params)
		cmdutil.RunAndPrintFiltered(client, api.FuturesOrderbookRaw, params, orderbookRawFlags.BookFilterFlags)
	},
}

// ─── ticker ─────────────────────────────────────────────────────────────────

var tickerFlags cmdutil.CommonFlags

var tickerCmd = &cobra.Command{
	Use:   "ticker <instrument>",
	Short: "Historical ticker snapshots (mark price, OI, bid/ask, funding)",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures ticker {{FUT}} -p 24h
  laevitas futures ticker {{FUT}} -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tickerFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesTickerHistory, params)
	},
}

// ─── ref-price ──────────────────────────────────────────────────────────────

var refPriceFlags cmdutil.CommonFlags

var refPriceCmd = &cobra.Command{
	Use:   "ref-price <instrument>",
	Short: "Mark price and index price OHLC",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures ref-price {{FUT}} -p 24h
  laevitas futures ref-price {{FUT}} -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := refPriceFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = cmdutil.Exchange
		cmdutil.RunAndPrint(client, api.FuturesReferencePrice, params)
	},
}

// ─── metadata ───────────────────────────────────────────────────────────────

var metadataCmd = &cobra.Command{
	Use:     "metadata <instrument>",
	Short:   "Data availability info for a dated futures instrument",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas futures metadata {{FUT}}`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       cmdutil.Exchange,
		}
		cmdutil.RunAndPrint(client, api.FuturesMetadata, params)
	},
}

// ─── liquidations ───────────────────────────────────────────────────────────

var liquidationsFlags struct {
	cmdutil.CommonFlags
	Direction    string
	PositionSide string
	MinAmountUsd float64
	Sort         string
}

var liquidationsCmd = &cobra.Command{
	Use:   "liquidations",
	Short: "Forced liquidation events for dated futures",
	Long: `Returns individual forced liquidation events.
Filter by --currency (e.g. BTC) or instrument via --currency + specific flags.`,
	Example: `  laevitas futures liquidations --currency BTC -p 24h
  laevitas futures liquidations --currency BTC --position-side long --min-amount-usd 10000
  laevitas futures liquidations --currency ETH --direction sell -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := liquidationsFlags.CommonFlags.ToParams()
		params.Direction = liquidationsFlags.Direction
		params.PositionSide = liquidationsFlags.PositionSide
		params.MinAmountUsd = liquidationsFlags.MinAmountUsd
		params.Sort = liquidationsFlags.Sort
		cmdutil.RunAndPrint(client, api.FuturesLiquidations, params)
	},
}

// ─── trades-summary ─────────────────────────────────────────────────────────

var tradesSummaryFlags struct {
	cmdutil.CommonFlags
	GroupBy   string
	Direction string
	BlockOnly bool
	MinAmount float64
	Strategy  string
	Maturity  string
}

var tradesSummaryCmd = &cobra.Command{
	Use:     "trades-summary",
	Aliases: []string{"ts"},
	Short:   "Aggregated trade statistics grouped by axis",
	Long: `Returns aggregated trade statistics grouped by a chosen axis.
Valid --group-by values: exchange, instrument_name, maturity, direction, strategy.

Flag notes:
  --group-by is required (the API needs to know what axis to aggregate on).
  Standard -n / --limit / pagination flags apply to the row count returned;
    each row represents one bucket of the chosen group.`,
	Example: `  laevitas futures trades-summary --currency BTC --group-by maturity
  laevitas futures trades-summary --currency BTC --group-by direction --block-only
  laevitas futures ts --currency ETH --group-by exchange -p 24h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesSummaryFlags.CommonFlags.ToParams()
		params.GroupBy = tradesSummaryFlags.GroupBy
		params.Direction = tradesSummaryFlags.Direction
		params.BlockOnly = tradesSummaryFlags.BlockOnly
		params.MinAmount = tradesSummaryFlags.MinAmount
		params.Strategy = tradesSummaryFlags.Strategy
		params.Maturity = tradesSummaryFlags.Maturity
		cmdutil.RunAndPrint(client, api.FuturesTradesSummary, params)
	},
}

// ─── flow ───────────────────────────────────────────────────────────────────

var flowFlags struct {
	Currency  string
	Start     string
	End       string
	MinAmount float64
	TopN      int
}

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Aggregated flow summary — trades, volume, OI, liquidations",
	Long: `Returns a complete futures flow summary including trade volume,
buy/sell breakdown, OI changes, liquidation pressure, notable trades,
and most active instruments — all in a single call.

Flag notes:
  --currency is required.
  --top-n caps the notable-trades / active-instruments lists. NOT a
    pagination flag — flow returns a single aggregated record per
    request, so -n / --limit / --cursor do not apply.`,
	Example: `  laevitas futures flow --currency BTC
  laevitas futures flow --currency BTC --min-amount 10 --top-n 20
  laevitas futures flow --currency ETH --start 2026-02-26T00:00:00Z`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:  cmdutil.Exchange,
			Currency:  flowFlags.Currency,
			Start:     flowFlags.Start,
			End:       flowFlags.End,
			MinAmount: flowFlags.MinAmount,
			TopN:      flowFlags.TopN,
		}
		cmdutil.RunAndPrint(client, api.FuturesFlow, params)
	},
}

func init() {
	cmdutil.AddCommonFlags(catalogCmd, &catalogFlags.CommonFlags)
	catalogCmd.Flags().StringVar(&catalogFlags.Maturity, "maturity", "", "Filter by maturity (e.g. "+cmdutil.ExampleMaturity()+")")

	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Filter by currency (BTC, ETH)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")

	cmdutil.AddCommonFlags(ohlcvCmd, &ohlcvFlags)
	cmdutil.AddCommonFlags(oiCmd, &oiFlags)
	cmdutil.AddCommonFlags(carryCmd, &carryFlags)

	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags.CommonFlags)
	tradesCmd.Flags().StringVar(&tradesFlags.Direction, "direction", "", "Filter: buy or sell")
	tradesCmd.Flags().BoolVar(&tradesFlags.BlockOnly, "block-only", false, "Only block trades")
	tradesCmd.Flags().Float64Var(&tradesFlags.MinAmount, "min-amount", 0, "Min trade amount (contracts)")
	tradesCmd.Flags().StringVar(&tradesFlags.Strategy, "strategy", "", "Filter by strategy")
	tradesCmd.Flags().StringVar(&tradesFlags.Maturity, "maturity", "", "Filter by maturity (e.g. "+cmdutil.ExampleMaturity()+")")
	tradesCmd.Flags().StringVar(&tradesFlags.Sort, "sort", "", "Sort: timestamp, amount_usd, price")
	tradesCmd.Flags().IntVar(&tradesFlags.TopN, "top-n", 0, "Return top N trades (no pagination)")

	cmdutil.AddCommonFlags(volumeCmd, &volumeFlags)
	cmdutil.AddCommonFlags(level1Cmd, &level1Flags)
	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags.CommonFlags)
	output.AddBookFilterFlags(orderbookCmd, &orderbookFlags.BookFilterFlags)
	cmdutil.AddCommonFlags(orderbookRawCmd, &orderbookRawFlags.CommonFlags)
	output.AddBookFilterFlags(orderbookRawCmd, &orderbookRawFlags.BookFilterFlags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(refPriceCmd, &refPriceFlags)

	cmdutil.AddCommonFlags(liquidationsCmd, &liquidationsFlags.CommonFlags)
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.Direction, "direction", "", "Filter: buy or sell")
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.PositionSide, "position-side", "", "Filter: long or short")
	liquidationsCmd.Flags().Float64Var(&liquidationsFlags.MinAmountUsd, "min-amount-usd", 0, "Min liquidation value in USD")
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.Sort, "sort", "", "Sort: timestamp, amount_usd, price")

	cmdutil.AddCommonFlags(tradesSummaryCmd, &tradesSummaryFlags.CommonFlags)
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.GroupBy, "group-by", "", "Group axis (required): exchange, instrument_name, maturity, direction, strategy")
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.Direction, "direction", "", "Filter: buy or sell")
	tradesSummaryCmd.Flags().BoolVar(&tradesSummaryFlags.BlockOnly, "block-only", false, "Only block trades")
	tradesSummaryCmd.Flags().Float64Var(&tradesSummaryFlags.MinAmount, "min-amount", 0, "Min trade amount")
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.Strategy, "strategy", "", "Filter by strategy")
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.Maturity, "maturity", "", "Filter by maturity")
	_ = tradesSummaryCmd.MarkFlagRequired("group-by")

	flowCmd.Flags().StringVar(&flowFlags.Currency, "currency", "", "Base currency (required)")
	flowCmd.Flags().StringVar(&flowFlags.Start, "start", "", "Start datetime (ISO 8601)")
	flowCmd.Flags().StringVar(&flowFlags.End, "end", "", "End datetime (ISO 8601)")
	flowCmd.Flags().Float64Var(&flowFlags.MinAmount, "min-amount", 0, "Min trade amount for notable trades")
	flowCmd.Flags().IntVar(&flowFlags.TopN, "top-n", 10, "Number of notable trades / active instruments")
	_ = flowCmd.MarkFlagRequired("currency")

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(ohlcvCmd)
	Cmd.AddCommand(oiCmd)
	Cmd.AddCommand(carryCmd)
	Cmd.AddCommand(tradesCmd)
	Cmd.AddCommand(volumeCmd)
	Cmd.AddCommand(level1Cmd)
	Cmd.AddCommand(orderbookCmd)
	Cmd.AddCommand(orderbookRawCmd)
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(refPriceCmd)
	Cmd.AddCommand(metadataCmd)
	Cmd.AddCommand(liquidationsCmd)
	Cmd.AddCommand(tradesSummaryCmd)
	Cmd.AddCommand(flowCmd)
}
