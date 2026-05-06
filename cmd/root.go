package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/laevitas/cli/cmd/analytics"
	"github.com/laevitas/cli/cmd/config"
	"github.com/laevitas/cli/cmd/dash"
	"github.com/laevitas/cli/cmd/futures"
	"github.com/laevitas/cli/cmd/instruments"
	"github.com/laevitas/cli/cmd/options"
	"github.com/laevitas/cli/cmd/perps"
	"github.com/laevitas/cli/cmd/predictions"
	"github.com/laevitas/cli/cmd/spot"
	"github.com/laevitas/cli/cmd/update"
	"github.com/laevitas/cli/cmd/wallet"
	wscmd "github.com/laevitas/cli/cmd/ws"
	"github.com/laevitas/cli/internal/cmdutil"
	internalConfig "github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/version"
)

var (
	outputFormat  string
	exchange      string
	verbose       bool
	noChart       bool
	wide          bool
	widthOverride int
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
	Long: `LAEVITAS CLI provides real-time access to crypto market data
including futures, perpetuals, options, spot, volatility surfaces,
prediction markets, analytics, instruments, and live streams.

Data sourced from Laevitas REST and WebSocket APIs across supported venues.

  Authenticate:  laevitas config init
  Quick start:   laevitas futures snapshot --currency BTC
  Agent mode:    laevitas perps carry BTCUSDT -o json | jq '.data[0]'
  Discover:      laevitas commands -o json    (full command manifest)
  Diagnose:      laevitas doctor              (auth + API + WS health check)
  Interactive:   laevitas   (no arguments → REPL shell, humans only —
                            agents/scripts: pipe subcommands directly)

CLI install + docs:  https://cli.laevitas.ch
REST API (Swagger):  https://apiv2.laevitas.ch/swagger
WebSocket protocol:  https://apiv2.laevitas.ch/websocket/`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version.Version, version.CommitSHA, version.BuildDate),
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		switch outputFormat {
		case "auto", "json", "table", "csv":
		default:
			fmt.Fprintf(os.Stderr, "Invalid output format: %s (use: auto, json, table, csv)\n", outputFormat)
			os.Exit(1)
		}
		// Push globals into cmdutil so subcommands can access them.
		cmdutil.OutputFormat = outputFormat
		// `cmdutil.Exchange` already carries the config-file default
		// (loaded by cmdutil's config bootstrap). The user can override
		// or clear it via --exchange / LAEVITAS_EXCHANGE; the
		// ExchangeExplicit flag tells cross-product endpoints whether
		// this came from the user or just from the default. We treat
		// `--exchange ""` as "user explicitly asked to clear it" — the
		// previous code dropped that case silently which made it
		// impossible to scope a query across all venues from the CLI.
		if cmd.Flags().Changed("exchange") || os.Getenv("LAEVITAS_EXCHANGE") != "" {
			cmdutil.Exchange = exchange
			cmdutil.ExchangeExplicit = true
		}
		cmdutil.Verbose = verbose
		cmdutil.NoChart = noChart
		// Width override: --wide takes precedence over --width
		if wide {
			output.WidthOverride = 0
		} else if widthOverride > 0 {
			output.WidthOverride = widthOverride
		}
	},
	SuggestionsMinimumDistance: 2,
	SilenceUsage:               true,
	SilenceErrors:              true,
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

	// Branded help template — banner + section headers in the brand
	// palette (#46be52 green wordmark, mid-grey footer links).
	//
	// The template applies ALWAYS (TTY or not) so the footer URLs
	// (Docs / API / WebSocket / x402 / Changelog / Discord / Twitter)
	// stay visible to agents and tool wrappers that capture --help
	// output. ANSI colour codes are only emitted when stdout is a real
	// terminal — non-TTY callers get the same content without escape
	// noise. Previous gating skipped the entire template on non-TTY,
	// which made the footer invisible to agents reading help via
	// `laevitas --help | cat` or similar.
	bg, bgl, bgm, reset := "", "", "", ""
	if term.IsTerminal(int(os.Stdout.Fd())) {
		bg = output.BrandGreen
		bgl = output.BrandGreyLight
		bgm = output.BrandGreyMid
		reset = output.Reset
	}
	rootCmd.SetUsageTemplate(bg + helpBanner + reset + bgm + `            v` + version.Version + reset + `

` + bgl + `USAGE:` + reset + `
  {{.UseLine}}{{if .HasAvailableSubCommands}} [command]{{end}}

` + bgl + `COMMANDS:` + reset + `{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}
{{if .HasAvailableLocalFlags}}
` + bgl + `FLAGS:` + reset + `
{{.LocalFlags.FlagUsages}}{{end}}{{if .HasAvailableInheritedFlags}}
` + bgl + `GLOBAL FLAGS:` + reset + `
{{.InheritedFlags.FlagUsages}}{{end}}
Use "{{.CommandPath}} [command] --help" for more info.

` + bgm + `Docs:       https://cli.laevitas.ch
API:        https://apiv2.laevitas.ch          (Swagger: /swagger · Redoc: /redoc)
WebSocket:  https://apiv2.laevitas.ch/websocket/
x402:       https://apiv2.laevitas.ch/x402/
Changelog:  https://apiv2.laevitas.ch/changelog.html
Discord:    https://discord.com/invite/yaXc4EFFay
Twitter:    https://twitter.com/laevitas1` + reset + `
`)

	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", internalConfig.DefaultOutput, "Output format: auto, json, table, csv")
	rootCmd.PersistentFlags().StringVar(&exchange, "exchange", "", "Exchange filter/override (market-dependent).")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "Show full HTTP request/response for debugging")
	rootCmd.PersistentFlags().BoolVar(&noChart, "no-chart", false, "Disable inline charts for time-series data")
	rootCmd.PersistentFlags().BoolVar(&wide, "wide", false, "Disable column truncation (show all data)")
	rootCmd.PersistentFlags().IntVar(&widthOverride, "width", 0, "Override terminal width for table formatting")

	rootCmd.AddCommand(config.Cmd)
	rootCmd.AddCommand(futures.Cmd)
	rootCmd.AddCommand(perps.Cmd)
	rootCmd.AddCommand(options.Cmd)
	rootCmd.AddCommand(spot.Cmd)
	rootCmd.AddCommand(predictions.Cmd)
	rootCmd.AddCommand(instruments.Cmd)
	rootCmd.AddCommand(analytics.Cmd)
	rootCmd.AddCommand(wallet.Cmd)
	rootCmd.AddCommand(wscmd.Cmd)
	rootCmd.AddCommand(dash.Cmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(update.Cmd)

	// Replace {{FUT}}/{{OPT_C}}/{{OPT_P}}/{{MAT}} tokens in help text with
	// instrument names computed from time.Now() so examples never go stale.
	cmdutil.SubstituteExamplesRecursive(rootCmd)
}

func Execute() error {
	err := executeRootCommand(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %s\n", err)
	}
	return err
}

func executeRootCommand(args []string) error {
	err := rootCmd.Execute()
	if err != nil {
		return augmentUnknownCommandError(err, args)
	}
	return nil
}

func augmentUnknownCommandError(err error, args []string) error {
	msg := err.Error()
	if strings.Contains(msg, "Did you mean") {
		return err
	}
	if strings.HasPrefix(msg, "unknown flag: ") {
		return augmentUnknownFlagAfterCommandTypo(err, args)
	}
	if !strings.HasPrefix(msg, "unknown command ") {
		return err
	}
	parts := strings.Split(msg, "\"")
	if len(parts) < 2 {
		return err
	}
	token := parts[1]
	suggestions := nestedCommandSuggestions(rootCmd, token, rootCmd.SuggestionsMinimumDistance)
	if len(suggestions) == 0 {
		return err
	}
	return fmt.Errorf("%s\n\nDid you mean this?\n\t%s", msg, strings.Join(suggestions, "\n\t"))
}

func augmentUnknownFlagAfterCommandTypo(err error, args []string) error {
	token, parent := firstUnknownCommandToken(rootCmd, args)
	if token == "" || parent == nil {
		return err
	}
	suggestions := siblingCommandSuggestions(parent, token, rootCmd.SuggestionsMinimumDistance)
	if len(suggestions) == 0 {
		return err
	}
	return fmt.Errorf("%s\n\nDid you mean this?\n\t%s", err.Error(), strings.Join(suggestions, "\n\t"))
}

func firstUnknownCommandToken(parent *cobra.Command, args []string) (string, *cobra.Command) {
	current := parent
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		child := findChildCommand(current, arg)
		if child == nil {
			return arg, current
		}
		current = child
	}
	return "", nil
}

func findChildCommand(parent *cobra.Command, token string) *cobra.Command {
	for _, child := range parent.Commands() {
		if child.Hidden {
			continue
		}
		if child.Name() == token {
			return child
		}
		for _, alias := range child.Aliases {
			if alias == token {
				return child
			}
		}
	}
	return nil
}

func siblingCommandSuggestions(parent *cobra.Command, token string, maxDistance int) []string {
	if maxDistance <= 0 {
		maxDistance = 2
	}
	seen := map[string]bool{}
	var out []string
	for _, child := range parent.Commands() {
		if child.Hidden {
			continue
		}
		name := child.Name()
		if name != "" && editDistance(strings.ToLower(token), strings.ToLower(name)) <= maxDistance && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		for _, alias := range child.Aliases {
			if editDistance(strings.ToLower(token), strings.ToLower(alias)) <= maxDistance && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func nestedCommandSuggestions(cmd *cobra.Command, token string, maxDistance int) []string {
	if maxDistance <= 0 {
		maxDistance = 2
	}
	seen := map[string]bool{}
	var out []string
	var walk func(*cobra.Command)
	walk = func(parent *cobra.Command) {
		for _, child := range parent.Commands() {
			if child.Hidden {
				continue
			}
			name := child.Name()
			if name != "" && editDistance(strings.ToLower(token), strings.ToLower(name)) <= maxDistance && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
			for _, alias := range child.Aliases {
				if editDistance(strings.ToLower(token), strings.ToLower(alias)) <= maxDistance && !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
			walk(child)
		}
	}
	walk(cmd)
	sort.Strings(out)
	return out
}

func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
