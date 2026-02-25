package perps

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

var Cmd = &cobra.Command{
	Use:   "perps",
	Short: "Perpetual swap data — carry, OHLCV, OI, trades",
	Long: `Access perpetual swap data from Deribit and Binance.

Examples:
  laevitas perps catalog
  laevitas perps carry BTC-PERPETUAL
  laevitas perps carry BTCUSDT --exchange binance -r 1d
  laevitas perps snapshot --currency BTC`,
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List all available perpetual instruments",
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
	Example: `  laevitas perps carry BTC-PERPETUAL
  laevitas perps carry BTCUSDT --exchange binance -r 1d -n 30
  laevitas perps carry ETH-PERPETUAL -o json | jq '.[].funding_rate_close'`,
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
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := oiFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsOpenInterest, params)
	},
}

var tradesFlags cmdutil.CommonFlags

var tradesCmd = &cobra.Command{
	Use:   "trades <instrument>",
	Short: "Individual trade records",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.PerpsTrades, params)
	},
}

var volumeFlags cmdutil.CommonFlags

var volumeCmd = &cobra.Command{
	Use:   "volume <instrument>",
	Short: "24h rolling volume data",
	Args:  cobra.ExactArgs(1),
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
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       cmdutil.Exchange,
		}
		cmdutil.RunAndPrint(client, api.PerpsMetadata, params)
	},
}

func init() {
	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Filter by currency (BTC, ETH)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")

	cmdutil.AddCommonFlags(carryCmd, &carryFlags)
	cmdutil.AddCommonFlags(ohlcvCmd, &ohlcvFlags)
	cmdutil.AddCommonFlags(oiCmd, &oiFlags)
	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags)
	cmdutil.AddCommonFlags(volumeCmd, &volumeFlags)
	cmdutil.AddCommonFlags(level1Cmd, &level1Flags)
	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(refPriceCmd, &refPriceFlags)

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
}
