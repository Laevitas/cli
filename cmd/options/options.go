package options

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

var Cmd = &cobra.Command{
	Use:   "options",
	Short: "Options data — flow, trades, volatility, Greeks, OI, vol-surface",
	Long: `Access options data from Deribit and Binance.

Examples:
  laevitas options catalog
  laevitas options snapshot --currency BTC
  laevitas options flow --currency BTC --min-premium 5000
  laevitas options trades --currency ETH --direction buy --block-only
  laevitas options volatility BTC-27MAR26-70000-C -p 24h
  laevitas options ohlcvt BTC-27MAR26-70000-C -p 3d -r 1h
  laevitas options vol-surface snapshot --currency BTC`,
}

var catalogCmd = &cobra.Command{
	Use:   "catalog",
	Short: "List all available options instruments",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{Exchange: cmdutil.Exchange}
		cmdutil.RunAndPrint(client, api.OptionsCatalog, params)
	},
}

var snapshotFlags struct {
	Currency string
	Date     string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Full options chain snapshot — all strikes, maturities, Greeks",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange: cmdutil.Exchange,
			Currency: snapshotFlags.Currency,
			Date:     snapshotFlags.Date,
		}
		cmdutil.RunAndPrint(client, api.OptionsSnapshot, params)
	},
}

var flowFlags struct {
	Currency   string
	Start      string
	End        string
	MinPremium float64
	TopN       int
}

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Aggregated options flow summary — premium, Greeks, notable trades",
	Example: `  laevitas options flow --currency BTC
  laevitas options flow --currency BTC --min-premium 10000 --top-n 20`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:   cmdutil.Exchange,
			Currency:   flowFlags.Currency,
			Start:      flowFlags.Start,
			End:        flowFlags.End,
			MinPremium: flowFlags.MinPremium,
			Extra:      map[string]string{},
		}
		if flowFlags.TopN > 0 {
			params.Extra["top_n"] = cmdutil.Itoa(flowFlags.TopN)
		}
		cmdutil.RunAndPrint(client, api.OptionsFlow, params)
	},
}

var tradesFlags struct {
	cmdutil.CommonFlags
	InstrumentName string
	Direction      string
	OptionType     string
	Maturity       string
	MinPremium     float64
	MinNotional    float64
	Sort           string
	SortDir        string
	BlockOnly      bool
	OpeningOnly    bool
}

var tradesCmd = &cobra.Command{
	Use:   "trades",
	Short: "Individual options trades with full Greeks and details",
	Long: `Fetch options trades. Provide either --instrument for a specific option,
or --currency for cross-instrument flow (max 7-day window).`,
	Example: `  laevitas options trades --currency BTC --min-premium 5000
  laevitas options trades --instrument BTC-28MAR25-100000-C
  laevitas options trades --currency ETH --direction buy --block-only`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tradesFlags.CommonFlags.ToParams()
		params.InstrumentName = tradesFlags.InstrumentName
		params.Direction = tradesFlags.Direction
		params.OptionType = tradesFlags.OptionType
		params.Maturity = tradesFlags.Maturity
		params.MinPremium = tradesFlags.MinPremium
		params.Sort = tradesFlags.Sort
		params.SortDir = tradesFlags.SortDir
		params.BlockOnly = tradesFlags.BlockOnly
		params.OpeningOnly = tradesFlags.OpeningOnly
		if tradesFlags.MinNotional > 0 {
			if params.Extra == nil {
				params.Extra = map[string]string{}
			}
			params.Extra["min_notional"] = cmdutil.Ftoa(tradesFlags.MinNotional)
		}
		cmdutil.RunAndPrint(client, api.OptionsTrades, params)
	},
}

var ohlcvFlags cmdutil.CommonFlags

var ohlcvCmd = &cobra.Command{
	Use:     "ohlcvt <instrument>",
	Aliases: []string{"ohlcv"},
	Short:   "OHLCVT candle data for a specific option",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := ohlcvFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsOHLCVT, params)
	},
}

var oiFlags cmdutil.CommonFlags

var oiCmd = &cobra.Command{
	Use:     "oi <instrument>",
	Aliases: []string{"open-interest"},
	Short:   "Open interest for a specific option over time",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := oiFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsOpenInterest, params)
	},
}

var volFlags cmdutil.CommonFlags

var volCmd = &cobra.Command{
	Use:   "volatility <instrument>",
	Short: "Implied volatility and Greeks for a specific option",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := volFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsVolatility, params)
	},
}

var level1Flags cmdutil.CommonFlags

var level1Cmd = &cobra.Command{
	Use:  "level1 <instrument>",
	Short: "Best bid/ask data for an option",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := level1Flags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsLevel1, params)
	},
}

var tickerFlags cmdutil.CommonFlags

var tickerCmd = &cobra.Command{
	Use:  "ticker <instrument>",
	Short: "Historical ticker — IV surface, Greeks, OI by strike",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := tickerFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsTickerHistory, params)
	},
}

var volumeFlags cmdutil.CommonFlags

var volumeCmd = &cobra.Command{
	Use:  "volume <instrument>",
	Short: "24h rolling volume for an option",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := volumeFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsVolume, params)
	},
}

var refPriceFlags cmdutil.CommonFlags

var refPriceCmd = &cobra.Command{
	Use:  "ref-price <instrument>",
	Short: "Mark price and index price OHLC",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := refPriceFlags.ToParams()
		params.InstrumentName = args[0]
		cmdutil.RunAndPrint(client, api.OptionsReferencePrice, params)
	},
}

var metadataCmd = &cobra.Command{
	Use:  "metadata <instrument>",
	Short: "Data availability info",
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			InstrumentName: args[0],
			Exchange:       cmdutil.Exchange,
		}
		cmdutil.RunAndPrint(client, api.OptionsMetadata, params)
	},
}

// ─── Vol-Surface subcommand group (under options) ───────────────────────────

var VolSurfaceCmd = &cobra.Command{
	Use:     "vol-surface",
	Aliases: []string{"vol-surf", "vs"},
	Short:   "Volatility surface — ATM IV, skew, butterfly, term structure",
	Long: `Access volatility surface data: ATM implied volatility, 25-delta skew,
butterfly spreads, and interpolated term structure.

Examples:
  laevitas options vol-surface snapshot --currency BTC
  laevitas options vol-surface term-structure --currency BTC
  laevitas options vol-surface history --currency BTC --maturity 28MAR25 -r 1h`,
}

var vsSnapshotFlags struct {
	Currency   string
	Date       string
	Resolution string
}

var vsSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Vol surface across ALL maturities at a point in time",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:   cmdutil.Exchange,
			Currency:   vsSnapshotFlags.Currency,
			Date:       vsSnapshotFlags.Date,
			Resolution: vsSnapshotFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.VolSurfaceByExpiry, params)
	},
}

var vsTSFlags struct {
	Currency   string
	Date       string
	Resolution string
}

var vsTSCmd = &cobra.Command{
	Use:     "term-structure",
	Aliases: []string{"ts"},
	Short:   "Interpolated constant-maturity term structure (1d to 365d)",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:   cmdutil.Exchange,
			Currency:   vsTSFlags.Currency,
			Date:       vsTSFlags.Date,
			Resolution: vsTSFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.VolSurfaceByTenor, params)
	},
}

var vsHistFlags struct {
	cmdutil.CommonFlags
	Maturity string
}

var vsHistCmd = &cobra.Command{
	Use:   "history",
	Short: "Historical vol surface data for a specific maturity",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := vsHistFlags.CommonFlags.ToParams()
		params.Maturity = vsHistFlags.Maturity
		cmdutil.RunAndPrint(client, api.VolSurfaceByTime, params)
	},
}

func init() {
	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Base currency (required)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	_ = snapshotCmd.MarkFlagRequired("currency")

	flowCmd.Flags().StringVar(&flowFlags.Currency, "currency", "", "Base currency (required)")
	flowCmd.Flags().StringVar(&flowFlags.Start, "start", "", "Start datetime (ISO 8601)")
	flowCmd.Flags().StringVar(&flowFlags.End, "end", "", "End datetime (ISO 8601)")
	flowCmd.Flags().Float64Var(&flowFlags.MinPremium, "min-premium", 0, "Min premium USD per trade")
	flowCmd.Flags().IntVar(&flowFlags.TopN, "top-n", 10, "Number of notable trades / active strikes")
	_ = flowCmd.MarkFlagRequired("currency")

	cmdutil.AddCommonFlags(tradesCmd, &tradesFlags.CommonFlags)
	tradesCmd.Flags().StringVar(&tradesFlags.InstrumentName, "instrument", "", "Specific option instrument")
	tradesCmd.Flags().StringVar(&tradesFlags.Direction, "direction", "", "Filter: buy or sell")
	tradesCmd.Flags().StringVar(&tradesFlags.OptionType, "type", "", "Filter: C (call) or P (put)")
	tradesCmd.Flags().StringVar(&tradesFlags.Maturity, "maturity", "", "Filter by maturity (e.g. 28MAR25)")
	tradesCmd.Flags().Float64Var(&tradesFlags.MinPremium, "min-premium", 0, "Min premium USD")
	tradesCmd.Flags().Float64Var(&tradesFlags.MinNotional, "min-notional", 0, "Min notional USD")
	tradesCmd.Flags().StringVar(&tradesFlags.Sort, "sort", "", "Sort: timestamp, premium_usd, notional, amount")
	tradesCmd.Flags().StringVar(&tradesFlags.SortDir, "sort-dir", "", "Sort direction: ASC or DESC")
	tradesCmd.Flags().BoolVar(&tradesFlags.BlockOnly, "block-only", false, "Only block trades")
	tradesCmd.Flags().BoolVar(&tradesFlags.OpeningOnly, "opening-only", false, "Only opening trades")

	cmdutil.AddCommonFlags(ohlcvCmd, &ohlcvFlags)
	cmdutil.AddCommonFlags(oiCmd, &oiFlags)
	cmdutil.AddCommonFlags(volCmd, &volFlags)
	cmdutil.AddCommonFlags(level1Cmd, &level1Flags)
	cmdutil.AddCommonFlags(tickerCmd, &tickerFlags)
	cmdutil.AddCommonFlags(volumeCmd, &volumeFlags)
	cmdutil.AddCommonFlags(refPriceCmd, &refPriceFlags)

	// Vol-surface sub-subcommands
	vsSnapshotCmd.Flags().StringVar(&vsSnapshotFlags.Currency, "currency", "", "Base currency (required)")
	vsSnapshotCmd.Flags().StringVar(&vsSnapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	vsSnapshotCmd.Flags().StringVarP(&vsSnapshotFlags.Resolution, "resolution", "r", "1m", "Resolution")
	_ = vsSnapshotCmd.MarkFlagRequired("currency")

	vsTSCmd.Flags().StringVar(&vsTSFlags.Currency, "currency", "", "Base currency (required)")
	vsTSCmd.Flags().StringVar(&vsTSFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	vsTSCmd.Flags().StringVarP(&vsTSFlags.Resolution, "resolution", "r", "1m", "Resolution")
	_ = vsTSCmd.MarkFlagRequired("currency")

	cmdutil.AddCommonFlags(vsHistCmd, &vsHistFlags.CommonFlags)
	vsHistCmd.Flags().StringVar(&vsHistFlags.Maturity, "maturity", "", "Maturity (required, e.g. 28MAR25)")
	_ = vsHistCmd.MarkFlagRequired("currency")
	_ = vsHistCmd.MarkFlagRequired("maturity")

	VolSurfaceCmd.AddCommand(vsSnapshotCmd)
	VolSurfaceCmd.AddCommand(vsTSCmd)
	VolSurfaceCmd.AddCommand(vsHistCmd)

	Cmd.AddCommand(catalogCmd)
	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(flowCmd)
	Cmd.AddCommand(tradesCmd)
	Cmd.AddCommand(ohlcvCmd)
	Cmd.AddCommand(oiCmd)
	Cmd.AddCommand(volCmd)
	Cmd.AddCommand(level1Cmd)
	Cmd.AddCommand(tickerCmd)
	Cmd.AddCommand(volumeCmd)
	Cmd.AddCommand(refPriceCmd)
	Cmd.AddCommand(metadataCmd)
	Cmd.AddCommand(VolSurfaceCmd)
}
