package tapefilter

import "testing"

func TestNextCyclesPresets(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0, 1_000},
		{1_000, 10_000},
		{10_000, 50_000},
		{50_000, 100_000},
		{100_000, 500_000},
		{500_000, 0},
		{7_500, 10_000},
	}
	for _, c := range cases {
		if got := Next(c.in); got != c.want {
			t.Errorf("Next(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAllowsNotional(t *testing.T) {
	if !AllowsNotional(1, 0) {
		t.Fatal("zero filter should allow every positive notional")
	}
	if AllowsNotional(9_999, 10_000) {
		t.Fatal("notional below filter passed")
	}
	if !AllowsNotional(10_000, 10_000) {
		t.Fatal("notional equal to filter should pass")
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "all"},
		{1_000, "$1K"},
		{10_000, "$10K"},
		{500_000, "$500K"},
		{1_500_000, "$1.5M"},
	}
	for _, c := range cases {
		if got := Label(c.in); got != c.want {
			t.Errorf("Label(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
