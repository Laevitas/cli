package predictions

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/output"
)

// predictionsExchange returns the exchange to use for prediction-market
// endpoints. Polymarket is the only supported venue today, so the global
// `--exchange` default (which is `deribit` for the rest of the CLI) leaks
// a meaningless param into every predictions request. This helper pins the
// value to `polymarket` unless the user has explicitly chosen a known
// prediction-market exchange.
func predictionsExchange() string {
	switch cmdutil.Exchange {
	case "polymarket":
		return cmdutil.Exchange
	}
	return "polymarket"
}

var Cmd = &cobra.Command{
	Use:     "predictions",
	Aliases: []string{"pred", "pm"},
	Short:   "Prediction markets (Polymarket) — probabilities, trades, orderbooks",
	Long: `Access prediction market data from Polymarket.

Examples:
  laevitas predictions catalog --keyword bitcoin
  laevitas predictions categories
  laevitas predictions snapshot --category crypto
  laevitas predictions ohlcvt will-bitcoin-reach-250000-YES -r 1d`,
}

var catalogFlags struct {
	cmdutil.CommonFlags
	Category  string
	EventSlug string
	Keyword   string
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List prediction market instruments (paginated)",
	Example: `  laevitas predictions catalog --keyword bitcoin
  laevitas predictions catalog --category crypto -n 50`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := catalogFlags.CommonFlags.ToParams()
		params.Start = ""
		params.End = ""
		params.Resolution = ""
		params.SortDir = ""
		params.Exchange = predictionsExchange()
		params.Category = catalogFlags.Category
		params.EventSlug = catalogFlags.EventSlug
		params.Keyword = catalogFlags.Keyword
		cmdutil.RunAndPrint(client, api.PredictionsCatalog, params)
	},
}

var categoriesCmd = &cobra.Command{
	Use:   "categories",
	Short: "List all prediction market categories with counts",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		cmdutil.RunAndPrint(client, api.PredictionsCategories, nil)
	},
}

var snapshotFlags struct {
	Category       string
	EventSlug      string
	Keyword        string
	InstrumentName string
	Date           string
	Resolution     string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Point-in-time snapshot of all prediction instruments",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:       predictionsExchange(),
			Category:       snapshotFlags.Category,
			EventSlug:      snapshotFlags.EventSlug,
			Keyword:        snapshotFlags.Keyword,
			InstrumentName: snapshotFlags.InstrumentName,
			Date:           snapshotFlags.Date,
			Resolution:     snapshotFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.PredictionsSnapshot, params)
	},
}

var ohlcvtFlags cmdutil.CommonFlags

var ohlcvtCmd = &cobra.Command{
	Use:   "ohlcvt <instrument>",
	Short: "Probability OHLCVT candle data (prices = 0.0-1.0)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := ohlcvtFlags.ToParams()
		params.Exchange = predictionsExchange()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PredictionsOHLCVT, params)
	},
}

var tradesFlags cmdutil.CommonFlags

var tradesCmd = &cobra.Command{
	Use:   "trades <instrument>",
	Short: "Individual prediction market trades",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesFlags.ToParams()
		params.Exchange = predictionsExchange()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PredictionsTrades, params)
	},
}

var tickerFlags cmdutil.CommonFlags

var tickerCmd = &cobra.Command{
	Use:   "ticker <instrument>",
	Short: "Historical ticker — probability, bid/ask, spread, liquidity",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tickerFlags.ToParams()
		params.Exchange = predictionsExchange()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PredictionsTickerHistory, params)
	},
}

var orderbookFlags struct {
	cmdutil.CommonFlags
	output.BookFilterFlags
}

var orderbookCmd = &cobra.Command{
	Use:   "orderbook <instrument>",
	Short: "Raw L2 orderbook snapshots",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.CommonFlags.ToParams()
		params.Exchange = predictionsExchange()
		params.InstrumentName = args[0]
		cmdutil.ApplySnapshotDefaults(params)
		cmdutil.RunAndPrintFiltered(client, api.PredictionsOrderbookRaw, params, orderbookFlags.BookFilterFlags)
	},
}

var metadataCmd = &cobra.Command{
	Use:   "metadata <instrument>",
	Short: "Data availability info",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:       predictionsExchange(),
			InstrumentName: args[0],
		}
		cmdutil.RunAndPrint(client, api.PredictionsMetadata, params)
	},
}

func init() {
	cmdutil.AddCommonFlags(catalogCmd, &catalogFlags.CommonFlags)
	catalogCmd.Flags().StringVar(&catalogFlags.Category, "category", "", "Filter by category")
	catalogCmd.Flags().StringVar(&catalogFlags.EventSlug, "event", "", "Filter by event slug")
	catalogCmd.Flags().StringVar(&catalogFlags.Keyword, "keyword", "", "Keyword search")

	snapshotCmd.Flags().StringVar(&snapshotFlags.Category, "category", "", "Filter by category")
	snapshotCmd.Flags().StringVar(&snapshotFlags.EventSlug, "event", "", "Filter by event slug")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Keyword, "keyword", "", "Keyword search across instrument names")
	snapshotCmd.Flags().StringVar(&snapshotFlags.InstrumentName, "instrument", "", "Filter to a specific instrument name (case-insensitive exact)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	snapshotCmd.Flags().StringVarP(&snapshotFlags.Resolution, "resolution", "r", "1h", "Resolution")

	cmdutil.AddCommonFlags(ohlcvtCmd, &ohlcvtFlags)
	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags.CommonFlags)
	output.AddBookFilterFlags(orderbookCmd, &orderbookFlags.BookFilterFlags)

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(categoriesCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(ohlcvtCmd)
	Cmd.AddCommand(tradesCmd)
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(orderbookCmd)
	Cmd.AddCommand(metadataCmd)
}
