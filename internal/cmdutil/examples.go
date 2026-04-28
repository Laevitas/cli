package cmdutil

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// SubstituteExamplesRecursive walks cmd and all its descendants, applying
// SubstituteExampleTokens to each command's Long, Example, and Short strings.
// Call this once per command group from init() after the tree is wired up.
func SubstituteExamplesRecursive(cmd *cobra.Command) {
	cmd.Long = SubstituteExampleTokens(cmd.Long)
	cmd.Example = SubstituteExampleTokens(cmd.Example)
	cmd.Short = SubstituteExampleTokens(cmd.Short)
	for _, c := range cmd.Commands() {
		SubstituteExamplesRecursive(c)
	}
}

// formatDeribitDate renders t in Deribit's instrument-name date format: DMMMYY,
// upper-case month abbreviation, no zero-padding on the day. e.g. "8MAY26".
func formatDeribitDate(t time.Time) string {
	return fmt.Sprintf("%d%s%02d", t.Day(), strings.ToUpper(t.Month().String()[:3]), t.Year()%100)
}

// nextQuarterlyExpiry returns the last Friday of the third month after now —
// i.e. roughly the next standard quarterly options/futures expiry. The result
// is approximate; exchanges may not list this exact date. It exists only so
// help-text examples never rot to obviously-expired dates.
func nextQuarterlyExpiry(now time.Time) time.Time {
	target := now.AddDate(0, 3, 0)
	// Move to first of the month, then to the last day.
	first := time.Date(target.Year(), target.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1)
	// Walk back to the most recent Friday on or before "last".
	for last.Weekday() != time.Friday {
		last = last.AddDate(0, 0, -1)
	}
	return last
}

// nextWeeklyExpiry returns the next Friday at least 7 days out — for examples
// where a near-term instrument is more illustrative (trades, ohlcvt -p 24h).
func nextWeeklyExpiry(now time.Time) time.Time {
	d := now.AddDate(0, 0, 7)
	for d.Weekday() != time.Friday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// ExampleFuturesInstrument returns a plausible active futures instrument name
// for the given base currency, e.g. "BTC-26JUN26". Used only in help text.
func ExampleFuturesInstrument(currency string) string {
	return currency + "-" + formatDeribitDate(nextQuarterlyExpiry(time.Now().UTC()))
}

// ExampleNearTermFuturesInstrument returns a near-term (weekly) futures
// instrument name, e.g. "BTC-8MAY26". Useful for examples that fetch short
// time windows like -p 24h.
func ExampleNearTermFuturesInstrument(currency string) string {
	return currency + "-" + formatDeribitDate(nextWeeklyExpiry(time.Now().UTC()))
}

// ExampleOptionInstrument returns a plausible active option name with a
// round-ish strike, e.g. "BTC-26JUN26-100000-C".
func ExampleOptionInstrument(currency string, strike int, putCall string) string {
	pc := strings.ToUpper(putCall)
	if pc != "C" && pc != "P" {
		pc = "C"
	}
	return fmt.Sprintf("%s-%s-%d-%s",
		currency,
		formatDeribitDate(nextQuarterlyExpiry(time.Now().UTC())),
		strike,
		pc,
	)
}

// ExampleMaturity returns just the date portion of an instrument name, e.g.
// "26JUN26", for use with --maturity filters.
func ExampleMaturity() string {
	return formatDeribitDate(nextQuarterlyExpiry(time.Now().UTC()))
}

// SubstituteExampleTokens replaces {{FUT}}, {{OPT_C}}, {{OPT_P}}, and {{MAT}}
// placeholders in cobra Example/Long strings with concrete instrument names
// computed at startup. Call from init() after registering commands.
//
// Tokens:
//
//	{{FUT}}    → BTC-<near-term quarterly expiry>      (e.g. BTC-26JUN26)
//	{{OPT_C}}  → BTC-<expiry>-100000-C                  (e.g. BTC-26JUN26-100000-C)
//	{{OPT_P}}  → BTC-<expiry>-100000-P
//	{{MAT}}    → <expiry>                                (e.g. 26JUN26)
func SubstituteExampleTokens(s string) string {
	if s == "" {
		return s
	}
	fut := ExampleFuturesInstrument("BTC")
	optC := ExampleOptionInstrument("BTC", 100000, "C")
	optP := ExampleOptionInstrument("BTC", 100000, "P")
	mat := ExampleMaturity()
	r := strings.NewReplacer(
		"{{FUT}}", fut,
		"{{OPT_C}}", optC,
		"{{OPT_P}}", optP,
		"{{MAT}}", mat,
	)
	return r.Replace(s)
}
