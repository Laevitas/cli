package cmdutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/briandowns/spinner"
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
)

// ─── Global state (set by root command) ─────────────────────────────────────

var (
	OutputFormat string
	Exchange     string
	Verbose      bool
	NoChart      bool

	// InteractiveMode is true when running inside the REPL.
	// Commands should avoid os.Exit and return errors instead.
	InteractiveMode bool

	// SharedClient is the persistent API client used in REPL mode.
	SharedClient *api.Client

	// SpinnerInstance is the active spinner during REPL command execution.
	SpinnerInstance *spinner.Spinner
)

// ─── Common flags for time-series commands ──────────────────────────────────

// CommonFlags holds flags shared across data commands.
type CommonFlags struct {
	Start      string
	End        string
	Resolution string
	Limit      int
	Cursor     string
	Currency   string
}

// AddCommonFlags registers the shared flags on a command.
func AddCommonFlags(cmd *cobra.Command, f *CommonFlags) {
	cmd.Flags().StringVar(&f.Start, "start", "", "Start datetime (ISO 8601, e.g. 2025-01-15T00:00:00Z)")
	cmd.Flags().StringVar(&f.End, "end", "", "End datetime (ISO 8601)")
	cmd.Flags().StringVarP(&f.Resolution, "resolution", "r", "", "Candle resolution: 1m, 5m, 15m, 1h, 4h, 1d")
	cmd.Flags().IntVarP(&f.Limit, "limit", "n", 0, "Number of records (1-1000)")
	cmd.Flags().StringVar(&f.Cursor, "cursor", "", "Pagination cursor from previous response")
	cmd.Flags().StringVar(&f.Currency, "currency", "", "Base currency filter (BTC, ETH)")
}

// ToParams converts common flags into API request params.
func (f *CommonFlags) ToParams() *api.RequestParams {
	p := &api.RequestParams{
		Start:      f.Start,
		End:        f.End,
		Resolution: f.Resolution,
		Limit:      f.Limit,
		Cursor:     f.Cursor,
		Currency:   f.Currency,
	}
	if Exchange != "" {
		p.Exchange = Exchange
	}
	return p
}

// ─── Client / Printer helpers ───────────────────────────────────────────────

// MustClient loads config and creates an API client, exiting on error.
// In interactive mode, it reuses the shared persistent client.
// If no API key is configured, it runs a friendly onboarding prompt.
func MustClient() (*api.Client, *config.Config) {
	cfg, err := config.Load()
	if err != nil {
		output.Errorf("Loading config: %s", err)
		if !InteractiveMode {
			os.Exit(1)
		}
		return nil, nil
	}

	if cfg.APIKey == "" {
		if !promptOnboarding(cfg) {
			if !InteractiveMode {
				os.Exit(1)
			}
			return nil, nil
		}
	}

	// Reuse persistent client in REPL mode
	if InteractiveMode && SharedClient != nil {
		SharedClient.Verbose = Verbose
		return SharedClient, cfg
	}

	client := api.NewClient(cfg)
	client.Verbose = Verbose
	return client, cfg
}

// promptOnboarding runs the first-run API key setup inline.
// Returns true if a key was successfully configured.
func promptOnboarding(cfg *config.Config) bool {
	fmt.Println()
	fmt.Println("  Welcome to LAEVITAS CLI! You need an API key to get started.")
	fmt.Println("  Get your key at \033[1mhttps://app.laevitas.ch/settings/api\033[0m")
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("  Paste your API key: ")
	key, err := reader.ReadString('\n')
	if err != nil {
		output.Errorf("Reading input: %s", err)
		return false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		output.Errorf("No API key provided.")
		return false
	}

	cfg.APIKey = key
	if err := config.Save(cfg); err != nil {
		output.Errorf("Saving config: %s", err)
		return false
	}

	output.Successf("API key saved to ~/.config/laevitas/config.json")
	fmt.Println()
	return true
}

// MustPrinter returns a printer configured from global flags.
func MustPrinter() *output.Printer {
	return output.NewPrinter(OutputFormat)
}

// Itoa converts int to string.
func Itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// Ftoa converts float64 to string.
func Ftoa(f float64) string {
	return fmt.Sprintf("%.0f", f)
}

// RunAndPrint fetches data, prints it, and handles errors.
func RunAndPrint(client *api.Client, endpoint string, params *api.RequestParams) {
	p := MustPrinter()

	// Start spinner in interactive mode
	if InteractiveMode && SpinnerInstance != nil {
		SpinnerInstance.Start()
	}

	data, err := client.Get(endpoint, params)

	// Stop spinner before printing output
	if InteractiveMode && SpinnerInstance != nil {
		SpinnerInstance.Stop()
	}

	if err != nil {
		output.PrintError(p.Format, err)
		if !InteractiveMode {
			os.Exit(1)
		}
		return
	}

	// Extract total count from API response metadata for the footer
	if p.Format == output.FormatTable {
		var wrapper struct {
			Count int `json:"count"`
			Meta  *struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if json.Unmarshal(data, &wrapper) == nil {
			if wrapper.Meta != nil && wrapper.Meta.Total > 0 {
				p.TotalCount = wrapper.Meta.Total
			} else if wrapper.Count > 0 {
				p.TotalCount = wrapper.Count
			}
		}
	}

	if err := p.Print(data); err != nil {
		output.Errorf("Formatting output: %s", err)
		if !InteractiveMode {
			os.Exit(1)
		}
		return
	}

	// Render inline chart for time-series data in table mode
	if p.Format == output.FormatTable && !NoChart {
		if col, caption := output.ChartableEndpoint(endpoint); col != "" {
			output.RenderChart(p.Writer, data, col, caption)
		}
	}

	// Show pagination hint for table/csv output
	if p.Format != output.FormatJSON {
		var wrapper struct {
			Meta *struct {
				NextCursor string `json:"next_cursor"`
			} `json:"meta"`
			NextCursor string `json:"next_cursor"`
		}
		if json.Unmarshal(data, &wrapper) == nil {
			cursor := ""
			if wrapper.Meta != nil && wrapper.Meta.NextCursor != "" {
				cursor = wrapper.Meta.NextCursor
			} else if wrapper.NextCursor != "" {
				cursor = wrapper.NextCursor
			}
			if cursor != "" {
				fmt.Fprintf(os.Stderr, "\n→ More results available. Use --cursor %q\n", cursor)
			}
		}
	}
}
