package predictions

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

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
	Category  string
	EventSlug string
	Keyword   string
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List available prediction market instruments",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Category:  catalogFlags.Category,
			EventSlug: catalogFlags.EventSlug,
			Keyword:   catalogFlags.Keyword,
		}
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
	Category   string
	EventSlug  string
	Date       string
	Resolution string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Point-in-time snapshot of all prediction instruments",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Category:   snapshotFlags.Category,
			EventSlug:  snapshotFlags.EventSlug,
			Date:       snapshotFlags.Date,
			Resolution: snapshotFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.PredictionsSnapshot, params)
	},
}

var ohlcvtFlags cmdutil.CommonFlags

var ohlcvtCmd = &cobra.Command{
	Use:   "ohlcvt <instrument>",
	Short: "Probability OHLCV candle data (prices = 0.0-1.0)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := ohlcvtFlags.ToParams()
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
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PredictionsTickerHistory, params)
	},
}

var orderbookFlags cmdutil.CommonFlags

var orderbookCmd = &cobra.Command{
	Use:   "orderbook <instrument>",
	Short: "Raw L2 orderbook snapshots",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PredictionsOrderbookRaw, params)
	},
}

var metadataCmd = &cobra.Command{
	Use:   "metadata <instrument>",
	Short: "Data availability info",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{InstrumentName: args[0]}
		cmdutil.RunAndPrint(client, api.PredictionsMetadata, params)
	},
}

func init() {
	catalogCmd.Flags().StringVar(&catalogFlags.Category, "category", "", "Filter by category")
	catalogCmd.Flags().StringVar(&catalogFlags.EventSlug, "event", "", "Filter by event slug")
	catalogCmd.Flags().StringVar(&catalogFlags.Keyword, "keyword", "", "Keyword search")

	snapshotCmd.Flags().StringVar(&snapshotFlags.Category, "category", "", "Filter by category")
	snapshotCmd.Flags().StringVar(&snapshotFlags.EventSlug, "event", "", "Filter by event slug")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	snapshotCmd.Flags().StringVarP(&snapshotFlags.Resolution, "resolution", "r", "1h", "Resolution")

	cmdutil.AddCommonFlags(ohlcvtCmd, &ohlcvtFlags)
	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags)

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(categoriesCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(ohlcvtCmd)
	Cmd.AddCommand(tradesCmd)
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(orderbookCmd)
	Cmd.AddCommand(metadataCmd)
}
