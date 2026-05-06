package ladder

import "testing"

func TestAdaptiveGroupTicksScaleWithReferencePrice(t *testing.T) {
	high := AdaptiveGroupTicks(80_000)
	low := AdaptiveGroupTicks(0.00003)

	if len(high) < 2 || high[1] != 0.1 {
		t.Fatalf("high-price first group = %v, want 0.1 in %v", groupAt(high, 1), high)
	}
	if len(low) < 2 || low[1] >= 0.000001 {
		t.Fatalf("low-price first group = %v, want sub-micro bucket in %v", groupAt(low, 1), low)
	}
	if high[1] == low[1] {
		t.Fatalf("adaptive groups did not scale: high=%v low=%v", high, low)
	}
}

func TestAdaptiveGroupTickNavigation(t *testing.T) {
	tests := []struct {
		name string
		curr float64
		ref  float64
		next float64
		prev float64
	}{
		{name: "btc first step", curr: 0, ref: 80_000, next: 0.1, prev: 0},
		{name: "btc middle step", curr: 1, ref: 80_000, next: 5, prev: 0.5},
		{name: "micro first step", curr: 0, ref: 0.00003, next: 0.00000000005, prev: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextAdaptiveGroupTick(tt.curr, tt.ref); got != tt.next {
				t.Fatalf("next = %g, want %g", got, tt.next)
			}
			if got := PrevAdaptiveGroupTick(tt.curr, tt.ref); got != tt.prev {
				t.Fatalf("prev = %g, want %g", got, tt.prev)
			}
		})
	}
}

func TestGroupLabelRendersTinyBuckets(t *testing.T) {
	if got := GroupLabel(0.00000000005); got != "0.00000000005" {
		t.Fatalf("GroupLabel tiny = %q, want %q", got, "0.00000000005")
	}
}

func groupAt(groups []float64, i int) float64 {
	if i >= len(groups) {
		return 0
	}
	return groups[i]
}
