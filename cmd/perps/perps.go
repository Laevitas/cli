package perps

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/output"
)

var Cmd = &cobra.Command{
	Use:   "perps",
	Short: "Perpetual swap data — carry, OHLCVT, OI, trades",
	Long: `Access perpetual swap data from Deribit and Binance.

Examples:
  laevitas perps catalog
  laevitas perps carry BTC-PERPETUAL -p 24h
  laevitas perps ohlcvt BTCUSDT --exchange binance -p 3d -r 1h
  laevitas perps oi BTC-PERPETUAL -p 7d
  laevitas perps snapshot --currency BTC`,
	// Reject unknown positional args so typos like `perps banana` exit
	// non-zero instead of silently falling through to group help. Without
	// this, cobra's default for a parent with no Run is to print help and
	// exit 0 — dangerous for agents that rely on exit codes to detect
	// typos. Args validation alone isn't enough; we also need a RunE
	// that returns an error so Execute propagates non-zero.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Reached when no subcommand matched. Args validator above
		// already returned non-nil for unknown args; this branch covers
		// the bare `laevitas perps` invocation by showing help and
		// returning nil (success), preserving the help-on-no-args UX.
		return cmd.Help()
	},
}

var catalogFlags struct {
	cmdutil.CommonFlags
	Maturity string
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List perpetual instruments (paginated)",
	Example: `  laevitas perps catalog
  laevitas perps catalog --exchange binance
  laevitas perps catalog --currency BTC -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := catalogFlags.CommonFlags.ToParams()
		params.Start = ""
		params.End = ""
		params.Resolution = ""
		params.SortDir = ""
		// Catalog is a cross-exchange registry. Only filter by exchange
		// when the user explicitly asked for one — otherwise the
		// config default (e.g. "deribit") would hide every other
		// venue's listings.
		if cmdutil.ExchangeExplicit {
			params.Exchange = cmdutil.Exchange
		}
		params.Maturity = catalogFlags.Maturity
		cmdutil.RunAndPrint(client, api.PerpsCatalog, params)
	},
}

var snapshotFlags struct {
	Currency string
	Date     string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Market snapshot of ALL perpetuals at a point in time",
	Example: `  laevitas perps snapshot --currency BTC
  laevitas perps snapshot --currency ETH`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange: cmdutil.Exchange,
			Currency: snapshotFlags.Currency,
			Date:     snapshotFlags.Date,
		}
		cmdutil.RunAndPrint(client, api.PerpsSnapshot, params)
	},
}

var carryFlags cmdutil.CommonFlags

var carryCmd = &cobra.Command{
	Use:     "carry <instrument>",
	Aliases: []string{"funding"},
	Short:   "Funding rate, basis, and annualized carry",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas perps carry BTC-PERPETUAL -p 24h
  laevitas perps carry BTCUSDT --exchange binance -p 7d -r 1d
  laevitas perps carry ETH-PERPETUAL -p 1h -o json | jq '.data[].funding_rate_close'`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := carryFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsCarry, params)
	},
}

var ohlcvFlags cmdutil.CommonFlags

var ohlcvCmd = &cobra.Command{
	Use:     "ohlcvt <instrument>",
	Aliases: []string{"ohlcv"},
	Short:   "OHLCVT candle data from trades",
	Args:    cobra.ExactArgs(1),
	Example: `  # Deribit (default exchange)
  laevitas perps ohlcvt BTC-PERPETUAL -p 24h
  laevitas perps ohlcvt ETH-PERPETUAL -p 3d -r 1h

  # Binance (requires --exchange flag)
  laevitas perps ohlcvt BTCUSDT --exchange binance -p 24h
  laevitas perps ohlcvt ETHUSDT --exchange binance -p 7d -r 4h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := ohlcvFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsOHLCVT, params)
	},
}

var oiFlags cmdutil.CommonFlags

var oiCmd = &cobra.Command{
	Use:     "oi <instrument>",
	Aliases: []string{"open-interest"},
	Short:   "Open interest data over time",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas perps oi BTC-PERPETUAL -p 7d
  laevitas perps oi BTCUSDT --exchange binance -p 30d -r 1d`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := oiFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsOpenInterest, params)
	},
}

var tradesFlags struct {
	cmdutil.CommonFlags
	Direction string
	BlockOnly bool
	MinAmount float64
	Strategy  string
	Sort      string
	TopN      int
}

var tradesCmd = &cobra.Command{
	Use:   "trades [instrument]",
	Short: "Individual trade records (by instrument or currency)",
	Long: `Fetch individual trade records. Two modes:
  • Instrument mode: laevitas perps trades BTC-PERPETUAL -p 24h
  • Currency mode:   laevitas perps trades --currency BTC --top-n 50`,
	Args: cobra.MaximumNArgs(1),
	Example: `  laevitas perps trades BTC-PERPETUAL -p 24h
  laevitas perps trades BTCUSDT --exchange binance -p 1h -n 20
  laevitas perps trades --currency BTC --top-n 50
  laevitas perps trades --currency BTC --direction buy --block-only`,
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
		params.Sort = tradesFlags.Sort
		params.TopN = tradesFlags.TopN
		cmdutil.RunAndPrint(client, api.PerpsTrades, params)
	},
}

var volumeFlags cmdutil.CommonFlags

var volumeCmd = &cobra.Command{
	Use:   "volume <instrument>",
	Short: "24h rolling volume data",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas perps volume BTC-PERPETUAL -p 24h
  laevitas perps volume BTCUSDT --exchange binance -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := volumeFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsVolume, params)
	},
}

var level1Flags cmdutil.CommonFlags

var level1Cmd = &cobra.Command{
	Use:   "level1 <instrument>",
	Short: "Best bid/ask data over time",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas perps level1 BTC-PERPETUAL -p 24h
  laevitas perps level1 BTCUSDT --exchange binance -p 3d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := level1Flags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsLevel1, params)
	},
}

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
  laevitas ws perpetuals book <exchange>:<instrument>`,
	Args: cmdutil.SingleInstrumentArg,
	Example: `  # Historical metrics table (compact, default tier 10)
  laevitas perps orderbook BTCUSDT --exchange binance -p 1h -r 1m

  # Pick a deeper tier for the table view
  laevitas perps orderbook BTCUSDT --exchange binance -p 1h -r 1m --depth 50

  # Full metrics payload for agents/scripts (all tiers)
  laevitas perps orderbook BTCUSDT --exchange binance -p 1h -r 1m -o json

  # Live book ladder TUI / NDJSON stream
  laevitas ws perpetuals book binance:BTCUSDT`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrintFiltered(client, api.PerpsOrderbook, params, orderbookFlags.BookFilterFlags)
	},
}

// ─── orderbook-raw ──────────────────────────────────────────────────────────
//
// Fills the REST/WS parity gap: `ws perpetuals book <pair>` exists for
// streaming snapshots, but the corresponding one-shot REST call
// (`/api/v1/perpetuals/orderbook-raw`) had no CLI command. Now it does.
// Same `--depth` / `--compact` filters work here as on `ws perpetuals
// book`, so an agent can swap between transports without learning two
// flag dialects.
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
  laevitas ws perpetuals book <exchange>:<instrument>

Both transports accept --depth and --compact for agent-friendly trimming.`,
	Args: cobra.ExactArgs(1),
	Example: `  laevitas perps orderbook-raw BTCUSDT --exchange binance -p 1h
  laevitas perps orderbook-raw BTCUSDT --exchange binance --depth 10
  laevitas perps orderbook-raw BTCUSDT --exchange binance --depth 10 --compact -o json`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookRawFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		// raw orderbook has no resolution param
		params.Resolution = ""
		cmdutil.ApplySnapshotDefaults(params)
		cmdutil.RunAndPrintFiltered(client, api.PerpsOrderbookRaw, params, orderbookRawFlags.BookFilterFlags)
	},
}

var tickerFlags cmdutil.CommonFlags

var tickerCmd = &cobra.Command{
	Use:   "ticker <instrument>",
	Short: "Historical ticker snapshots",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas perps ticker BTC-PERPETUAL -p 24h
  laevitas perps ticker BTCUSDT --exchange binance -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tickerFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsTickerHistory, params)
	},
}

var refPriceFlags cmdutil.CommonFlags

var refPriceCmd = &cobra.Command{
	Use:   "ref-price <instrument>",
	Short: "Mark price and index price OHLC",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas perps ref-price BTC-PERPETUAL -p 24h
  laevitas perps ref-price BTCUSDT --exchange binance -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := refPriceFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = cmdutil.Exchange
		cmdutil.RunAndPrint(client, api.PerpsReferencePrice, params)
	},
}

var metadataCmd = &cobra.Command{
	Use:   "metadata <instrument>",
	Short: "Data availability info",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas perps metadata BTC-PERPETUAL
  laevitas perps metadata BTCUSDT --exchange binance`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       cmdutil.Exchange,
		}
		cmdutil.RunAndPrint(client, api.PerpsMetadata, params)
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
	Short: "Forced liquidation events for perpetual swaps",
	Long: `Returns individual forced liquidation events for perpetual contracts.
Filter by --currency (e.g. BTC) and optional direction/position filters.`,
	Example: `  laevitas perps liquidations --currency BTC -p 24h
  laevitas perps liquidations --currency BTC --position-side long --min-amount-usd 10000
  laevitas perps liquidations --currency ETH --direction sell -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := liquidationsFlags.CommonFlags.ToParams()
		params.Direction = liquidationsFlags.Direction
		params.PositionSide = liquidationsFlags.PositionSide
		params.MinAmountUsd = liquidationsFlags.MinAmountUsd
		params.Sort = liquidationsFlags.Sort
		cmdutil.RunAndPrint(client, api.PerpsLiquidations, params)
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
}

var tradesSummaryCmd = &cobra.Command{
	Use:     "trades-summary",
	Aliases: []string{"ts"},
	Short:   "Aggregated trade statistics grouped by axis",
	Long: `Returns aggregated trade statistics grouped by a chosen axis.
Valid --group-by values: exchange, instrument_name, direction, strategy.

Flag notes:
  --group-by is required (the API needs to know what axis to aggregate on).
  Standard -n / --limit / pagination flags apply to the row count returned;
    each row represents one bucket of the chosen group.`,
	Example: `  laevitas perps trades-summary --currency BTC --group-by direction
  laevitas perps trades-summary --currency BTC --group-by exchange --block-only
  laevitas perps ts --currency ETH --group-by instrument_name -p 24h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesSummaryFlags.CommonFlags.ToParams()
		params.GroupBy = tradesSummaryFlags.GroupBy
		params.Direction = tradesSummaryFlags.Direction
		params.BlockOnly = tradesSummaryFlags.BlockOnly
		params.MinAmount = tradesSummaryFlags.MinAmount
		params.Strategy = tradesSummaryFlags.Strategy
		cmdutil.RunAndPrint(client, api.PerpsTradesSummary, params)
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
	Long: `Returns a complete perpetuals flow summary including trade volume,
buy/sell breakdown, OI changes, liquidation pressure, notable trades,
and most active instruments — all in a single call.

Flag notes:
  --currency is required.
  --top-n caps the notable-trades / active-instruments lists. NOT a
    pagination flag — flow returns a single aggregated record per
    request, so -n / --limit / --cursor do not apply.`,
	Example: `  laevitas perps flow --currency BTC
  laevitas perps flow --currency BTC --min-amount 10 --top-n 20
  laevitas perps flow --currency ETH --start 2026-02-26T00:00:00Z`,
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
		cmdutil.RunAndPrint(client, api.PerpsFlow, params)
	},
}

func init() {
	cmdutil.AddCommonFlags(catalogCmd, &catalogFlags.CommonFlags)
	catalogCmd.Flags().StringVar(&catalogFlags.Maturity, "maturity", "", "Filter by maturity (e.g. "+cmdutil.ExampleMaturity()+")")

	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Filter by currency (BTC, ETH)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")

	cmdutil.AddCommonFlags(carryCmd, &carryFlags)
	cmdutil.AddCommonFlags(ohlcvCmd, &ohlcvFlags)
	cmdutil.AddCommonFlags(oiCmd, &oiFlags)

	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags.CommonFlags)
	tradesCmd.Flags().StringVar(&tradesFlags.Direction, "direction", "", "Filter: buy or sell")
	tradesCmd.Flags().BoolVar(&tradesFlags.BlockOnly, "block-only", false, "Only block trades")
	tradesCmd.Flags().Float64Var(&tradesFlags.MinAmount, "min-amount", 0, "Min trade amount (contracts)")
	tradesCmd.Flags().StringVar(&tradesFlags.Strategy, "strategy", "", "Filter by strategy")
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
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.GroupBy, "group-by", "", "Group axis (required): exchange, instrument_name, direction, strategy")
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.Direction, "direction", "", "Filter: buy or sell")
	tradesSummaryCmd.Flags().BoolVar(&tradesSummaryFlags.BlockOnly, "block-only", false, "Only block trades")
	tradesSummaryCmd.Flags().Float64Var(&tradesSummaryFlags.MinAmount, "min-amount", 0, "Min trade amount")
	tradesSummaryCmd.Flags().StringVar(&tradesSummaryFlags.Strategy, "strategy", "", "Filter by strategy")
	_ = tradesSummaryCmd.MarkFlagRequired("group-by")

	flowCmd.Flags().StringVar(&flowFlags.Currency, "currency", "", "Base currency (required)")
	flowCmd.Flags().StringVar(&flowFlags.Start, "start", "", "Start datetime (ISO 8601)")
	flowCmd.Flags().StringVar(&flowFlags.End, "end", "", "End datetime (ISO 8601)")
	flowCmd.Flags().Float64Var(&flowFlags.MinAmount, "min-amount", 0, "Min trade amount for notable trades")
	flowCmd.Flags().IntVar(&flowFlags.TopN, "top-n", 10, "Number of notable trades / active instruments")
	_ = flowCmd.MarkFlagRequired("currency")

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(carryCmd)
	Cmd.AddCommand(ohlcvCmd)
	Cmd.AddCommand(oiCmd)
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
