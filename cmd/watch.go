package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/cmdutil"
	"github.com/laevitas/cli/internal/output"
)

// ANSI escape sequences for watch mode rendering.
const (
	wClearScreen = "\033[H\033[2J"
	wHideCursor  = "\033[?25l"
	wShowCursor  = "\033[?25h"
	wBold        = "\033[1m"
	wDim         = "\033[2m"
	wReset       = "\033[0m"
	wGreen       = "\033[32m"
	wRed         = "\033[31m"
	wCyan        = "\033[36m"
	wBgDarkGray  = "\033[48;5;236m"
	wWhite       = "\033[97m"
)

// validIntervals are the supported watch intervals.
var validIntervals = map[string]time.Duration{
	"5s":  5 * time.Second,
	"10s": 10 * time.Second,
	"30s": 30 * time.Second,
	"1m":  1 * time.Minute,
	"5m":  5 * time.Minute,
}

var watchCmd = &cobra.Command{
	Use:   "watch <interval> <command> [args...]",
	Short: "Re-run a query at a configurable interval with live-updating output",
	Long: `Watch mode re-runs any LAEVITAS command at a fixed interval and
displays live-updating output with change highlighting.

Values that increased since the last refresh are shown in green,
values that decreased are shown in red.

Supported intervals: 5s, 10s, 30s, 1m, 5m
Press 'q' or Ctrl+C to exit watch mode.`,
	Example: `  laevitas watch 10s perps funding BTC-PERPETUAL -n 1
  laevitas watch 30s futures snapshot --currency BTC
  laevitas watch 1m options snapshot --currency ETH
  laevitas watch 5s perps snapshot --currency BTC`,
	DisableFlagParsing: true,
	SilenceUsage:       true,
	SilenceErrors:      true,
	Run: func(cmd *cobra.Command, args []string) {
		// Handle --help / -h manually since DisableFlagParsing is true
		for _, a := range args {
			if a == "--help" || a == "-h" {
				cmd.Help()
				return
			}
		}
		if len(args) < 2 {
			cmd.Help()
			return
		}
		if err := runWatch(args); err != nil {
			output.Errorf("%s", err)
			if !cmdutil.InteractiveMode {
				os.Exit(1)
			}
		}
	},
}

// runWatch is the main watch loop.
func runWatch(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: watch <interval> <command> [args...]")
	}

	intervalStr := args[0]
	interval, ok := validIntervals[intervalStr]
	if !ok {
		return fmt.Errorf("invalid interval %q — supported: 5s, 10s, 30s, 1m, 5m", intervalStr)
	}

	innerArgs := args[1:]
	cmdLabel := strings.Join(innerArgs, " ")

	// Resolve the inner command to find the endpoint and params.
	endpoint, params, err := resolveWatchCommand(innerArgs)
	if err != nil {
		return fmt.Errorf("cannot watch: %s", err)
	}

	client, _ := cmdutil.MustClient()
	if client == nil {
		return fmt.Errorf("no API client available")
	}

	// Put terminal in raw mode so we can read 'q' without blocking
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to set raw terminal mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	// Hide cursor
	fmt.Print(wHideCursor)
	defer fmt.Print(wShowCursor)

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Channel for keypress events
	keyCh := make(chan byte, 1)
	go watchReadKeys(keyCh)

	var prevData []byte
	lastRefresh := time.Time{}
	tick := time.NewTicker(100 * time.Millisecond) // tick for status bar updates
	defer tick.Stop()

	refreshNow := true // fetch immediately on start

	for {
		select {
		case <-sigCh:
			watchExit()
			return nil

		case key := <-keyCh:
			if key == 'q' || key == 'Q' || key == 3 { // 3 = Ctrl+C
				watchExit()
				return nil
			}

		case <-tick.C:
			now := time.Now()

			// Time for a new refresh?
			if refreshNow || (!lastRefresh.IsZero() && now.Sub(lastRefresh) >= interval) {
				refreshNow = false

				data, fetchErr := client.Get(endpoint, params)

				// Render the screen
				fmt.Print(wClearScreen)
				watchPrintHeader(cmdLabel, intervalStr)

				if fetchErr != nil {
					output.Errorf("Fetch failed: %s", fetchErr)
				} else {
					watchRenderTable(data, prevData)
					prevData = data
				}

				lastRefresh = time.Now()
			}

			// Always update the status bar
			if !lastRefresh.IsZero() {
				elapsed := time.Since(lastRefresh)
				remaining := interval - elapsed
				if remaining < 0 {
					remaining = 0
				}
				watchPrintStatusBar(elapsed, remaining)
			}
		}
	}
}

// resolveWatchCommand walks the cobra command tree to find the API endpoint
// and request params for the given args (e.g. ["perps", "funding", "BTC-PERPETUAL", "-n", "1"]).
func resolveWatchCommand(args []string) (string, *api.RequestParams, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("no command specified")
	}

	// Walk the command tree to find the leaf command
	cmd, remainingArgs, err := rootCmd.Find(args)
	if err != nil {
		return "", nil, fmt.Errorf("unknown command: %s", strings.Join(args, " "))
	}

	if cmd == rootCmd {
		return "", nil, fmt.Errorf("cannot watch the root command — specify a subcommand like 'perps funding BTC-PERPETUAL'")
	}

	// Parse flags on the resolved command
	if err := cmd.ParseFlags(remainingArgs); err != nil {
		return "", nil, fmt.Errorf("parsing flags: %w", err)
	}

	// Get non-flag args
	nonFlagArgs := cmd.Flags().Args()

	// Resolve the API endpoint from the command path
	endpoint, err := watchEndpointForCommand(cmd)
	if err != nil {
		return "", nil, err
	}

	// Build params from the command's flags
	params := &api.RequestParams{
		Exchange: cmdutil.Exchange,
	}

	// Extract common flags if they exist on this command
	if f := cmd.Flags().Lookup("start"); f != nil && f.Value.String() != "" {
		params.Start = f.Value.String()
	}
	if f := cmd.Flags().Lookup("end"); f != nil && f.Value.String() != "" {
		params.End = f.Value.String()
	}
	if f := cmd.Flags().Lookup("resolution"); f != nil && f.Value.String() != "" {
		params.Resolution = f.Value.String()
	}
	if f := cmd.Flags().Lookup("limit"); f != nil && f.Value.String() != "" && f.Value.String() != "0" {
		if n, parseErr := strconv.Atoi(f.Value.String()); parseErr == nil {
			params.Limit = n
		}
	}
	if f := cmd.Flags().Lookup("cursor"); f != nil && f.Value.String() != "" {
		params.Cursor = f.Value.String()
	}
	if f := cmd.Flags().Lookup("currency"); f != nil && f.Value.String() != "" {
		params.Currency = f.Value.String()
	}
	if f := cmd.Flags().Lookup("date"); f != nil && f.Value.String() != "" {
		params.Date = f.Value.String()
	}
	if f := cmd.Flags().Lookup("maturity"); f != nil && f.Value.String() != "" {
		params.Maturity = f.Value.String()
	}
	if f := cmd.Flags().Lookup("category"); f != nil && f.Value.String() != "" {
		params.Category = f.Value.String()
	}
	if f := cmd.Flags().Lookup("event"); f != nil && f.Value.String() != "" {
		params.EventSlug = f.Value.String()
	}
	if f := cmd.Flags().Lookup("keyword"); f != nil && f.Value.String() != "" {
		params.Keyword = f.Value.String()
	}
	if f := cmd.Flags().Lookup("direction"); f != nil && f.Value.String() != "" {
		params.Direction = f.Value.String()
	}
	if f := cmd.Flags().Lookup("type"); f != nil && f.Value.String() != "" {
		params.OptionType = f.Value.String()
	}
	if f := cmd.Flags().Lookup("min-premium"); f != nil && f.Value.String() != "" && f.Value.String() != "0" {
		if v, parseErr := strconv.ParseFloat(f.Value.String(), 64); parseErr == nil {
			params.MinPremium = v
		}
	}
	if f := cmd.Flags().Lookup("sort"); f != nil && f.Value.String() != "" {
		params.Sort = f.Value.String()
	}
	if f := cmd.Flags().Lookup("sort-dir"); f != nil && f.Value.String() != "" {
		params.SortDir = f.Value.String()
	}
	if f := cmd.Flags().Lookup("block-only"); f != nil && f.Value.String() == "true" {
		params.BlockOnly = true
	}
	if f := cmd.Flags().Lookup("opening-only"); f != nil && f.Value.String() == "true" {
		params.OpeningOnly = true
	}

	// If the command expects a positional instrument argument
	if len(nonFlagArgs) > 0 {
		params.InstrumentName = nonFlagArgs[0]
	}

	return endpoint, params, nil
}

// watchEndpointForCommand maps a resolved cobra command to its API endpoint path.
func watchEndpointForCommand(cmd *cobra.Command) (string, error) {
	path := cmd.CommandPath() // e.g. "laevitas perps funding"

	// Map of "parent child" → endpoint
	endpointMap := map[string]string{
		// Futures
		"futures catalog":   api.FuturesCatalog,
		"futures snapshot":  api.FuturesSnapshot,
		"futures ohlcvt":    api.FuturesOHLCVT,
		"futures oi":        api.FuturesOpenInterest,
		"futures carry":     api.FuturesCarry,
		"futures trades":    api.FuturesTrades,
		"futures volume":    api.FuturesVolume,
		"futures level1":    api.FuturesLevel1,
		"futures orderbook": api.FuturesOrderbook,
		"futures ticker":    api.FuturesTickerHistory,
		"futures ref-price": api.FuturesReferencePrice,
		"futures metadata":  api.FuturesMetadata,
		// Perps
		"perps catalog":   api.PerpsCatalog,
		"perps snapshot":  api.PerpsSnapshot,
		"perps carry":     api.PerpsCarry,
		"perps ohlcvt":    api.PerpsOHLCVT,
		"perps oi":        api.PerpsOpenInterest,
		"perps trades":    api.PerpsTrades,
		"perps volume":    api.PerpsVolume,
		"perps level1":    api.PerpsLevel1,
		"perps orderbook": api.PerpsOrderbook,
		"perps ticker":    api.PerpsTickerHistory,
		"perps ref-price": api.PerpsReferencePrice,
		"perps metadata":  api.PerpsMetadata,
		// Options
		"options catalog":    api.OptionsCatalog,
		"options snapshot":   api.OptionsSnapshot,
		"options ohlcvt":     api.OptionsOHLCVT,
		"options trades":     api.OptionsTrades,
		"options oi":         api.OptionsOpenInterest,
		"options volume":     api.OptionsVolume,
		"options level1":     api.OptionsLevel1,
		"options ref-price":  api.OptionsReferencePrice,
		"options flow":       api.OptionsFlow,
		"options ticker":     api.OptionsTickerHistory,
		"options volatility": api.OptionsVolatility,
		"options metadata":   api.OptionsMetadata,
		// Vol surface (under options)
		"options vol-surface by-expiry": api.VolSurfaceByExpiry,
		"options vol-surface by-tenor":  api.VolSurfaceByTenor,
		"options vol-surface by-time":   api.VolSurfaceByTime,
		// Predictions
		"predictions catalog":    api.PredictionsCatalog,
		"predictions categories": api.PredictionsCategories,
		"predictions snapshot":   api.PredictionsSnapshot,
		"predictions ohlcvt":     api.PredictionsOHLCVT,
		"predictions trades":     api.PredictionsTrades,
		"predictions orderbook":  api.PredictionsOrderbookRaw,
		"predictions ticker":     api.PredictionsTickerHistory,
		"predictions metadata":   api.PredictionsMetadata,
	}

	// Extract command key from full path "laevitas parent child [subchild]"
	parts := strings.Fields(path)
	if len(parts) >= 4 {
		// Try 3-level key first (e.g. "options vol-surface snapshot")
		key := parts[1] + " " + parts[2] + " " + parts[3]
		if ep, found := endpointMap[key]; found {
			return ep, nil
		}
	}
	if len(parts) >= 3 {
		key := parts[1] + " " + parts[2]
		if ep, found := endpointMap[key]; found {
			return ep, nil
		}
	}

	return "", fmt.Errorf("unsupported command for watch: %s", path)
}

// watchPrintHeader renders the header line at the top of the screen.
func watchPrintHeader(cmdLabel, interval string) {
	now := time.Now().Format("15:04:05")
	header := fmt.Sprintf(" %s%sLAEVITAS WATCH%s  %s%s%s  every %s  %s",
		wBold, wCyan, wReset,
		wDim, cmdLabel, wReset,
		interval,
		wDim+now+wReset,
	)
	fmt.Println(header)
	fmt.Println()
}

// watchRenderTable renders the API data as a table with change highlighting.
func watchRenderTable(data, prevData []byte) {
	currRows := watchParseJSON(data)
	prevRows := watchParseJSON(prevData)

	if len(currRows) == 0 {
		fmt.Println("  No data.")
		return
	}

	headers := currRows[0]
	dataRows := currRows[1:]

	prevValues := watchBuildValueMap(prevRows)

	numCols := len(headers)
	isNumeric := watchDetectNumeric(headers, dataRows)

	// Format data cells for display
	displayRows := make([][]string, len(dataRows))
	for r, row := range dataRows {
		displayRows[r] = make([]string, numCols)
		for c := 0; c < numCols; c++ {
			if c < len(row) {
				if isNumeric[c] && row[c] != "" {
					displayRows[r][c] = output.FormatNumber(row[c])
				} else {
					displayRows[r][c] = row[c]
				}
			}
		}
	}

	// Display headers
	displayHeaders := make([]string, numCols)
	for i, h := range headers {
		displayHeaders[i] = strings.ToUpper(h)
	}

	// Calculate column widths
	widths := make([]int, numCols)
	for i, h := range displayHeaders {
		widths[i] = len(h)
	}
	for _, row := range displayRows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Terminal width truncation
	termWidth := watchTermWidth()
	totalWidth := 0
	for i, w := range widths {
		if i > 0 {
			totalWidth += 2
		}
		totalWidth += w
	}
	if termWidth > 0 && totalWidth > termWidth {
		watchTruncCols(widths, termWidth)
	}

	// Print header row
	var hdr strings.Builder
	for i, h := range displayHeaders {
		if i > 0 {
			hdr.WriteString("  ")
		}
		hdr.WriteString(watchPadRight(h, widths[i]))
	}
	fmt.Printf("%s%s%s%s%s\n", wBold, wWhite, wBgDarkGray, hdr.String(), wReset)

	// Print separator
	var sep strings.Builder
	for i, w := range widths {
		if i > 0 {
			sep.WriteString("  ")
		}
		sep.WriteString(strings.Repeat("─", w))
	}
	fmt.Printf("%s%s%s\n", wDim, sep.String(), wReset)

	// Print data rows with change highlighting
	for r, row := range displayRows {
		var line strings.Builder
		for c, cell := range row {
			if c > 0 {
				line.WriteString("  ")
			}

			padded := watchPadCell(cell, widths[c], isNumeric[c])

			// Check for change from previous data
			if isNumeric[c] && prevData != nil && c < len(headers) && r < len(dataRows) && c < len(dataRows[r]) {
				prevVal, hasPrev := prevValues[watchKey(r, headers[c])]
				if hasPrev {
					currRaw := dataRows[r][c]
					change := watchCompare(currRaw, prevVal)
					if change > 0 {
						padded = wGreen + wBold + padded + wReset
					} else if change < 0 {
						padded = wRed + wBold + padded + wReset
					}
				}
			}

			line.WriteString(padded)
		}
		fmt.Println(line.String())
	}

	// Record count
	if len(dataRows) > 0 {
		fmt.Printf("\n%s%d records%s\n", wDim, len(dataRows), wReset)
	}
}

// watchPrintStatusBar renders the live-updating status bar at the bottom.
func watchPrintStatusBar(elapsed, remaining time.Duration) {
	tw := watchTermWidth()
	if tw <= 0 {
		tw = 80
	}

	elapsedStr := watchFmtDuration(elapsed)
	remainingStr := watchFmtDuration(remaining)

	bar := fmt.Sprintf(" Last updated: %s ago | Next refresh in %s | Press q to stop",
		elapsedStr, remainingStr)

	// Pad to terminal width for full-width background
	if len(bar) < tw {
		bar += strings.Repeat(" ", tw-len(bar))
	}

	// Move cursor to the bottom of the terminal
	rows := watchTermHeight()
	if rows <= 0 {
		rows = 24
	}
	fmt.Printf("\033[%d;1H", rows)
	fmt.Printf("%s%s%s%s", wBgDarkGray, wWhite, bar, wReset)
}

// ─── Watch helper functions ──────────────────────────────────────────────────

// watchParseJSON parses raw API JSON into a 2D string grid (header + data).
func watchParseJSON(data []byte) [][]string {
	if data == nil {
		return nil
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err != nil {
		// Try unwrapping { "data": [...] }
		var wrapper struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(data, &wrapper) != nil || wrapper.Data == nil {
			return nil
		}
		if json.Unmarshal(wrapper.Data, &records) != nil {
			return nil
		}
	}

	if len(records) == 0 {
		return nil
	}

	// Collect headers in order of first appearance
	var headers []string
	headerSet := map[string]bool{}
	for _, rec := range records {
		for k := range rec {
			if !headerSet[k] {
				headerSet[k] = true
				headers = append(headers, k)
			}
		}
	}

	rows := [][]string{headers}
	for _, rec := range records {
		row := make([]string, len(headers))
		for i, h := range headers {
			if v, ok := rec[h]; ok {
				row[i] = watchFmtValue(v)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// watchBuildValueMap creates a lookup from "rowIdx:colName" to raw string value.
func watchBuildValueMap(rows [][]string) map[string]string {
	m := make(map[string]string)
	if len(rows) < 2 {
		return m
	}
	headers := rows[0]
	for r, row := range rows[1:] {
		for c, cell := range row {
			if c < len(headers) {
				m[watchKey(r, headers[c])] = cell
			}
		}
	}
	return m
}

func watchKey(row int, col string) string {
	return fmt.Sprintf("%d:%s", row, col)
}

// watchCompare returns +1 if curr > prev, -1 if curr < prev, 0 if equal.
func watchCompare(curr, prev string) int {
	c, errC := strconv.ParseFloat(curr, 64)
	p, errP := strconv.ParseFloat(prev, 64)
	if errC != nil || errP != nil {
		return 0
	}
	if c > p {
		return 1
	}
	if c < p {
		return -1
	}
	return 0
}

// watchDetectNumeric checks each column to see if all values are numeric.
func watchDetectNumeric(headers []string, dataRows [][]string) []bool {
	numCols := len(headers)
	isNumeric := make([]bool, numCols)
	for c := 0; c < numCols; c++ {
		numCount := 0
		total := 0
		for _, row := range dataRows {
			if c >= len(row) || row[c] == "" {
				continue
			}
			total++
			if _, err := strconv.ParseFloat(row[c], 64); err == nil {
				numCount++
			}
		}
		isNumeric[c] = total > 0 && numCount == total
	}
	return isNumeric
}

func watchFmtValue(v interface{}) string {
	switch val := v.(type) {
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%.0f", val)
		}
		return fmt.Sprintf("%g", val)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

func watchFmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

func watchPadRight(s string, width int) string {
	if len(s) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(s))
}

func watchPadCell(s string, width int, rightAlign bool) string {
	if len(s) > width {
		if width <= 3 {
			return s[:width]
		}
		return s[:width-1] + "…"
	}
	if rightAlign {
		return fmt.Sprintf("%*s", width, s)
	}
	return fmt.Sprintf("%-*s", width, s)
}

func watchTermWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

func watchTermHeight() int {
	_, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || h <= 0 {
		return 0
	}
	return h
}

func watchTruncCols(widths []int, termWidth int) {
	const minWidth = 6
	for {
		total := 0
		for i, w := range widths {
			if i > 0 {
				total += 2
			}
			total += w
		}
		if total <= termWidth {
			break
		}
		maxIdx := 0
		maxW := 0
		for i, w := range widths {
			if w > maxW {
				maxW = w
				maxIdx = i
			}
		}
		if maxW <= minWidth {
			break
		}
		widths[maxIdx]--
	}
}

// watchReadKeys reads single bytes from stdin in a goroutine.
func watchReadKeys(ch chan<- byte) {
	buf := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return
		}
		ch <- buf[0]
	}
}

// watchExit restores the screen before exiting watch mode.
func watchExit() {
	fmt.Print(wShowCursor)
	fmt.Print(wClearScreen)
	fmt.Println("Watch mode stopped.")
}
