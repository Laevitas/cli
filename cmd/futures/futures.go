package futures

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

var Cmd = &cobra.Command{
	Use:   "futures",
	Short: "Dated futures data — catalog, OHLCVT, OI, carry, trades",
	Long: `Access dated futures data from Deribit and Binance.

Examples:
  laevitas futures catalog
  laevitas futures snapshot --currency BTC
  laevitas futures ohlcvt BTC-27MAR26 -p 24h
  laevitas futures ohlcvt BTC-27MAR26 -p 3d -r 1h
  laevitas futures carry BTC-27MAR26 -p 7d
  laevitas futures oi BTC-27MAR26 -r 1d -n 30`,
}

// ─── catalog ────────────────────────────────────────────────────────────────

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List all available dated futures instruments",
	Example: `  laevitas futures catalog
  laevitas futures catalog --exchange binance`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{Exchange: cmdutil.Exchange}
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
	Example: `  laevitas futures ohlcvt BTC-27MAR26 -p 24h
  laevitas futures ohlcvt BTC-27MAR26 -p 3d -r 1h -n 50`,
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
	Example: `  laevitas futures oi BTC-27MAR26 -p 7d
  laevitas futures oi BTC-27MAR26 -p 30d -r 1d`,
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
	Example: `  laevitas futures carry BTC-27MAR26 -p 24h
  laevitas futures carry BTC-27MAR26 -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := carryFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesCarry, params)
	},
}

// ─── trades ─────────────────────────────────────────────────────────────────

var tradesFlags cmdutil.CommonFlags

var tradesCmd = &cobra.Command{
	Use:   "trades <instrument>",
	Short: "Individual trade records",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures trades BTC-27MAR26 -p 24h
  laevitas futures trades BTC-27MAR26 -p 1h -n 20`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesTrades, params)
	},
}

// ─── volume ─────────────────────────────────────────────────────────────────

var volumeFlags cmdutil.CommonFlags

var volumeCmd = &cobra.Command{
	Use:   "volume <instrument>",
	Short: "24h rolling volume data",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures volume BTC-27MAR26 -p 24h
  laevitas futures volume BTC-27MAR26 -p 7d -r 1h`,
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
	Example: `  laevitas futures level1 BTC-27MAR26 -p 24h
  laevitas futures level1 BTC-27MAR26 -p 3d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := level1Flags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesLevel1, params)
	},
}

// ─── orderbook ──────────────────────────────────────────────────────────────

var orderbookFlags cmdutil.CommonFlags

var orderbookCmd = &cobra.Command{
	Use:   "orderbook <instrument>",
	Short: "L2 orderbook depth metrics",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures orderbook BTC-27MAR26 -p 24h
  laevitas futures orderbook BTC-27MAR26 -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := orderbookFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesOrderbook, params)
	},
}

// ─── ticker ─────────────────────────────────────────────────────────────────

var tickerFlags cmdutil.CommonFlags

var tickerCmd = &cobra.Command{
	Use:   "ticker <instrument>",
	Short: "Historical ticker snapshots (mark price, OI, bid/ask, funding)",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures ticker BTC-27MAR26 -p 24h
  laevitas futures ticker BTC-27MAR26 -p 7d -r 1h`,
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
	Example: `  laevitas futures ref-price BTC-27MAR26 -p 24h
  laevitas futures ref-price BTC-27MAR26 -p 7d -r 1h`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := refPriceFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.FuturesReferencePrice, params)
	},
}

// ─── metadata ───────────────────────────────────────────────────────────────

var metadataCmd = &cobra.Command{
	Use:   "metadata <instrument>",
	Short: "Data availability info for a dated futures instrument",
	Args:  cobra.ExactArgs(1),
	Example: `  laevitas futures metadata BTC-27MAR26`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       cmdutil.Exchange,
		}
		cmdutil.RunAndPrint(client, api.FuturesMetadata, params)
	},
}

func init() {
	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Filter by currency (BTC, ETH)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")

	cmdutil.AddCommonFlags(ohlcvCmd, &ohlcvFlags)
	cmdutil.AddCommonFlags(oiCmd, &oiFlags)
	cmdutil.AddCommonFlags(carryCmd, &carryFlags)
	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags)
	cmdutil.AddCommonFlags(volumeCmd, &volumeFlags)
	cmdutil.AddCommonFlags(level1Cmd, &level1Flags)
	cmdutil.AddCommonFlags(orderbookCmd, &orderbookFlags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(refPriceCmd, &refPriceFlags)

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(ohlcvCmd)
	Cmd.AddCommand(oiCmd)
	Cmd.AddCommand(carryCmd)
	Cmd.AddCommand(tradesCmd)
	Cmd.AddCommand(volumeCmd)
	Cmd.AddCommand(level1Cmd)
	Cmd.AddCommand(orderbookCmd)
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(refPriceCmd)
	Cmd.AddCommand(metadataCmd)
}
