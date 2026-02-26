package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// Format determines the output format.
type Format string

const (
	FormatAuto  Format = "auto"
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatCSV   Format = "csv"
)

// Resolve determines the effective format, using TTY detection for "auto".
func Resolve(f string) Format {
	switch strings.ToLower(f) {
	case "json":
		return FormatJSON
	case "csv":
		return FormatCSV
	case "table":
		return FormatTable
	default:
		// auto: table if interactive terminal, json if piped
		if term.IsTerminal(int(os.Stdout.Fd())) {
			return FormatTable
		}
		return FormatJSON
	}
}

// Printer handles rendering data in the chosen format.
type Printer struct {
	Format Format
	Writer io.Writer

	// TotalCount is the total number of records available (from API metadata).
	// Set by the caller before Print() to enable the footer.
	TotalCount int
}

// NewPrinter creates a printer for the given format string.
func NewPrinter(format string) *Printer {
	return &Printer{
		Format: Resolve(format),
		Writer: os.Stdout,
	}
}

// Print renders data according to the configured format.
// data can be:
//   - []byte (raw JSON from API — printed directly for JSON format)
//   - any struct or slice (marshaled to JSON, or rendered as table/csv)
func (p *Printer) Print(data interface{}) error {
	switch p.Format {
	case FormatJSON:
		return p.printJSON(data)
	case FormatCSV:
		return p.printCSV(data)
	default:
		return p.printTable(data)
	}
}

func (p *Printer) printJSON(data interface{}) error {
	// If it's already raw bytes, try to pretty-print
	if raw, ok := data.([]byte); ok {
		var buf interface{}
		if err := json.Unmarshal(raw, &buf); err == nil {
			enc := json.NewEncoder(p.Writer)
			enc.SetIndent("", "  ")
			return enc.Encode(buf)
		}
		_, err := p.Writer.Write(raw)
		return err
	}

	enc := json.NewEncoder(p.Writer)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}

func (p *Printer) printCSV(data interface{}) error {
	rows := toRows(data)
	if len(rows) == 0 {
		return nil
	}

	w := csv.NewWriter(p.Writer)
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// ─── Table styles (lipgloss) ────────────────────────────────────────────────

var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")). // bright white
			Background(lipgloss.Color("236")) // dark gray

	separatorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("238")) // subtle dark gray

	footerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")). // medium gray
			Italic(true)

	positiveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	negativeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))  // red
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("245")) // dim gray for timestamps
)

func (p *Printer) printTable(data interface{}) error {
	rows := toRows(data)
	if len(rows) == 0 {
		fmt.Fprintln(p.Writer, "No data.")
		return nil
	}

	headers := rows[0]
	dataRows := rows[1:]

	// Detect column types from headers and data
	numCols := len(headers)
	isNumeric := make([]bool, numCols)
	isTimestamp := make([]bool, numCols)
	isSignedValue := make([]bool, numCols) // columns where sign matters (funding, basis, carry)

	for i, h := range headers {
		hl := strings.ToLower(h)
		isTimestamp[i] = isTimestampHeader(hl) && !isDaysColumn(hl)
		isSignedValue[i] = isSignedHeader(hl)
	}

	// Format data cells for table display and detect numeric columns
	formatted := make([][]string, len(dataRows))
	for r, row := range dataRows {
		formatted[r] = make([]string, numCols)
		for c := 0; c < numCols; c++ {
			if c < len(row) {
				formatted[r][c] = row[c]
			}
		}
	}

	// Detect numeric columns by sampling data
	for c := 0; c < numCols; c++ {
		if isTimestamp[c] {
			continue
		}
		numericCount := 0
		total := 0
		for _, row := range formatted {
			if row[c] == "" {
				continue
			}
			total++
			if _, err := strconv.ParseFloat(row[c], 64); err == nil {
				numericCount++
			}
		}
		if total > 0 && numericCount == total {
			isNumeric[c] = true
		}
	}

	// Apply formatting to cells
	displayRows := make([][]string, len(formatted))
	for r, row := range formatted {
		displayRows[r] = make([]string, numCols)
		for c, cell := range row {
			hl := strings.ToLower(headers[c])
			if isTimestamp[c] && cell != "" {
				displayRows[r][c] = formatRelativeTime(cell)
			} else if hl == "days_to_expiry" && cell != "" {
				// Round noisy float to integer (e.g. 30.649... → 31)
				if f, err := strconv.ParseFloat(cell, 64); err == nil {
					displayRows[r][c] = fmt.Sprintf("%d", int(math.Round(f)))
				} else {
					displayRows[r][c] = cell
				}
			} else if isNumeric[c] && cell != "" {
				displayRows[r][c] = formatNumber(cell)
			} else {
				displayRows[r][c] = cell
			}
		}
	}

	// Format headers for display
	displayHeaders := make([]string, numCols)
	for i, h := range headers {
		displayHeaders[i] = strings.ToUpper(h)
	}

	// Calculate column widths from formatted data
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

	// Detect terminal width and truncate if needed
	termWidth := getTerminalWidth()
	totalWidth := calcTotalWidth(widths)
	if termWidth > 0 && totalWidth > termWidth {
		truncateColumns(widths, termWidth)
	}

	// Print header row
	var hdr strings.Builder
	for i, h := range displayHeaders {
		if i > 0 {
			hdr.WriteString("  ")
		}
		cell := padOrTruncate(h, widths[i], false) // headers left-aligned
		hdr.WriteString(cell)
	}
	fmt.Fprintln(p.Writer, headerStyle.Render(hdr.String()))

	// Print separator
	var sep strings.Builder
	for i, w := range widths {
		if i > 0 {
			sep.WriteString("  ")
		}
		sep.WriteString(strings.Repeat("─", w))
	}
	fmt.Fprintln(p.Writer, separatorStyle.Render(sep.String()))

	// Print data rows
	for r, row := range displayRows {
		var line strings.Builder
		for c, cell := range row {
			if c > 0 {
				line.WriteString("  ")
			}
			truncated := padOrTruncate(cell, widths[c], isNumeric[c])

			// Apply color for signed numeric values
			if isSignedValue[c] && isNumeric[c] && cell != "" {
				val, err := strconv.ParseFloat(formatted[r][c], 64)
				if err == nil {
					if val > 0 {
						truncated = positiveStyle.Render(truncated)
					} else if val < 0 {
						truncated = negativeStyle.Render(truncated)
					}
				}
			} else if isTimestamp[c] && cell != "" {
				truncated = dimStyle.Render(truncated)
			}

			line.WriteString(truncated)
		}
		fmt.Fprintln(p.Writer, line.String())

		// Subtle separator every 5 rows
		if (r+1)%5 == 0 && r < len(displayRows)-1 {
			var subtle strings.Builder
			for i, w := range widths {
				if i > 0 {
					subtle.WriteString("  ")
				}
				subtle.WriteString(strings.Repeat("·", w))
			}
			fmt.Fprintln(p.Writer, separatorStyle.Render(subtle.String()))
		}
	}

	// Footer
	shown := len(displayRows)
	if p.TotalCount > 0 && p.TotalCount != shown {
		fmtr := message.NewPrinter(language.English)
		footer := fmtr.Sprintf("Showing %d of %d records", shown, p.TotalCount)
		fmt.Fprintln(p.Writer, footerStyle.Render(footer))
	} else if shown > 0 {
		fmtr := message.NewPrinter(language.English)
		footer := fmtr.Sprintf("%d records", shown)
		fmt.Fprintln(p.Writer, footerStyle.Render(footer))
	}

	return nil
}

// ─── Column type detection ──────────────────────────────────────────────────

func isDaysColumn(h string) bool {
	return strings.HasPrefix(h, "days_")
}

func isTimestampHeader(h string) bool {
	tsKeywords := []string{
		"timestamp", "time", "datetime", "date", "minute",
		"created_at", "updated_at",
		"expiration", "expiry", "start_time", "end_time", "trade_time",
		"open_time", "close_time",
	}
	for _, kw := range tsKeywords {
		if h == kw || strings.HasSuffix(h, "_"+kw) || strings.HasSuffix(h, "_at") {
			return true
		}
	}
	return false
}

func isSignedHeader(h string) bool {
	signedKeywords := []string{
		"funding", "basis", "carry", "pnl", "profit", "loss", "change",
		"return", "delta", "diff", "spread", "premium", "discount",
		"funding_rate", "annualized_basis", "annualized_carry",
		"funding_rate_close", "funding_rate_open",
	}
	for _, kw := range signedKeywords {
		if h == kw || strings.Contains(h, kw) {
			return true
		}
	}
	return false
}

// ─── Number formatting ──────────────────────────────────────────────────────

var numberPrinter = message.NewPrinter(language.English)

// FormatNumber formats a numeric string with thousand separators and
// appropriate decimal precision. Exported for use by the watch command.
func FormatNumber(s string) string {
	return formatNumber(s)
}

func formatNumber(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}

	// Integers: thousand separators, no decimals
	if f == math.Trunc(f) && math.Abs(f) >= 1 {
		return numberPrinter.Sprintf("%d", int64(f))
	}

	// Small decimals (rates, percentages): keep precision
	abs := math.Abs(f)
	if abs < 0.01 {
		return fmt.Sprintf("%.6f", f)
	}
	if abs < 1 {
		return fmt.Sprintf("%.4f", f)
	}

	// Large decimals: 2 decimal places with thousand separators
	return numberPrinter.Sprintf("%.2f", f)
}

// ─── Relative time formatting ───────────────────────────────────────────────

// isoTimestampRe matches common ISO 8601 formats.
var isoTimestampRe = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`,
)

func formatRelativeTime(s string) string {
	if !isoTimestampRe.MatchString(s) {
		return s
	}

	// Try parsing common timestamp formats
	var t time.Time
	var err error

	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05.000Z",
	}
	for _, layout := range formats {
		t, err = time.Parse(layout, s)
		if err == nil {
			break
		}
	}
	if err != nil {
		return s
	}

	// Use compact absolute timestamps to preserve precision in time-series data.
	// Only fall back to relative ("2d ago") for data older than 7 days.
	now := time.Now().UTC()
	diff := now.Sub(t)
	if diff < 0 {
		diff = -diff
	}

	switch {
	case diff < 24*time.Hour:
		return t.Format("15:04") // e.g. "16:09"
	case diff < 7*24*time.Hour:
		return t.Format("Mon 15:04") // e.g. "Mon 16:09"
	case diff < 365*24*time.Hour:
		return t.Format("Jan 02 15:04") // e.g. "Feb 24 16:09"
	default:
		return t.Format("2006-01-02") // e.g. "2025-02-24"
	}
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h"
		}
		return fmt.Sprintf("%dh", h)
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// ─── Terminal width and truncation ──────────────────────────────────────────

func getTerminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 0 // unknown — don't truncate
	}
	return w
}

func calcTotalWidth(widths []int) int {
	total := 0
	for i, w := range widths {
		if i > 0 {
			total += 2 // column spacing
		}
		total += w
	}
	return total
}

// truncateColumns reduces column widths to fit terminal width.
// It shrinks the widest columns first, down to a minimum of 6 chars.
func truncateColumns(widths []int, termWidth int) {
	const minWidth = 6
	for calcTotalWidth(widths) > termWidth {
		// Find widest column
		maxIdx := 0
		maxW := 0
		for i, w := range widths {
			if w > maxW {
				maxW = w
				maxIdx = i
			}
		}
		if maxW <= minWidth {
			break // can't shrink further
		}
		widths[maxIdx]--
	}
}

// padOrTruncate pads a cell to the given width, or truncates with ellipsis.
// If rightAlign is true, the value is right-aligned (for numbers).
func padOrTruncate(s string, width int, rightAlign bool) string {
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

// ─── Data extraction helpers ────────────────────────────────────────────────

// toRows converts structured data into a 2D string grid (header + data rows).
// It handles slices of structs/maps or single structs/maps.
func toRows(data interface{}) [][]string {
	// Handle raw JSON bytes by unmarshaling first
	if raw, ok := data.([]byte); ok {
		var parsed interface{}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return nil
		}
		return toRows(parsed)
	}

	v := reflect.ValueOf(data)

	// Dereference pointers
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	switch v.Kind() {
	case reflect.Slice:
		if v.Len() == 0 {
			return nil
		}
		return sliceToRows(v)
	case reflect.Map:
		// Unwrap API envelope: if the map has a "data" key with a slice value,
		// render the inner array as a table instead of a raw key-value dump.
		dataVal := v.MapIndex(reflect.ValueOf("data"))
		if dataVal.IsValid() {
			inner := dataVal
			for inner.Kind() == reflect.Interface {
				inner = inner.Elem()
			}
			if inner.Kind() == reflect.Slice {
				if inner.Len() == 0 {
					return nil // empty data → "No data."
				}
				return sliceToRows(inner)
			}
		}
		return mapToRows(v)
	default:
		return nil
	}
}

func sliceToRows(v reflect.Value) [][]string {
	first := v.Index(0)
	for first.Kind() == reflect.Interface {
		first = first.Elem()
	}

	if first.Kind() == reflect.Map {
		return sliceOfMapsToRows(v)
	}

	return nil
}

// columnPriority returns a sort weight for a column name.
// Lower = shown first. Columns not listed get a default weight of 500.
var columnPriorities = map[string]int{
	// ── Identity / time — always first ──────────────────────────────────
	"date": 1, "minute": 1, "timestamp": 2,
	"exchange": 5, "currency": 6,
	"instrument_name": 10, "instrument_type": 11,
	"maturity": 12, "tenor": 13,
	"days_to_expiry": 14,

	// ── OHLCV core ──────────────────────────────────────────────────────
	"open": 20, "high": 21, "low": 22, "close": 23,
	"volume": 24, "vwap": 25,
	"mark_price": 26, "index_price": 27,
	"underlying_price": 28, "price": 29,

	// ── OI ───────────────────────────────────────────────────────────────
	"oi": 30, "oi_open": 30, "oi_high": 31, "oi_low": 32, "oi_close": 33,
	"open_interest": 34, "oi_change": 35,

	// ── Carry / funding ─────────────────────────────────────────────────
	"annualized_carry": 40, "funding_rate": 41,
	"funding_rate_close": 42, "funding_8h_close": 43, "basis_close": 44,
	"funding_rate_open": 45, "funding_8h_open": 46,
	"basis_open": 47, "basis_high": 48, "basis_low": 49,
	"funding_rate_high": 50, "funding_rate_low": 51,
	"funding_8h_high": 52, "funding_8h_low": 53,
	"basis": 54, "funding": 55,
	"next_funding_time": 56,

	// ── Level1 / bid-ask ────────────────────────────────────────────────
	"bid_price": 60, "ask_price": 61, "bid_size": 62, "ask_size": 63,
	"bid_ask_spread": 64,
	"bid_price_close": 65, "ask_price_close": 66,
	"bid_size_close": 67, "ask_size_close": 68,
	"bid_ask_spread_close": 69,
	"mark_price_close": 70, "index_price_close": 71,
	"bid_price_open": 72, "ask_price_open": 73,
	"bid_size_open": 74, "ask_size_open": 75,
	"bid_ask_spread_open": 76,
	"bid_price_high": 77, "ask_price_high": 78,
	"bid_size_high": 79, "ask_size_high": 80,
	"bid_ask_spread_high": 81,
	"bid_price_low": 82, "ask_price_low": 83,
	"bid_size_low": 84, "ask_size_low": 85,
	"bid_ask_spread_low": 86,
	"total_liquidity_close": 87, "total_liquidity_open": 88,
	"total_liquidity_high": 89, "total_liquidity_low": 90,
	"total_liquidity_avg": 91,
	"mark_price_open": 92, "mark_price_high": 93, "mark_price_low": 94,
	"index_price_open": 95, "index_price_high": 96, "index_price_low": 97,

	// ── Volume / trades ─────────────────────────────────────────────────
	"trades_count": 100, "buy_trades_count": 101, "sell_trades_count": 102,
	"buy_volume": 103, "sell_volume": 104,
	"volume_usd_24h": 105, "volume_24h": 106,

	// ── Options: greeks / pricing ───────────────────────────────────────
	"strike": 120, "option_type": 121, "direction": 122,
	"premium_usd": 123, "premium": 124, "notional": 125, "amount": 126,
	"iv": 130, "delta": 131, "gamma": 132, "theta": 133, "vega": 134, "rho": 135,
	"bid_iv": 136, "ask_iv": 137, "mark_iv": 138, "iv_spread": 139,
	"bid_iv_close": 140, "ask_iv_close": 141, "mark_iv_close": 142, "iv_spread_close": 143,
	"bid_iv_open": 144, "ask_iv_open": 145, "mark_iv_open": 146, "iv_spread_open": 147,
	"bid_iv_high": 148, "ask_iv_high": 149, "mark_iv_high": 150, "iv_spread_high": 151,
	"bid_iv_low": 152, "ask_iv_low": 153, "mark_iv_low": 154, "iv_spread_low": 155,
	"iv_spread_avg": 156,

	// ── Vol surface ─────────────────────────────────────────────────────
	"atm_iv": 160, "skew_25d": 161, "butterfly_25d": 162,
	"call_25d_iv": 163, "put_25d_iv": 164,

	// ── Options trade changes (secondary) ───────────────────────────────
	"bid_price_change": 200, "ask_price_change": 201,
	"bid_size_change": 202, "ask_size_change": 203,
	"bid_iv_change": 204, "ask_iv_change": 205,

	// ── Predictions ─────────────────────────────────────────────────────
	"category": 250, "event_slug": 251,

	// ── Orderbook depth: microprice ─────────────────────────────────────
	"microprice_close": 300, "microprice_open": 301,
	"microprice_high": 302, "microprice_low": 303, "microprice_avg": 304,

	// ── Orderbook depth: 10 bps ─────────────────────────────────────────
	"bid_liq_10_close": 310, "ask_liq_10_close": 311,
	"bid_liq_10_open": 312, "ask_liq_10_open": 313,
	"bid_liq_10_high": 314, "ask_liq_10_high": 315,
	"bid_liq_10_low": 316, "ask_liq_10_low": 317,
	"bid_liq_10_avg": 318, "ask_liq_10_avg": 319,
	"imbalance_10_close": 320, "imbalance_10_open": 321,
	"imbalance_10_high": 322, "imbalance_10_low": 323, "imbalance_10_avg": 324,

	// ── Orderbook depth: 20 bps ─────────────────────────────────────────
	"bid_liq_20_close": 330, "ask_liq_20_close": 331,
	"bid_liq_20_open": 332, "ask_liq_20_open": 333,
	"bid_liq_20_high": 334, "ask_liq_20_high": 335,
	"bid_liq_20_low": 336, "ask_liq_20_low": 337,
	"bid_liq_20_avg": 338, "ask_liq_20_avg": 339,
	"imbalance_20_close": 340, "imbalance_20_open": 341,
	"imbalance_20_high": 342, "imbalance_20_low": 343, "imbalance_20_avg": 344,

	// ── Orderbook depth: 50 bps ─────────────────────────────────────────
	"bid_liq_50_close": 350, "ask_liq_50_close": 351,
	"bid_liq_50_open": 352, "ask_liq_50_open": 353,
	"bid_liq_50_high": 354, "ask_liq_50_high": 355,
	"bid_liq_50_low": 356, "ask_liq_50_low": 357,
	"bid_liq_50_avg": 358, "ask_liq_50_avg": 359,
	"imbalance_50_close": 360, "imbalance_50_open": 361,
	"imbalance_50_high": 362, "imbalance_50_low": 363, "imbalance_50_avg": 364,

	// ── Orderbook depth: 100 bps ────────────────────────────────────────
	"bid_liq_100_close": 370, "ask_liq_100_close": 371,
	"bid_liq_100_open": 372, "ask_liq_100_open": 373,
	"bid_liq_100_high": 374, "ask_liq_100_high": 375,
	"bid_liq_100_low": 376, "ask_liq_100_low": 377,
	"bid_liq_100_avg": 378, "ask_liq_100_avg": 379,
	"imbalance_100_close": 380, "imbalance_100_open": 381,
	"imbalance_100_high": 382, "imbalance_100_low": 383, "imbalance_100_avg": 384,

	// ── Orderbook depth: snapshot count ─────────────────────────────────
	"snapshot_count": 390,

	// ── Noisy / IDs — push to end ───────────────────────────────────────
	"block_trade_buy_volume": 900, "block_trade_sell_volume": 901,
	"liquidation_long_volume": 902, "liquidation_short_volume": 903,
	"trade_id": 910, "combo_id": 911, "combo_trade_id": 912,
	"block_trade_id": 913, "tick_direction": 914,
	"oi_before": 915, "strategy": 916,
}

func columnWeight(name string) int {
	if w, ok := columnPriorities[name]; ok {
		return w
	}
	return 500
}

func sliceOfMapsToRows(v reflect.Value) [][]string {
	// Collect all unique keys in order of first appearance
	keyOrder := []string{}
	keySet := map[string]bool{}

	for i := 0; i < v.Len(); i++ {
		item := v.Index(i)
		for item.Kind() == reflect.Interface {
			item = item.Elem()
		}
		if item.Kind() != reflect.Map {
			continue
		}
		for _, key := range item.MapKeys() {
			k := fmt.Sprintf("%v", key.Interface())
			if !keySet[k] {
				keySet[k] = true
				keyOrder = append(keyOrder, k)
			}
		}
	}

	if len(keyOrder) == 0 {
		return nil
	}

	// Drop redundant columns: raw "timestamp" when "minute" or "date" exists
	if (keySet["minute"] || keySet["date"]) && keySet["timestamp"] {
		filtered := keyOrder[:0]
		for _, k := range keyOrder {
			if k != "timestamp" {
				filtered = append(filtered, k)
			}
		}
		keyOrder = filtered
	}

	// Sort columns by priority — important fields first, noisy fields last
	sort.SliceStable(keyOrder, func(i, j int) bool {
		return columnWeight(keyOrder[i]) < columnWeight(keyOrder[j])
	})

	rows := [][]string{keyOrder}

	for i := 0; i < v.Len(); i++ {
		item := v.Index(i)
		for item.Kind() == reflect.Interface {
			item = item.Elem()
		}
		row := make([]string, len(keyOrder))
		for j, key := range keyOrder {
			val := item.MapIndex(reflect.ValueOf(key))
			if val.IsValid() {
				row[j] = formatValue(val.Interface())
			}
		}
		rows = append(rows, row)
	}

	return rows
}

func mapToRows(v reflect.Value) [][]string {
	rows := [][]string{{"Key", "Value"}}
	for _, key := range v.MapKeys() {
		k := fmt.Sprintf("%v", key.Interface())
		val := v.MapIndex(key)
		rows = append(rows, []string{k, formatValue(val.Interface())})
	}
	return rows
}

// formatValue converts a raw value to its string representation.
// Numbers are kept as plain strings here — formatNumber() is applied
// later only for table output.
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case float64:
		// Avoid scientific notation for large numbers
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

// ─── Colored message helpers ────────────────────────────────────────────────

const (
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBold   = "\033[1m"
	ansiReset  = "\033[0m"
)

// Errorf prints a red ✗ prefixed error message to stderr.
func Errorf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s%s✗ %s%s\n", ansiBold, ansiRed, msg, ansiReset)
}

// Successf prints a green ✓ prefixed success message to stderr.
func Successf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s%s✓ %s%s\n", ansiBold, ansiGreen, msg, ansiReset)
}

// Warnf prints a yellow warning message to stderr.
func Warnf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(os.Stderr, "%s%s⚠ %s%s\n", ansiBold, ansiYellow, msg, ansiReset)
}

// PrintError outputs a structured error.
func PrintError(format Format, err error) {
	if format == FormatJSON {
		errObj := map[string]string{"error": err.Error()}
		data, _ := json.Marshal(errObj)
		fmt.Fprintln(os.Stderr, string(data))
	} else {
		Errorf("%s", err.Error())
	}
}
