package perps

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
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
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List all available perpetual instruments",
	Example: `  laevitas perps catalog
  laevitas perps catalog --exchange binance`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{Exchange: cmdutil.Exchange}
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
  laevitas perps carry ETH-PERPETUAL -p 1h -o json | jq '.[].funding_rate_close'`,
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
	SortDir   string
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
		params.SortDir = tradesFlags.SortDir
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

var orderbookFlags cmdutil.CommonFlags

var orderbookCmd = &cobra.Command{
	Use:   "orderbook <instrument>",
	Short: "L2 orderbook depth metrics",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas perps orderbook BTC-PERPETUAL -p 24h
  laevitas perps orderbook BTCUSDT --exchange binance -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsOrderbook, params)
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
	SortDir      string
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
		params.SortDir = liquidationsFlags.SortDir
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
Valid --group-by values: exchange, instrument_name, direction, strategy.`,
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
and most active instruments — all in a single call.`,
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
	tradesCmd.Flags().StringVar(&tradesFlags.SortDir, "sort-dir", "", "Sort direction: ASC or DESC")
	tradesCmd.Flags().IntVar(&tradesFlags.TopN, "top-n", 0, "Return top N trades (no pagination)")

	cmdutil.AddCommonFlags(volumeCmd, &volumeFlags)
	cmdutil.AddCommonFlags(level1Cmd, &level1Flags)
	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(refPriceCmd, &refPriceFlags)

	cmdutil.AddCommonFlags(liquidationsCmd, &liquidationsFlags.CommonFlags)
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.Direction, "direction", "", "Filter: buy or sell")
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.PositionSide, "position-side", "", "Filter: long or short")
	liquidationsCmd.Flags().Float64Var(&liquidationsFlags.MinAmountUsd, "min-amount-usd", 0, "Min liquidation value in USD")
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.Sort, "sort", "", "Sort: timestamp, amount_usd, price")
	liquidationsCmd.Flags().StringVar(&liquidationsFlags.SortDir, "sort-dir", "", "Sort direction: ASC or DESC")

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
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(refPriceCmd)
	Cmd.AddCommand(metadataCmd)
	Cmd.AddCommand(liquidationsCmd)
	Cmd.AddCommand(tradesSummaryCmd)
	Cmd.AddCommand(flowCmd)
}
