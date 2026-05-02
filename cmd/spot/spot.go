package spot

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/output"
)

// spotExchange returns the exchange to use for spot endpoints.
// Deribit does not trade spot, so when the global default is deribit and the
// user has not explicitly overridden --exchange, fall back to binance.
func spotExchange() string {
	switch cmdutil.Exchange {
	case "binance", "coinbase", "bybit", "okx", "kraken", "bullish":
		return cmdutil.Exchange
	}
	return "binance"
}

var Cmd = &cobra.Command{
	Use:   "spot",
	Short: "Spot market data — catalog, OHLCVT, ticker, volume, orderbook, trades",
	Long: `Access spot market data from binance, coinbase, bybit, okx, kraken.

Examples:
  laevitas spot catalog --exchange binance
  laevitas spot snapshot --exchange binance --currency BTC
  laevitas spot ohlcvt BTCUSDT -p 24h -r 1h
  laevitas spot trades --currency BTC --min-quote-amount 100000
  laevitas spot volume BTCUSDT -p 7d -r 1d`,
}

// ─── catalog ────────────────────────────────────────────────────────────────

var catalogFlags struct {
	cmdutil.CommonFlags
	QuoteCurrency string
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List available spot instruments (paginated)",
	Example: `  laevitas spot catalog --exchange binance
  laevitas spot catalog --exchange coinbase --currency BTC
  laevitas spot catalog --exchange binance --quote-currency USDT -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := catalogFlags.CommonFlags.ToParams()
		params.Exchange = spotExchange()
		params.QuoteCurrency = catalogFlags.QuoteCurrency
		// Catalog has no time-series — clear time-window params we don't want sent.
		params.Start = ""
		params.End = ""
		params.Resolution = ""
		params.SortDir = ""
		cmdutil.RunAndPrint(client, api.SpotCatalog, params)
	},
}

// ─── snapshot ───────────────────────────────────────────────────────────────

var snapshotFlags struct {
	Currency      string
	QuoteCurrency string
	Date          string
	Resolution    string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Snapshot of all spot instruments at a single minute",
	Example: `  laevitas spot snapshot --exchange binance --currency BTC
  laevitas spot snapshot --exchange coinbase --quote-currency USD
  laevitas spot snapshot --exchange binance --date 2026-04-25T12:00:00Z`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:      spotExchange(),
			Currency:      snapshotFlags.Currency,
			QuoteCurrency: snapshotFlags.QuoteCurrency,
			Date:          snapshotFlags.Date,
			Resolution:    snapshotFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.SpotSnapshot, params)
	},
}

// ─── metadata ───────────────────────────────────────────────────────────────

var metadataCmd = &cobra.Command{
	Use:     "metadata <instrument>",
	Short:   "Data availability for a spot instrument",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas spot metadata BTCUSDT --exchange binance`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       spotExchange(),
		}
		cmdutil.RunAndPrint(client, api.SpotMetadata, params)
	},
}

// ─── ohlcvt ─────────────────────────────────────────────────────────────────

var ohlcvFlags struct {
	cmdutil.CommonFlags
	QuoteCurrency string
}

var ohlcvCmd = &cobra.Command{
	Use:     "ohlcvt <instrument>",
	Aliases: []string{"ohlcv"},
	Short:   "OHLCVT candle data from spot trades",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas spot ohlcvt BTCUSDT -p 24h
  laevitas spot ohlcvt ETHUSDT -p 7d -r 1h
  laevitas spot ohlcvt BTC-USD --exchange coinbase -p 24h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := ohlcvFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = spotExchange()
		params.QuoteCurrency = ohlcvFlags.QuoteCurrency
		cmdutil.RunAndPrint(client, api.SpotOHLCVT, params)
	},
}

// ─── ticker ─────────────────────────────────────────────────────────────────

var tickerFlags struct {
	cmdutil.CommonFlags
	QuoteCurrency string
}

var tickerCmd = &cobra.Command{
	Use:   "ticker <instrument>",
	Short: "Historical ticker snapshots (bid/ask, spread, 24h stats)",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas spot ticker BTCUSDT -p 24h
  laevitas spot ticker ETHUSDT -p 3d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tickerFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = spotExchange()
		params.QuoteCurrency = tickerFlags.QuoteCurrency
		cmdutil.RunAndPrint(client, api.SpotTicker, params)
	},
}

// ─── volume ─────────────────────────────────────────────────────────────────

var volumeFlags struct {
	cmdutil.CommonFlags
	QuoteCurrency string
}

var volumeCmd = &cobra.Command{
	Use:   "volume <instrument>",
	Short: "24h rolling volume metrics, buy/sell breakdown",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas spot volume BTCUSDT -p 24h
  laevitas spot volume ETHUSDT -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := volumeFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = spotExchange()
		params.QuoteCurrency = volumeFlags.QuoteCurrency
		cmdutil.RunAndPrint(client, api.SpotVolume, params)
	},
}

// ─── level1 ─────────────────────────────────────────────────────────────────

var level1Flags struct {
	cmdutil.CommonFlags
	QuoteCurrency string
}

var level1Cmd = &cobra.Command{
	Use:   "level1 <instrument>",
	Short: "Top-of-book bid/ask, spreads, liquidity metrics",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas spot level1 BTCUSDT -p 24h
  laevitas spot level1 ETHUSDT -p 3d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := level1Flags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = spotExchange()
		params.QuoteCurrency = level1Flags.QuoteCurrency
		cmdutil.RunAndPrint(client, api.SpotLevel1, params)
	},
}

// ─── orderbook (l2 aggregated) ──────────────────────────────────────────────

var orderbookFlags struct {
	cmdutil.CommonFlags
	output.BookFilterFlags
	QuoteCurrency string
}

var orderbookCmd = &cobra.Command{
	Use:     "orderbook <instrument>",
	Aliases: []string{"l2-orderbook"},
	Short:   "Aggregated L2 orderbook depth (10/20/50/100 levels)",
	Long: `Historical L2 orderbook depth metrics.

This REST endpoint returns a wide metrics payload: bid/ask liquidity,
imbalance, and microprice across four depth tiers (10/20/50/100). Table
output shows a compact latest-close view at one tier; use --depth N to
pick which tier the table surfaces. Use -o json or -o csv for the full
payload (all tiers, all OHLC fields).

For a current snapshot of the actual asks/bids, use:
  laevitas spot orderbook-raw <instrument>`,
	Args: cobra.ExactArgs(1),
	Example: `  # Historical metrics table (compact, default tier 10)
  laevitas spot orderbook BTCUSDT -p 24h

  # Pick a deeper tier for the table view
  laevitas spot orderbook BTCUSDT -p 24h --depth 100

  # Full metrics payload for agents/scripts (all tiers)
  laevitas spot orderbook BTCUSDT -p 7d -r 1h -o json`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = spotExchange()
		params.QuoteCurrency = orderbookFlags.QuoteCurrency
		cmdutil.RunAndPrintFiltered(client, api.SpotL2Orderbook, params, orderbookFlags.BookFilterFlags)
	},
}

// ─── orderbook-raw ──────────────────────────────────────────────────────────

var orderbookRawFlags struct {
	cmdutil.CommonFlags
	output.BookFilterFlags
	QuoteCurrency string
}

var orderbookRawCmd = &cobra.Command{
	Use:     "orderbook-raw <instrument>",
	Aliases: []string{"l2-orderbook-raw"},
	Short:   "Full L2 orderbook snapshots with raw bid/ask arrays",
	Args:    cobra.ExactArgs(1),
	Example: `  laevitas spot orderbook-raw BTCUSDT -p 1h
  laevitas spot orderbook-raw ETHUSDT --start 2026-04-25T00:00:00Z --end 2026-04-25T01:00:00Z`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookRawFlags.CommonFlags.ToParams()
		params.InstrumentName = args[0]
		params.Exchange = spotExchange()
		params.QuoteCurrency = orderbookRawFlags.QuoteCurrency
		// raw orderbook has no resolution param
		params.Resolution = ""
		cmdutil.ApplySnapshotDefaults(params)
		cmdutil.RunAndPrintFiltered(client, api.SpotL2OrderbookRaw, params, orderbookRawFlags.BookFilterFlags)
	},
}

// ─── trades ─────────────────────────────────────────────────────────────────

var tradesFlags struct {
	cmdutil.CommonFlags
	QuoteCurrency  string
	Direction      string
	MinAmount      float64
	MinQuoteAmount float64
	Sort           string
	TopN           int
}

var tradesCmd = &cobra.Command{
	Use:   "trades [instrument]",
	Short: "Individual spot trades (by instrument or currency)",
	Long: `Fetch individual spot trades. Two modes:
  • Instrument mode: laevitas spot trades BTCUSDT -p 24h
  • Currency mode:   laevitas spot trades --currency BTC --min-quote-amount 100000`,
	Args: cobra.MaximumNArgs(1),
	Example: `  laevitas spot trades BTCUSDT -p 24h
  laevitas spot trades --currency BTC --quote-currency USDT --min-quote-amount 100000
  laevitas spot trades --currency BTC --direction buy --top-n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesFlags.CommonFlags.ToParams()
		if len(args) > 0 {
			params.InstrumentName = args[0]
		}
		params.Exchange = spotExchange()
		params.QuoteCurrency = tradesFlags.QuoteCurrency
		params.Direction = tradesFlags.Direction
		params.MinAmount = tradesFlags.MinAmount
		params.MinQuoteAmount = tradesFlags.MinQuoteAmount
		params.Sort = tradesFlags.Sort
		params.TopN = tradesFlags.TopN
		cmdutil.RunAndPrint(client, api.SpotTrades, params)
	},
}

func init() {
	cmdutil.AddCommonFlags(catalogCmd, &catalogFlags.CommonFlags)
	catalogCmd.Flags().StringVar(&catalogFlags.QuoteCurrency, "quote-currency", "", "Quote currency filter (USDT, USDC, USD)")

	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Filter by base currency (BTC, ETH)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.QuoteCurrency, "quote-currency", "", "Filter by quote currency (USDT, USDC, USD)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601, defaults to now)")
	snapshotCmd.Flags().StringVarP(&snapshotFlags.Resolution, "resolution", "r", "", "Time resolution: 1m, 5m, 15m, 1h, 4h, 1d")

	cmdutil.AddCommonFlags(ohlcvCmd, &ohlcvFlags.CommonFlags)
	ohlcvCmd.Flags().StringVar(&ohlcvFlags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")

	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags.CommonFlags)
	tickerCmd.Flags().StringVar(&tickerFlags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")

	cmdutil.AddCommonFlags(volumeCmd, &volumeFlags.CommonFlags)
	volumeCmd.Flags().StringVar(&volumeFlags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")

	cmdutil.AddCommonFlags(level1Cmd, &level1Flags.CommonFlags)
	level1Cmd.Flags().StringVar(&level1Flags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")

	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags.CommonFlags)
	output.AddBookFilterFlags(orderbookCmd, &orderbookFlags.BookFilterFlags)
	orderbookCmd.Flags().StringVar(&orderbookFlags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")

	cmdutil.AddCommonFlags(orderbookRawCmd, &orderbookRawFlags.CommonFlags)
	orderbookRawCmd.Flags().StringVar(&orderbookRawFlags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")
	output.AddBookFilterFlags(orderbookRawCmd, &orderbookRawFlags.BookFilterFlags)

	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags.CommonFlags)
	tradesCmd.Flags().StringVar(&tradesFlags.QuoteCurrency, "quote-currency", "", "Quote currency (USDT, USDC, USD)")
	tradesCmd.Flags().StringVar(&tradesFlags.Direction, "direction", "", "Filter: buy or sell")
	tradesCmd.Flags().Float64Var(&tradesFlags.MinAmount, "min-amount", 0, "Min trade amount in base currency")
	tradesCmd.Flags().Float64Var(&tradesFlags.MinQuoteAmount, "min-quote-amount", 0, "Min trade value in quote currency")
	tradesCmd.Flags().StringVar(&tradesFlags.Sort, "sort", "", "Sort: timestamp, amount, price, quote_amount")
	tradesCmd.Flags().IntVar(&tradesFlags.TopN, "top-n", 0, "Return top N trades (no pagination)")

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(metadataCmd)
	Cmd.AddCommand(ohlcvCmd)
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(volumeCmd)
	Cmd.AddCommand(level1Cmd)
	Cmd.AddCommand(orderbookCmd)
	Cmd.AddCommand(orderbookRawCmd)
	Cmd.AddCommand(tradesCmd)
}
