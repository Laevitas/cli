package tapefilter

import "fmt"

var presets = []float64{0, 1_000, 10_000, 50_000, 100_000, 500_000}

func Presets() []float64 {
	out := make([]float64, len(presets))
	copy(out, presets)
	return out
}

func Next(current float64) float64 {
	for i, v := range presets {
		if current == v {
			return presets[(i+1)%len(presets)]
		}
		if current < v {
			return v
		}
	}
	return presets[0]
}

func AllowsNotional(notional, min float64) bool {
	return min <= 0 || notional >= min
}

func Label(min float64) string {
	if min <= 0 {
		return "all"
	}
	return formatUSD(min)
}

func formatUSD(v float64) string {
	if v >= 1_000_000_000 {
		return fmt.Sprintf("$%.1fB", v/1_000_000_000)
	}
	if v >= 1_000_000 {
		return fmt.Sprintf("$%.1fM", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("$%.0fK", v/1_000)
	}
	return fmt.Sprintf("$%.0f", v)
}
