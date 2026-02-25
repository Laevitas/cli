package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/laevitas/cli/cmd/config"
	"github.com/laevitas/cli/cmd/futures"
	"github.com/laevitas/cli/cmd/options"
	"github.com/laevitas/cli/cmd/perps"
	"github.com/laevitas/cli/cmd/predictions"
	"github.com/laevitas/cli/cmd/update"
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

const helpBanner = `  ██╗      █████╗ ███████╗██╗   ██╗██╗████████╗ █████╗ ███████╗
  ██║     ██╔══██╗██╔════╝██║   ██║██║╚══██╔══╝██╔══██╗██╔════╝
  ██║     ███████║█████╗  ██║   ██║██║   ██║   ███████║███████╗
  ██║     ██╔══██║██╔══╝  ╚██╗ ██╔╝██║   ██║   ██╔══██║╚════██║
  ███████╗██║  ██║███████╗ ╚████╔╝ ██║   ██║   ██║  ██║███████║
  ╚══════╝╚═╝  ╚═╝╚══════╝  ╚═══╝  ╚═╝   ╚═╝   ╚═╝  ╚═╝╚══════╝
  Derivatives Data Without The Spread`

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

Documentation:  https://apiv2.laevitas.ch/redoc
API Reference:  https://apiv2.laevitas.ch/redoc`,
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
		}
		cmdutil.Verbose = verbose
		cmdutil.NoChart = noChart
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version and build information",
	Run: func(cmd *cobra.Command, args []string) {
		dim := "\033[2m"
		reset := "\033[0m"
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			dim = ""
			reset = ""
		}
		fmt.Printf("laevitas v%s (build: %s, %s)\n", version.Version, version.CommitSHA, version.BuildDate)
		fmt.Printf("%sLaevitas Pte. Ltd. — https://www.laevitas.ch%s\n", dim, reset)
		fmt.Printf("%sAPI: https://apiv2.laevitas.ch%s\n", dim, reset)
	},
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

	// Branded help template — show banner + colored section headers on TTY
	if term.IsTerminal(int(os.Stdout.Fd())) {
		rootCmd.SetUsageTemplate("\033[36m" + helpBanner + "\033[0m" + `            v` + version.Version + `

` + "\033[36m" + `USAGE:` + "\033[0m" + `
  {{.UseLine}}{{if .HasAvailableSubCommands}} [command]{{end}}

` + "\033[36m" + `COMMANDS:` + "\033[0m" + `{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}
{{if .HasAvailableLocalFlags}}
` + "\033[36m" + `FLAGS:` + "\033[0m" + `
{{.LocalFlags.FlagUsages}}{{end}}{{if .HasAvailableInheritedFlags}}
` + "\033[36m" + `GLOBAL FLAGS:` + "\033[0m" + `
{{.InheritedFlags.FlagUsages}}{{end}}
Use "{{.CommandPath}} [command] --help" for more info.

` + "\033[2m" + `Docs:    https://apiv2.laevitas.ch/redoc
Discord: https://discord.com/invite/yaXc4EFFay
Twitter: https://twitter.com/laevitas1` + "\033[0m" + `
`)
	}

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", internalConfig.DefaultOutput, "Output format: auto, json, table, csv")
	rootCmd.PersistentFlags().StringVar(&exchange, "exchange", "", "Exchange (deribit, binance). Overrides config default.")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Show full HTTP request/response for debugging")
	rootCmd.PersistentFlags().BoolVar(&noChart, "no-chart", false, "Disable inline charts for time-series data")

	rootCmd.AddCommand(config.Cmd)
	rootCmd.AddCommand(futures.Cmd)
	rootCmd.AddCommand(perps.Cmd)
	rootCmd.AddCommand(options.Cmd)
	rootCmd.AddCommand(predictions.Cmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(update.Cmd)
}

func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s\n", err)
	}
	return err
}
