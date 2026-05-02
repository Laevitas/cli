package analytics

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

var Cmd = &cobra.Command{
	Use:   "analytics",
	Short: "Computed cross-asset analytics — realized volatility and derived metrics",
	Long: `Access precomputed cross-asset analytics from the Laevitas pipeline.

Examples:
  laevitas analytics realized-volatility --instrument {{FUT}} --window-days 30
  laevitas analytics rv --instrument BTC-PERPETUAL --estimator parkinson
  laevitas analytics rv --instrument BTC-PERPETUAL -p 30d -r 1d`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
}

// ─── realized-volatility ────────────────────────────────────────────────────

var rvFlags struct {
	cmdutil.CommonFlags
	InstrumentName string
	Frequency      string
	WindowDays     int
	Estimator      string
	Date           string
}

var rvCmd = &cobra.Command{
	Use:     "realized-volatility",
	Aliases: []string{"rv"},
	Short:   "Annualized realized volatility (snapshot or historical)",
	Long: `Returns annualised realised volatility metrics computed from precomputed
cross-asset analytics. Two modes:

  • Snapshot mode (no --start/--end/--period): returns the latest available
    realized vol for the instrument (and the chosen window/estimator).
  • Historical mode (any of --start, --end, --period set): returns a
    paginated time-series.

Values are expressed as annualised percentages — e.g. 38.76 means 38.76%.

Estimators:
  close_to_close  — classic standard deviation of log returns (default behaviour upstream)
  parkinson       — uses high/low ranges; lower variance estimator
  garman_klass    — uses OHLC; even tighter for clean data

Window days: 7, 30, 60, 90, 180, 365.
Frequency:   daily, hourly.`,
	Example: `  laevitas analytics rv --instrument {{FUT}} --window-days 30
  laevitas analytics rv --instrument BTC-PERPETUAL --estimator parkinson --window-days 90
  laevitas analytics rv --instrument BTC-PERPETUAL --frequency hourly --window-days 7
  laevitas analytics rv --instrument BTC-PERPETUAL -p 30d -r 1d
  laevitas analytics rv --instrument BTC-PERPETUAL --start 2026-04-01T00:00:00Z --end 2026-04-27T00:00:00Z`,
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()

		// Decide snapshot vs historical from the flags the user actually set.
		historical := rvFlags.Period != "" || rvFlags.Start != "" || rvFlags.End != "" ||
			rvFlags.Limit > 0 || rvFlags.Cursor != ""

		var params *api.RequestParams
		if historical {
			params = rvFlags.CommonFlags.ToParams()
		} else {
			// Snapshot mode: do NOT compute a default start/end window. Send
			// nothing time-related so the API returns the latest snapshot.
			params = &api.RequestParams{}
			if cmdutil.Exchange != "" {
				params.Exchange = cmdutil.Exchange
			}
		}

		params.InstrumentName = rvFlags.InstrumentName
		params.Currency = rvFlags.Currency
		params.Frequency = rvFlags.Frequency
		params.WindowDays = rvFlags.WindowDays
		params.Estimator = rvFlags.Estimator

		// --date is snapshot-only; ignore in historical mode.
		if !historical && rvFlags.Date != "" {
			params.Date = rvFlags.Date
		}

		cmdutil.RunAndPrint(client, api.AnalyticsRealizedVolatility, params)
	},
}

func init() {
	cmdutil.AddCommonFlags(rvCmd, &rvFlags.CommonFlags)
	rvCmd.Flags().StringVar(&rvFlags.InstrumentName, "instrument", "", "Full instrument name (required, e.g. BTC-PERPETUAL)")
	rvCmd.Flags().StringVar(&rvFlags.Frequency, "frequency", "", "Sampling frequency: daily, hourly")
	rvCmd.Flags().IntVar(&rvFlags.WindowDays, "window-days", 0, "Lookback window: 7, 30, 60, 90, 180, 365")
	rvCmd.Flags().StringVar(&rvFlags.Estimator, "estimator", "", "Estimator: close_to_close, parkinson, garman_klass")
	rvCmd.Flags().StringVar(&rvFlags.Date, "date", "", "Snapshot point-in-time (ISO 8601, snapshot mode only)")
	_ = rvCmd.MarkFlagRequired("instrument")

	Cmd.AddCommand(rvCmd)
}
