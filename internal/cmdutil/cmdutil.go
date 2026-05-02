package cmdutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/briandowns/spinner"
	"github.com/spf13/cobra"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/config"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/x402"
)

// ─── Global state (set by root command) ─────────────────────────────────────

var (
	OutputFormat string
	Exchange     string
	// ExchangeExplicit is true when the user explicitly passed --exchange
	// (or LAEVITAS_EXCHANGE). When false, Exchange holds the config
	// default (e.g. "deribit") that the user never asked for —
	// cross-product endpoints (catalogs, instruments registry) must
	// suppress sending it so the API returns every venue's listing.
	// Concrete-instrument commands (snapshot, ohlcvt, etc.) still need
	// some exchange so they fall back to the default unconditionally.
	ExchangeExplicit bool
	Verbose          bool
	NoChart          bool

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
	Period     string
	Start      string
	End        string
	Resolution string
	Limit      int
	Cursor     string
	Currency   string
	SortDir    string
}

// AddCommonFlags registers the shared flags on a command.
func AddCommonFlags(cmd *cobra.Command, f *CommonFlags) {
	cmd.Flags().StringVarP(&f.Period, "period", "p", "", "Lookback period: 1h, 6h, 24h, 3d, 7d, 30d (default 7d)")
	cmd.Flags().StringVar(&f.Start, "start", "", "Start datetime (ISO 8601)")
	cmd.Flags().StringVar(&f.End, "end", "", "End datetime (ISO 8601)")
	cmd.Flags().StringVarP(&f.Resolution, "resolution", "r", "", "Candle resolution: 1m, 5m, 15m, 1h, 4h, 1d")
	cmd.Flags().IntVarP(&f.Limit, "limit", "n", 0, "Number of records (1-1000)")
	cmd.Flags().StringVar(&f.Cursor, "cursor", "", "Pagination cursor from previous response")
	cmd.Flags().StringVar(&f.Currency, "currency", "", "Base currency filter (BTC, ETH)")
	cmd.Flags().StringVar(&f.SortDir, "sort-dir", "", "Sort direction: ASC or DESC (default DESC — newest first)")
}

// SingleInstrumentArg validates commands that take exactly one instrument and
// turns the common "exchange instrument" mistake into an actionable hint.
func SingleInstrumentArg(cmd *cobra.Command, args []string) error {
	if len(args) == 1 {
		return nil
	}
	if len(args) == 2 && looksLikeExchange(args[0]) {
		return fmt.Errorf(
			"%s accepts one instrument argument; pass exchange as a flag: %s %s --exchange %s",
			cmd.CommandPath(),
			cmd.CommandPath(),
			args[1],
			strings.ToLower(args[0]),
		)
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func looksLikeExchange(s string) bool {
	switch strings.ToLower(s) {
	case "binance", "deribit", "coinbase", "bybit", "okx", "kraken", "polymarket":
		return true
	default:
		return false
	}
}

// parsePeriod converts a shorthand like "24h", "3d", "30d" into a time.Duration.
// Supports: Nh (hours), Nd (days), Nw (weeks).
func parsePeriod(s string) (time.Duration, bool) {
	if len(s) < 2 {
		return 0, false
	}
	unit := s[len(s)-1]
	n, err := fmt.Sscanf(s[:len(s)-1], "%d", new(int))
	if err != nil || n != 1 {
		return 0, false
	}
	var val int
	fmt.Sscanf(s[:len(s)-1], "%d", &val)
	if val <= 0 {
		return 0, false
	}
	switch unit {
	case 'h':
		return time.Duration(val) * time.Hour, true
	case 'd':
		return time.Duration(val) * 24 * time.Hour, true
	case 'w':
		return time.Duration(val) * 7 * 24 * time.Hour, true
	}
	return 0, false
}

// ToParams converts common flags into API request params.
// Time range priority: --start/--end > --period > default 7d.
// Some API endpoints return 500 without a time range, so we always send one.
func (f *CommonFlags) ToParams() *api.RequestParams {
	const layout = "2006-01-02T15:04:05Z"
	const defaultWindow = 7 * 24 * time.Hour

	now := time.Now().UTC()
	start := f.Start
	end := f.End

	// --start/--end take priority if both provided
	if start == "" || end == "" {
		// Determine the window from --period or fallback to default
		window := defaultWindow
		if f.Period != "" {
			if d, ok := parsePeriod(f.Period); ok {
				window = d
			}
		}

		switch {
		case start == "" && end == "":
			end = now.Format(layout)
			start = now.Add(-window).Format(layout)
		case start != "" && end == "":
			if t, err := time.Parse(time.RFC3339, start); err == nil {
				end = t.Add(window).Format(layout)
			}
		case start == "" && end != "":
			if t, err := time.Parse(time.RFC3339, end); err == nil {
				start = t.Add(-window).Format(layout)
			}
		}
	}

	// Default sort direction: newest-first when no cursor and no explicit direction.
	// Skip when --cursor is set so a paginated scan keeps the direction it started with.
	sortDir := f.SortDir
	if sortDir == "" && f.Cursor == "" {
		sortDir = "DESC"
	}

	p := &api.RequestParams{
		Start:      start,
		End:        end,
		Resolution: f.Resolution,
		Limit:      f.Limit,
		Cursor:     f.Cursor,
		Currency:   f.Currency,
		SortDir:    sortDir,
	}
	// Only inject Exchange when the user explicitly asked for one.
	// Cross-product endpoints (catalogs, registry) call ToParams() too
	// and would otherwise inherit the config default — which silently
	// scopes a multi-venue query to a single venue. Concrete-instrument
	// commands that need an exchange set it themselves below this call.
	if ExchangeExplicit && Exchange != "" {
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

	// Apply config exchange default if --exchange flag was not provided
	if Exchange == "" {
		if cfg.Exchange != "" {
			Exchange = cfg.Exchange
		} else {
			Exchange = config.DefaultExchange
		}
	}

	// Require either an API key or a wallet key for authentication
	if cfg.APIKey == "" && cfg.WalletKey == "" {
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
	if InteractiveMode {
		SharedClient = client
	}
	return client, cfg
}

// promptOnboarding runs first-run authentication setup. Two paths:
//
//  1. API key — paste a key from app.laevitas.ch.
//  2. x402 wallet — paste an EVM private key; pays per-request in USDC on Base.
//
// Returns true if either path succeeded.
func promptOnboarding(cfg *config.Config) bool {
	bold := output.Bold
	green := output.BrandGreen
	grey := output.BrandGreyMid
	reset := output.Reset

	fmt.Println()
	fmt.Printf("  %s%s▲%s  %sWelcome to LAEVITAS CLI%s\n", bold, green, reset, bold, reset)
	fmt.Println()
	fmt.Printf("  %sChoose how to authenticate:%s\n", grey, reset)
	fmt.Println()
	fmt.Printf("    %s1.%s  %sAPI key%s     %s— get one at https://app.laevitas.ch/settings/api%s\n", bold, reset, bold, reset, grey, reset)
	fmt.Printf("    %s2.%s  %sx402 wallet%s %s— pay-per-request in USDC on Base, no signup%s\n", bold, reset, bold, reset, grey, reset)
	fmt.Printf("    %s3.%s  %sSkip%s        %s— configure later via `laevitas config init`%s\n", bold, reset, bold, reset, grey, reset)
	fmt.Println()

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("  Choose [1/2/3]: ")
	choiceRaw, err := reader.ReadString('\n')
	if err != nil {
		output.Errorf("Reading input: %s", err)
		return false
	}
	choice := strings.TrimSpace(choiceRaw)

	switch choice {
	case "1", "":
		return onboardAPIKey(cfg, reader)
	case "2":
		return onboardWallet(cfg, reader)
	case "3":
		fmt.Println()
		fmt.Printf("  %sSkipping. Run %slaevitas config init%s%s when ready.%s\n", grey, bold, reset, grey, reset)
		return false
	default:
		output.Errorf("Unrecognised choice %q. Run again to retry.", choice)
		return false
	}
}

// onboardAPIKey is the API-key branch of the first-run flow. Validates against
// the live API by hitting Health; warns on failure but keeps the key (the user
// may have a transient network issue).
func onboardAPIKey(cfg *config.Config, reader *bufio.Reader) bool {
	fmt.Println()
	fmt.Print("  Paste your API key: ")
	keyRaw, err := reader.ReadString('\n')
	if err != nil {
		output.Errorf("Reading input: %s", err)
		return false
	}
	key := strings.TrimSpace(keyRaw)
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

	fmt.Print("  Verifying API key... ")
	client := api.NewClient(cfg)
	_, verifyErr := client.Get(api.Health, nil)
	if verifyErr != nil {
		fmt.Println("✗")
		output.Warnf("API key verification failed: %v", verifyErr)
	} else {
		fmt.Println("✓")
		output.Successf("API key is valid")
	}

	fmt.Println()
	return true
}

// onboardWallet is the x402 branch. Validates the private key by deriving the
// address before saving. No network call — funds get checked at first request.
func onboardWallet(cfg *config.Config, reader *bufio.Reader) bool {
	bold := output.Bold
	grey := output.BrandGreyMid
	reset := output.Reset

	fmt.Println()
	fmt.Printf("  %sx402 wallet setup%s\n", bold, reset)
	fmt.Printf("  %sYou'll need a hex-encoded EVM private key holding USDC on Base.%s\n", grey, reset)
	fmt.Printf("  %sGenerate a fresh key with: cast wallet new   (or any EVM wallet).%s\n", grey, reset)
	fmt.Println()
	fmt.Print("  Paste private key (input is echoed): ")
	keyRaw, err := reader.ReadString('\n')
	if err != nil {
		output.Errorf("Reading input: %s", err)
		return false
	}
	key := strings.TrimSpace(keyRaw)
	if key == "" {
		output.Errorf("No key provided.")
		return false
	}

	pc, err := x402.NewPaymentClient(key)
	if err != nil {
		output.Errorf("Invalid wallet key: %v", err)
		return false
	}

	cfg.WalletKey = key
	// New wallet voids any cached credit token from a previous setup.
	config.ClearCreditToken()
	if err := config.Save(cfg); err != nil {
		output.Errorf("Saving config: %s", err)
		return false
	}

	output.Successf("Wallet saved to ~/.config/laevitas/config.json")
	fmt.Printf("  Address: %s\n", pc.Address())
	fmt.Println()
	fmt.Printf("  %sFund the address above with USDC on Base mainnet.%s\n", grey, reset)
	fmt.Printf("  %sFirst request will trigger an on-chain payment;%s\n", grey, reset)
	fmt.Printf("  %ssubsequent requests use a cached credit token.%s\n", grey, reset)
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
//
// JSON output is wrapped in a stable envelope:
//
//	{"success": true,  "data": [...], "meta": {...}}            // success
//	{"success": false, "error": {"message", "code", "status"}}  // failure
//
// Table and CSV output are NOT enveloped — they format the data array directly.
// Errors in JSON mode go to stdout (so agents can parse them); errors in table
// or csv mode go to stderr free-text. Exit code is non-zero for any error.
func RunAndPrint(client *api.Client, endpoint string, params *api.RequestParams) {
	runAndPrintWith(client, endpoint, params, nil, output.BookFilterFlags{})
}

// DefaultSnapshotLimit is the limit used by orderbook-raw / book-
// snapshot commands when the user didn't pass `-n`. A bare
// `perps orderbook-raw BTCUSDT` almost always means "give me the
// current book", not "the last 100 historical snapshots" — and
// printing 100 snapshots in table mode is unreadable. Keep this
// adjustable from one place so REST and WS surfaces stay aligned.
const DefaultSnapshotLimit = 1

// ApplySnapshotDefaults sets sensible defaults for one-shot
// snapshot commands (orderbook-raw and friends): when the caller
// didn't specify --limit and isn't paginating with --cursor, default
// to the most recent record. Mutates params in place. Used by every
// orderbook-raw RunE so the default is set in one place and stays
// consistent across product groups.
//
// We deliberately don't check Start/End: ToParams() auto-populates
// them with a default 7-day window, so they're never zero by the
// time we get here. The user signals "give me a range" by passing
// --start, --end, --period, or -n explicitly — and a non-zero
// --limit OR a non-empty --cursor are the only states where we
// should respect the historical-walk intent.
func ApplySnapshotDefaults(params *api.RequestParams) {
	if params == nil {
		return
	}
	if params.Limit == 0 && params.Cursor == "" {
		params.Limit = DefaultSnapshotLimit
	}
}

// RunAndPrintFiltered is RunAndPrint with a transform applied to
// every element of the response's `.data` array before formatting.
// Used by commands that surface the L2 book snapshot shape to apply
// `--depth` / `--compact` consistently — the same helper is called
// from the WS emit path, so REST and WS produce identically-trimmed
// payloads. See output.BookFilterFlags / output.ApplyBookFilter.
//
// `filters` is the BookFilterFlags struct registered via
// output.AddBookFilterFlags. When inactive (no flags set) the call
// is a true zero-cost passthrough — no decode/re-encode round-trip.
func RunAndPrintFiltered(client *api.Client, endpoint string, params *api.RequestParams, filters output.BookFilterFlags) {
	// Reject obviously-invalid flag combinations before any HTTP work.
	// Same validation as the WS path so agents see consistent errors
	// regardless of transport.
	if err := filters.Validate(); err != nil {
		output.Errorf("%s", err.Error())
		if !InteractiveMode {
			os.Exit(1)
		}
		return
	}
	// On the snapshot shape (orderbook-raw / ws book), --depth
	// trims asks/bids per element via ApplyBookFilter. On the
	// stats shape (orderbook), --depth picks which tier columns
	// the table surfaces — same flag, semantics adapted to the
	// shape. Both go through the same filter struct so the flag
	// registration via output.AddBookFilterFlags stays uniform.
	var transform func(json.RawMessage) json.RawMessage
	if filters.Active() {
		transform = func(elem json.RawMessage) json.RawMessage {
			return output.ApplyBookFilter(elem, filters)
		}
	}
	runAndPrintWith(client, endpoint, params, transform, filters)
}

// runAndPrintWith is the shared body for RunAndPrint and
// RunAndPrintFiltered. The optional `transform` is applied to every
// element of `.data` (when present) before the response is handed
// to the printer. nil = passthrough. `filters` propagates to the
// printer so the stats-shape table can pick its tier columns from
// --depth (snapshot shape consumes filters via the transform).
func runAndPrintWith(client *api.Client, endpoint string, params *api.RequestParams, transform func(json.RawMessage) json.RawMessage, filters output.BookFilterFlags) {
	// Warn if instrument is specified but exchange is missing
	if params != nil && params.InstrumentName != "" && params.Exchange == "" {
		output.Warnf("No --exchange specified. Add --exchange <name> (e.g. --exchange deribit, --exchange binance) for accurate results.")
	}

	p := MustPrinter()
	p.StatsTier = filters.Depth

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
		printErrorEnvelope(p, err)
		if !InteractiveMode {
			os.Exit(1)
		}
		return
	}

	// Apply per-element transform when supplied (e.g. book-filter
	// trim from RunAndPrintFiltered). Operates on the raw bytes so
	// counting / charting / printing all see the post-transform
	// payload — keeps ChartableEndpoint, table footer counts, and
	// JSON envelope wrapping consistent. Errors degrade to
	// passthrough; we never block emit on a transform glitch.
	if transform != nil {
		if filtered, ok := applyDataTransform(data, transform); ok {
			data = filtered
		}
	}

	// Extract record counts from API response metadata
	var recordCount, totalCount int
	var wrapper struct {
		Count int `json:"count"`
		Meta  *struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if json.Unmarshal(data, &wrapper) == nil {
		if wrapper.Meta != nil && wrapper.Meta.Total > 0 {
			totalCount = wrapper.Meta.Total
		}
		if wrapper.Count > 0 {
			recordCount = wrapper.Count
		}
	}

	// Set total count on printer for table footer
	if p.Format == output.FormatTable {
		if totalCount > 0 {
			p.TotalCount = totalCount
		} else if recordCount > 0 {
			p.TotalCount = recordCount
		}
	}

	// JSON output: wrap in success envelope. Table/CSV: pass through.
	printPayload := data
	if p.Format == output.FormatJSON {
		if wrapped, ok := wrapSuccessEnvelope(data, &client.LastMeta); ok {
			printPayload = wrapped
		}
	}

	if err := p.Print(printPayload); err != nil {
		output.Errorf("Formatting output: %s", err)
		if !InteractiveMode {
			os.Exit(1)
		}
		return
	}

	// Render inline chart for time-series data in table mode.
	// Charts must be drawn chronologically (oldest→newest, left→right) regardless
	// of how the response is sorted, so reverse the data when the request was DESC.
	if p.Format == output.FormatTable && !NoChart {
		if col, caption := output.ChartableEndpoint(endpoint); col != "" {
			chartData := data
			if params != nil && params.SortDir == "DESC" {
				if reversed, ok := reverseJSONArray(data); ok {
					chartData = reversed
				}
			}
			output.RenderChart(p.Writer, chartData, col, caption)
		}
	}

	// Show pagination hint for table/csv output
	if p.Format != output.FormatJSON {
		var cursorWrapper struct {
			Meta *struct {
				NextCursor string `json:"next_cursor"`
			} `json:"meta"`
			NextCursor string `json:"next_cursor"`
		}
		if json.Unmarshal(data, &cursorWrapper) == nil {
			cursor := ""
			if cursorWrapper.Meta != nil && cursorWrapper.Meta.NextCursor != "" {
				cursor = cursorWrapper.Meta.NextCursor
			} else if cursorWrapper.NextCursor != "" {
				cursor = cursorWrapper.NextCursor
			}
			if cursor != "" {
				label := "More results"
				if params != nil && params.SortDir == "DESC" {
					label = "Older results"
				}
				fmt.Fprintf(os.Stderr, "\n→ %s available. Use --cursor %q\n", label, cursor)
			}
		}
	}

	// Show request metadata footer
	printRequestMeta(client, endpoint, params, recordCount, totalCount)
}

// printRequestMeta shows a compact metadata line on stderr after each request.
//
// Format: `▲ <auth> · <latency> · <records> · <exchange> · <credits>`
//
// The leading brand-green ▲ is a visual signal that the line is request meta,
// not data. Auth method is colored by kind so x402 paths stand out from
// API-key requests at a glance. Low credit balances flip to yellow.
func printRequestMeta(client *api.Client, endpoint string, params *api.RequestParams, recordCount, totalCount int) {
	meta := client.LastMeta
	if meta.PaymentMethod == "" {
		return
	}

	// Color helpers, scoped to this function so the rest of the line stays grey.
	dim := output.BrandGreyMid
	bold := output.Bold
	reset := output.Reset
	green := output.BrandGreen
	yellow := output.Yellow

	// Auth method gets bold + colored prefix so the eye lands on it first.
	var authStr string
	switch meta.PaymentMethod {
	case api.PaymentMethodOnChain:
		authStr = fmt.Sprintf("%s%sx402 on-chain%s", bold, green, reset)
	case api.PaymentMethodCredit:
		authStr = fmt.Sprintf("%s%sx402 credit%s", bold, green, reset)
	default:
		authStr = fmt.Sprintf("%sapi-key%s", bold, reset)
	}

	// Build the rest of the segments in dim grey.
	var parts []string
	parts = append(parts, authStr)
	parts = append(parts, dim+formatDuration(meta.Duration)+reset)

	if totalCount > 0 && totalCount != recordCount && recordCount > 0 {
		parts = append(parts, fmt.Sprintf("%s%d of %d records%s", dim, recordCount, totalCount, reset))
	} else if recordCount > 0 {
		parts = append(parts, fmt.Sprintf("%s%d records%s", dim, recordCount, reset))
	}

	if params != nil && params.Exchange != "" {
		parts = append(parts, dim+params.Exchange+reset)
	}

	// Credits remaining: color yellow when low so the user notices before the
	// next on-chain payment fires. Threshold is intentionally conservative —
	// at 50, agents have a few requests of headroom before refresh.
	if meta.Credits != "" {
		creditColor := dim
		if n, err := strconv.Atoi(meta.Credits); err == nil && n < 50 {
			creditColor = yellow
		}
		parts = append(parts, fmt.Sprintf("%s%s credits%s", creditColor, meta.Credits, reset))
	}

	// Verbose adds endpoint, body size, retry count — diagnostic surface.
	if Verbose {
		parts = append(parts, dim+endpoint+reset)
		parts = append(parts, dim+formatBytes(meta.ResponseSize)+reset)
		if meta.Retries > 0 {
			parts = append(parts, fmt.Sprintf("%s%d retries%s", dim, meta.Retries, reset))
		}
	}

	separator := dim + " · " + reset
	line := strings.Join(parts, separator)
	fmt.Fprintf(os.Stderr, "%s%s▲%s  %s\n", bold, green, reset, line)
}

// formatDuration formats a duration for display (e.g. "247ms", "1.2s").
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// formatBytes formats byte count for display (e.g. "1.2 KB", "3.4 MB").
func formatBytes(b int) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// wrapSuccessEnvelope rewrites the API response body into the v0.6.0 envelope
// shape: {"success": true, "data": [...], "meta": {...}}.
//
// The upstream API returns either a bare array, or {"data": [...], "meta": {...}}
// (sometimes plus "count"). We normalise to a single shape so agents always see
// the same envelope regardless of which endpoint they hit.
//
// reqMeta carries client-side request-time facts (auth method, latency,
// remaining x402 credits) that the agent surface needs alongside whatever
// meta the API itself returned. These get merged into the envelope's meta
// block so a single .meta.* path covers both server- and client-side info.
//
// Returns (payload, true) on success. On any unmarshal failure we return
// (nil, false) and the caller falls back to printing the raw bytes — the
// envelope is best-effort, never a hard requirement.
func wrapSuccessEnvelope(data []byte, reqMeta *api.RequestMeta) ([]byte, bool) {
	out := map[string]interface{}{"success": true}

	// Try envelope-shaped first: {"data": ..., "meta": ..., "count": ...}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Meta  json.RawMessage `json:"meta,omitempty"`
		Count *int            `json:"count,omitempty"`
	}
	if err := json.Unmarshal(data, &env); err == nil && len(env.Data) > 0 {
		out["data"] = env.Data
		meta := buildMeta(env.Meta, env.Count, reqMeta)
		if meta != nil {
			out["meta"] = meta
		}
		b, err := json.Marshal(out)
		return b, err == nil
	}

	// Fall back: bare array or any other JSON value.
	var bare json.RawMessage = data
	out["data"] = bare
	if meta := buildMeta(nil, nil, reqMeta); meta != nil {
		out["meta"] = meta
	}
	b, err := json.Marshal(out)
	return b, err == nil
}

// buildMeta normalises the meta block. Merges three sources:
//   - rawMeta: the API's own meta block (next_cursor, total, count).
//   - count:   API's top-level count field, when present (older shape).
//   - reqMeta: client-side request meta (auth method, latency, x402 credits).
//
// Returns nil when there is nothing to report so callers can omit the key.
func buildMeta(rawMeta json.RawMessage, count *int, reqMeta *api.RequestMeta) map[string]interface{} {
	meta := map[string]interface{}{}
	if len(rawMeta) > 0 {
		var parsed map[string]interface{}
		if err := json.Unmarshal(rawMeta, &parsed); err == nil {
			for k, v := range parsed {
				meta[k] = v
			}
		}
	}
	if count != nil {
		meta["count"] = *count
	}
	if reqMeta != nil {
		// Auth method always reported when present so agents can confirm which
		// path served the request (api-key, x402 credit, x402 on-chain).
		if reqMeta.PaymentMethod != "" {
			meta["auth"] = reqMeta.PaymentMethod
		}
		// x402 credits-remaining is critical for budget-aware agents. Surface
		// as a string when the API sent a non-numeric value, otherwise as int.
		if reqMeta.Credits != "" {
			if n, err := strconv.Atoi(reqMeta.Credits); err == nil {
				meta["credits_remaining"] = n
			} else {
				meta["credits_remaining"] = reqMeta.Credits
			}
		}
		// Latency is useful for client-side telemetry. Always emit when known.
		if reqMeta.Duration > 0 {
			meta["latency_ms"] = reqMeta.Duration.Milliseconds()
		}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

// printErrorEnvelope emits an error in the right shape for the active output
// format. JSON mode produces {"success": false, "error": {...}} on stdout so
// agents can parse a single shape for both success and failure. Table/CSV
// modes fall back to the existing stderr free-text path.
func printErrorEnvelope(p *output.Printer, err error) {
	if p.Format != output.FormatJSON {
		output.PrintError(p.Format, err)
		return
	}

	errObj := map[string]interface{}{
		"message": err.Error(),
		"code":    api.ErrCodeUnknown,
	}

	if apiErr, ok := err.(*api.APIError); ok {
		errObj["code"] = apiErr.Code()
		errObj["status"] = apiErr.StatusCode
		// Prefer the upstream message body over our wrapped Error() string.
		errObj["message"] = apiErr.Message
		if apiErr.Endpoint != "" {
			errObj["endpoint"] = apiErr.Endpoint
		}
	} else if payErr, ok := err.(*api.PaymentError); ok {
		errObj["code"] = payErr.Code()
		errObj["status"] = payErr.StatusCode
		errObj["message"] = payErr.Message
		if payErr.Endpoint != "" {
			errObj["endpoint"] = payErr.Endpoint
		}
		if payErr.WalletAddr != "" {
			errObj["wallet_address"] = payErr.WalletAddr
		}
	} else if netErr, ok := err.(*api.NetworkError); ok {
		errObj["code"] = netErr.Code()
	}

	envelope := map[string]interface{}{
		"success": false,
		"error":   errObj,
	}
	enc := json.NewEncoder(p.Writer)
	enc.SetIndent("", "  ")
	_ = enc.Encode(envelope)
}

// reverseJSONArray returns the same JSON payload with its top-level array
// reversed. Handles both bare arrays and { "data": [...] } envelopes.
// Returns ok=false if the payload doesn't look like a record array.
func reverseJSONArray(data []byte) ([]byte, bool) {
	var bare []json.RawMessage
	if err := json.Unmarshal(data, &bare); err == nil {
		reverseRaw(bare)
		out, err := json.Marshal(bare)
		return out, err == nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
		Meta json.RawMessage `json:"meta,omitempty"`
	}
	if err := json.Unmarshal(data, &env); err != nil || len(env.Data) == 0 {
		return nil, false
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(env.Data, &rows); err != nil {
		return nil, false
	}
	reverseRaw(rows)
	newData, err := json.Marshal(rows)
	if err != nil {
		return nil, false
	}
	wrapper := map[string]json.RawMessage{"data": newData}
	if len(env.Meta) > 0 {
		wrapper["meta"] = env.Meta
	}
	out, err := json.Marshal(wrapper)
	return out, err == nil
}

func reverseRaw(s []json.RawMessage) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// applyDataTransform walks the response envelope, applies a per-
// element transform to every entry of `.data`, and re-marshals the
// result. Used to wire book-filter trimming through RunAndPrint
// without bloating its body. Two response shapes accepted:
//
//   - `{"success": true, "data": [...], "meta": {...}}` — wrapped
//     envelope (typical REST response). Transform applied to
//     each element of `data`.
//   - bare array `[...]` — some legacy endpoints / test stubs.
//     Transform applied to each element.
//
// Returns (transformed bytes, true) on success, (input, false) if
// either shape can't be decoded — caller falls back to the
// untransformed payload. We never block emit on a transform glitch
// because the worst-case fallback (full payload) is always usable.
func applyDataTransform(data []byte, transform func(json.RawMessage) json.RawMessage) ([]byte, bool) {
	// Try wrapped-envelope shape first. Decode into a map so any
	// fields we don't know about (e.g. server adds a new top-level
	// key) survive the round-trip untouched.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(data, &env); err == nil {
		raw, ok := env["data"]
		if !ok {
			return data, false
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			// data isn't an array (e.g. instruments/detail returns an
			// object). Apply transform to the object itself.
			env["data"] = transform(raw)
		} else {
			for i := range arr {
				arr[i] = transform(arr[i])
			}
			out, err := json.Marshal(arr)
			if err != nil {
				return data, false
			}
			env["data"] = out
		}
		out, err := json.Marshal(env)
		if err != nil {
			return data, false
		}
		return out, true
	}

	// Bare-array shape — rare but documented in legacy responses.
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return data, false
	}
	for i := range arr {
		arr[i] = transform(arr[i])
	}
	out, err := json.Marshal(arr)
	if err != nil {
		return data, false
	}
	return out, true
}
