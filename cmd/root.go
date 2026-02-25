package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/laevitas/cli/cmd/config"
	"github.com/laevitas/cli/cmd/futures"
	"github.com/laevitas/cli/cmd/options"
	"github.com/laevitas/cli/cmd/perps"
	"github.com/laevitas/cli/cmd/predictions"
	"github.com/laevitas/cli/cmd/volsurface"
	"github.com/laevitas/cli/internal/cmdutil"
	internalConfig "github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/version"
)

var (
	outputFormat string
	exchange     string
	verbose      bool
	noChart      bool
)

var rootCmd = &cobra.Command{
	Use:   "laevitas",
	Short: "LAEVITAS — crypto derivatives analytics from your terminal",
	Long: `LAEVITAS CLI provides real-time access to crypto derivatives data
including futures, perpetuals, options, volatility surfaces, and prediction markets.

Data sourced from Deribit, Binance, and Polymarket.

  Authenticate:  laevitas config init
  Quick start:   laevitas futures snapshot --currency BTC
  Agent mode:    laevitas perps carry BTCUSDT -o json | jq '.[0]'
  Interactive:   laevitas   (no arguments → REPL shell)

Documentation:  https://docs.laevitas.ch/cli
API Reference:  https://docs.laevitas.ch/api`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version.Version, version.CommitSHA, version.BuildDate),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		switch outputFormat {
		case "auto", "json", "table", "csv":
		default:
			fmt.Fprintf(os.Stderr, "Invalid output format: %s (use: auto, json, table, csv)\n", outputFormat)
			os.Exit(1)
		}
		// Push globals into cmdutil so subcommands can access them
		cmdutil.OutputFormat = outputFormat
		if exchange != "" {
			cmdutil.Exchange = exchange
		} else if cmdutil.Exchange == "" {
			// Fall back to config default so API always gets an exchange param
			cmdutil.Exchange = internalConfig.DefaultExchange
		}
		cmdutil.Verbose = verbose
		cmdutil.NoChart = noChart
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	// Set Run here (not in the var declaration) to avoid an init cycle
	// between rootCmd and runInteractive, which references rootCmd.
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		if err := runInteractive(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
			os.Exit(1)
		}
	}

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", internalConfig.DefaultOutput, "Output format: auto, json, table, csv")
	rootCmd.PersistentFlags().StringVar(&exchange, "exchange", "", "Exchange (deribit, binance). Overrides config default.")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Show full HTTP request/response for debugging")
	rootCmd.PersistentFlags().BoolVar(&noChart, "no-chart", false, "Disable inline charts for time-series data")

	rootCmd.AddCommand(config.Cmd)
	rootCmd.AddCommand(futures.Cmd)
	rootCmd.AddCommand(perps.Cmd)
	rootCmd.AddCommand(options.Cmd)
	rootCmd.AddCommand(volsurface.Cmd)
	rootCmd.AddCommand(predictions.Cmd)
	rootCmd.AddCommand(watchCmd)
}

func Execute() error {
	return rootCmd.Execute()
}
