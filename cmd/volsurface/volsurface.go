package volsurface

import (
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
)

var Cmd = &cobra.Command{
	Use:     "vol-surface",
	Aliases: []string{"vol", "vs"},
	Short:   "Volatility surface — ATM IV, skew, butterfly, term structure",
	Long: `Access volatility surface data: ATM implied volatility, 25-delta skew,
butterfly spreads, and interpolated term structure.

Examples:
  laevitas vol-surface snapshot --currency BTC
  laevitas vol-surface term-structure --currency BTC
  laevitas vol-surface history --currency BTC --maturity 28MAR25 -r 1h`,
}

var snapshotFlags struct {
	Currency   string
	Date       string
	Resolution string
}

var snapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Vol surface across ALL maturities at a point in time",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:   cmdutil.Exchange,
			Currency:   snapshotFlags.Currency,
			Date:       snapshotFlags.Date,
			Resolution: snapshotFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.VolSurfaceSnapshot, params)
	},
}

var tsFlags struct {
	Currency   string
	Date       string
	Resolution string
}

var tsCmd = &cobra.Command{
	Use:     "term-structure",
	Aliases: []string{"ts"},
	Short:   "Interpolated constant-maturity term structure (1d to 365d)",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := &api.RequestParams{
			Exchange:   cmdutil.Exchange,
			Currency:   tsFlags.Currency,
			Date:       tsFlags.Date,
			Resolution: tsFlags.Resolution,
		}
		cmdutil.RunAndPrint(client, api.VolSurfaceTermStructure, params)
	},
}

var histFlags struct {
	cmdutil.CommonFlags
	Maturity string
}

var histCmd = &cobra.Command{
	Use:   "history",
	Short: "Historical vol surface data for a specific maturity",
	Run: func(cmd *cobra.Command, args []string) {
		client, _ := cmdutil.MustClient()
		params := histFlags.CommonFlags.ToParams()
		params.Maturity = histFlags.Maturity
		cmdutil.RunAndPrint(client, api.VolSurfaceHistory, params)
	},
}

func init() {
	snapshotCmd.Flags().StringVar(&snapshotFlags.Currency, "currency", "", "Base currency (required)")
	snapshotCmd.Flags().StringVar(&snapshotFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	snapshotCmd.Flags().StringVarP(&snapshotFlags.Resolution, "resolution", "r", "1m", "Resolution")
	_ = snapshotCmd.MarkFlagRequired("currency")

	tsCmd.Flags().StringVar(&tsFlags.Currency, "currency", "", "Base currency (required)")
	tsCmd.Flags().StringVar(&tsFlags.Date, "date", "", "Snapshot datetime (ISO 8601)")
	tsCmd.Flags().StringVarP(&tsFlags.Resolution, "resolution", "r", "1m", "Resolution")
	_ = tsCmd.MarkFlagRequired("currency")

	cmdutil.AddCommonFlags(histCmd, &histFlags.CommonFlags)
	histCmd.Flags().StringVar(&histFlags.Maturity, "maturity", "", "Maturity (required, e.g. 28MAR25)")
	_ = histCmd.MarkFlagRequired("currency")
	_ = histCmd.MarkFlagRequired("maturity")

	Cmd.AddCommand(snapshotCmd)
	Cmd.AddCommand(tsCmd)
	Cmd.AddCommand(histCmd)
}
