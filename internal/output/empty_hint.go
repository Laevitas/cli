package output

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// EmptyContext describes a successful request that returned zero
// rows. It lets table output explain what was queried without
// changing JSON/CSV envelopes that agents parse.
type EmptyContext struct {
	Endpoint   string
	Instrument string
	Exchange   string
	Start      string
	End        string
	Resolution string
}

var deribitOptionNameRE = regexp.MustCompile(`(?i)^[A-Z]+-\d{1,2}[A-Z]{3}\d{2}-\d+-[CP]$`)

// RenderEmptyContext renders the table-mode empty-result block.
func RenderEmptyContext(ctx EmptyContext) string {
	line := "No data"
	if ctx.Endpoint != "" {
		line += " for " + ctx.Endpoint
	}
	if ctx.Instrument != "" {
		line += " · " + ctx.Instrument
		if ctx.Exchange != "" {
			line += " on " + ctx.Exchange
		}
	} else if ctx.Exchange != "" {
		line += " · exchange " + ctx.Exchange
	}
	if ctx.Start != "" || ctx.End != "" {
		line += " · " + formatEmptyWindow(ctx.Start, ctx.End)
	}
	if ctx.Resolution != "" {
		line += " · " + ctx.Resolution
	}

	if hint := EmptyHint(ctx); hint != "" {
		return line + "\n" + hint
	}
	return line
}

// EmptyHint returns a local, best-effort hint for common
// exchange-specific instrument naming mismatches.
func EmptyHint(ctx EmptyContext) string {
	exchange := strings.ToLower(strings.TrimSpace(ctx.Exchange))
	endpoint := strings.ToLower(strings.TrimSpace(ctx.Endpoint))
	instrument := strings.ToUpper(strings.TrimSpace(ctx.Instrument))

	if instrument != "" && exchange == "deribit" {
		if base, ok := trimLinearQuote(instrument); ok {
			return fmt.Sprintf("Hint: %s looks like a Binance/Bybit-style linear perp symbol; Deribit names its %s perp %s-PERPETUAL. Try %s-PERPETUAL on deribit, or %s --exchange binance.", instrument, base, base, base, instrument)
		}
	}

	if instrument != "" && exchange != "deribit" && strings.HasSuffix(instrument, "-PERPETUAL") {
		return fmt.Sprintf("Hint: %s is Deribit-style perp naming. Try %s --exchange deribit.", instrument, instrument)
	}
	if instrument != "" && exchange != "deribit" && deribitOptionNameRE.MatchString(instrument) {
		return fmt.Sprintf("Hint: %s looks like a Deribit option name. Try %s --exchange deribit.", instrument, instrument)
	}
	if strings.Contains(endpoint, "/liquidations") && exchange != "" {
		return fmt.Sprintf("Hint: %s may not publish liquidation events to this gateway. Try --exchange binance or --exchange bybit for known coverage.", exchange)
	}
	return ""
}

func trimLinearQuote(instrument string) (string, bool) {
	for _, suffix := range []string{"USDT", "USDC"} {
		if strings.HasSuffix(instrument, suffix) && len(instrument) > len(suffix) {
			return strings.TrimSuffix(instrument, suffix), true
		}
	}
	return "", false
}

func formatEmptyWindow(start, end string) string {
	switch {
	case start != "" && end != "":
		window := start + " → " + end
		if suffix := emptyWindowSuffix(start, end); suffix != "" {
			window += " (" + suffix + ")"
		}
		return window
	case start != "":
		return "from " + start
	case end != "":
		return "until " + end
	default:
		return ""
	}
}

func emptyWindowSuffix(start, end string) string {
	const layout = "2006-01-02T15:04:05Z"
	startTime, errStart := time.Parse(layout, start)
	endTime, errEnd := time.Parse(layout, end)
	if errStart != nil || errEnd != nil || endTime.Before(startTime) {
		return ""
	}
	d := endTime.Sub(startTime)
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return ""
}
