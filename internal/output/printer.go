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
		isTimestamp[i] = isTimestampHeader(hl)
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
			if isTimestamp[c] && cell != "" {
				displayRows[r][c] = formatRelativeTime(cell)
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

func isTimestampHeader(h string) bool {
	tsKeywords := []string{
		"timestamp", "time", "datetime", "date", "created_at", "updated_at",
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

	now := time.Now().UTC()
	diff := now.Sub(t)
	if diff < 0 {
		diff = -diff
		return "in " + humanDuration(diff)
	}

	return humanDuration(diff) + " ago"
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
