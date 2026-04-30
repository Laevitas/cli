package agg

import (
	"math"
	"testing"
)

// approxEq checks that two floats are equal within a small epsilon —
// every numerical test uses this rather than `==` so floating-point
// drift doesn't cause spurious failures. 1e-9 is comfortably tighter
// than any precision we care about and looser than a typical IEEE
// rounding error.
func approxEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// ─── ConsolidatedBBO ───────────────────────────────────────────────────────

func TestConsolidatedBBO_PicksGlobalBest(t *testing.T) {
	books := []VenueBook{
		{
			Venue: "binance",
			Bids:  []VenueLevel{{Price: 100.0, Size: 1.0}},
			Asks:  []VenueLevel{{Price: 100.5, Size: 0.5}},
		},
		{
			Venue: "bybit",
			Bids:  []VenueLevel{{Price: 100.2, Size: 2.0}}, // higher bid wins
			Asks:  []VenueLevel{{Price: 100.6, Size: 1.0}},
		},
		{
			Venue: "okx",
			Bids:  []VenueLevel{{Price: 99.8, Size: 5.0}},
			Asks:  []VenueLevel{{Price: 100.4, Size: 0.2}}, // lower ask wins
		},
	}
	bid, ask := ConsolidatedBBO(books)
	if bid.Venue != "bybit" || !approxEq(bid.Price, 100.2) {
		t.Errorf("best bid: got %+v, want venue=bybit price=100.2", bid)
	}
	if ask.Venue != "okx" || !approxEq(ask.Price, 100.4) {
		t.Errorf("best ask: got %+v, want venue=okx price=100.4", ask)
	}
}

func TestConsolidatedBBO_SamePriceLargerSizeWins(t *testing.T) {
	books := []VenueBook{
		{Venue: "a", Bids: []VenueLevel{{Price: 100.0, Size: 1.0}}},
		{Venue: "b", Bids: []VenueLevel{{Price: 100.0, Size: 5.0}}}, // tie on price, more size
	}
	bid, _ := ConsolidatedBBO(books)
	if bid.Venue != "b" {
		t.Errorf("tie-break on size: got venue=%s, want b", bid.Venue)
	}
}

func TestConsolidatedBBO_EmptyBookSkipped(t *testing.T) {
	books := []VenueBook{
		{Venue: "empty"}, // both sides empty; must not crash, must not contribute
		{Venue: "real", Bids: []VenueLevel{{Price: 50.0, Size: 1.0}}, Asks: []VenueLevel{{Price: 51.0, Size: 1.0}}},
	}
	bid, ask := ConsolidatedBBO(books)
	if bid.Venue != "real" || ask.Venue != "real" {
		t.Errorf("empty venue must be skipped: bid=%+v ask=%+v", bid, ask)
	}
}

// ─── AggregatedDepth ───────────────────────────────────────────────────────

func TestAggregatedDepth_MergesIdenticalPrices(t *testing.T) {
	books := []VenueBook{
		{Venue: "a", Bids: []VenueLevel{{Price: 100.0, Size: 0.4}, {Price: 99.5, Size: 1.0}}},
		{Venue: "b", Bids: []VenueLevel{{Price: 100.0, Size: 0.6}}}, // same price as a's top
	}
	_, bids := AggregatedDepth(books)
	if len(bids) != 2 {
		t.Fatalf("expected 2 distinct prices, got %d: %+v", len(bids), bids)
	}
	if !approxEq(bids[0].Price, 100.0) || !approxEq(bids[0].Size, 1.0) {
		t.Errorf("merged level wrong: %+v", bids[0])
	}
	if len(bids[0].Sources) != 2 || bids[0].Sources[0] != "a" || bids[0].Sources[1] != "b" {
		t.Errorf("sources wrong: %+v", bids[0].Sources)
	}
}

func TestAggregatedDepth_AsksAscendBidsDescend(t *testing.T) {
	books := []VenueBook{
		{
			Venue: "x",
			Asks:  []VenueLevel{{Price: 101, Size: 1}, {Price: 102, Size: 1}, {Price: 103, Size: 1}},
			Bids:  []VenueLevel{{Price: 100, Size: 1}, {Price: 99, Size: 1}, {Price: 98, Size: 1}},
		},
	}
	asks, bids := AggregatedDepth(books)
	if !(asks[0].Price < asks[1].Price && asks[1].Price < asks[2].Price) {
		t.Errorf("asks not ascending: %+v", asks)
	}
	if !(bids[0].Price > bids[1].Price && bids[1].Price > bids[2].Price) {
		t.Errorf("bids not descending: %+v", bids)
	}
}

// ─── VolumeWeightedMid ─────────────────────────────────────────────────────

func TestVolumeWeightedMid_SymmetricBookGivesMid(t *testing.T) {
	// Equal sizes on both sides → microprice equals plain mid.
	books := []VenueBook{
		{
			Venue: "v",
			Bids:  []VenueLevel{{Price: 100, Size: 1}},
			Asks:  []VenueLevel{{Price: 102, Size: 1}},
		},
	}
	got := VolumeWeightedMid(books)
	if !approxEq(got, 101.0) {
		t.Errorf("symmetric microprice: got %v, want 101", got)
	}
}

func TestVolumeWeightedMid_AskHeavyTiltsTowardBid(t *testing.T) {
	// More size on the ask → microprice tilts toward bid (less likely
	// to fill at ask side because it's "denser" / more ready to absorb).
	books := []VenueBook{
		{
			Venue: "v",
			Bids:  []VenueLevel{{Price: 100, Size: 1}},
			Asks:  []VenueLevel{{Price: 102, Size: 9}},
		},
	}
	// num = 100·9 + 102·1 = 1002; den = 1+9 = 10; mid = 100.2
	got := VolumeWeightedMid(books)
	if !approxEq(got, 100.2) {
		t.Errorf("ask-heavy microprice: got %v, want 100.2", got)
	}
}

// ─── WeightedMean / OIWeighted / VolumeWeighted ────────────────────────────

func TestWeightedMean_HandCalculated(t *testing.T) {
	// (0.01·100 + 0.02·200 + 0.03·700) / (100+200+700)
	// = (1 + 4 + 21) / 1000 = 26/1000 = 0.026
	quotes := []VenueQuote{
		{Venue: "a", Value: 0.01, Weight: 100},
		{Venue: "b", Value: 0.02, Weight: 200},
		{Venue: "c", Value: 0.03, Weight: 700},
	}
	got := WeightedMean(quotes)
	if !approxEq(got, 0.026) {
		t.Errorf("weighted mean: got %v, want 0.026", got)
	}
}

func TestWeightedMean_AllZeroWeightsReturnsZero(t *testing.T) {
	quotes := []VenueQuote{{Value: 1, Weight: 0}, {Value: 2, Weight: 0}}
	if got := WeightedMean(quotes); got != 0 {
		t.Errorf("zero-weight: got %v, want 0", got)
	}
}

// ─── CrossVenueDivergence ──────────────────────────────────────────────────

func TestCrossVenueDivergence_SpreadAndExtremes(t *testing.T) {
	quotes := []VenueQuote{
		{Venue: "a", Value: 0.01, Weight: 1},
		{Venue: "b", Value: 0.05, Weight: 1}, // highest
		{Venue: "c", Value: -0.02, Weight: 1}, // lowest
	}
	r := CrossVenueDivergence(quotes)
	if r.HighVenue != "b" || r.LowVenue != "c" {
		t.Errorf("extremes wrong: %+v", r)
	}
	if !approxEq(r.Spread, 0.07) {
		t.Errorf("spread: got %v, want 0.07", r.Spread)
	}
}

func TestCrossVenueDivergence_LessThanTwoReturnsZero(t *testing.T) {
	quotes := []VenueQuote{{Venue: "only", Value: 1, Weight: 1}}
	r := CrossVenueDivergence(quotes)
	if r.Spread != 0 || r.HighVenue != "" {
		t.Errorf("single-venue must return zero result: %+v", r)
	}
}

// ─── TermStructureInterp ───────────────────────────────────────────────────

func TestTermStructureInterp_LinearForBasis(t *testing.T) {
	// Two points: 7d=0.10, 30d=0.20. At 14d, t=(14-7)/(30-7)=7/23.
	// Value = 0.10 + 7/23·(0.20-0.10) = 0.10 + 0.04347... = 0.14347...
	points := []TermPoint{{TenorDays: 7, Value: 0.10}, {TenorDays: 30, Value: 0.20}}
	got := TermStructureInterp(points, 14, false)
	expected := 0.10 + (7.0/23.0)*0.10
	if !approxEq(got, expected) {
		t.Errorf("linear interp: got %v, want %v", got, expected)
	}
}

func TestTermStructureInterp_VarianceLinearForIV(t *testing.T) {
	// Two points: 7d IV=0.5, 30d IV=0.7. Variance: 0.25 → 0.49.
	// At 14d, variance = 0.25 + 7/23·(0.49-0.25) = 0.25 + 7/23·0.24
	// = 0.25 + 0.0730434... = 0.3230434... → IV = sqrt(...) ≈ 0.56837
	points := []TermPoint{{TenorDays: 7, Value: 0.5}, {TenorDays: 30, Value: 0.7}}
	got := TermStructureInterp(points, 14, true)
	expected := math.Sqrt(0.25 + (7.0/23.0)*(0.49-0.25))
	if !approxEq(got, expected) {
		t.Errorf("IV interp: got %v, want %v", got, expected)
	}
}

func TestTermStructureInterp_FlatExtrapolatesAtEdges(t *testing.T) {
	points := []TermPoint{{TenorDays: 7, Value: 0.10}, {TenorDays: 30, Value: 0.20}}
	if got := TermStructureInterp(points, 1, false); !approxEq(got, 0.10) {
		t.Errorf("below first: got %v, want 0.10", got)
	}
	if got := TermStructureInterp(points, 90, false); !approxEq(got, 0.20) {
		t.Errorf("above last: got %v, want 0.20", got)
	}
}

// ─── Skew25Delta ───────────────────────────────────────────────────────────

func TestSkew25Delta_Positive(t *testing.T) {
	// Puts more expensive than calls → positive skew.
	smile := []DeltaIV{
		{Delta: 0.10, IV: 0.80, IsCall: false},
		{Delta: 0.25, IV: 0.70, IsCall: false},
		{Delta: 0.50, IV: 0.60, IsCall: false},
		{Delta: 0.10, IV: 0.50, IsCall: true},
		{Delta: 0.25, IV: 0.55, IsCall: true},
		{Delta: 0.50, IV: 0.60, IsCall: true},
	}
	got := Skew25Delta(smile)
	if got <= 0 {
		t.Errorf("expected positive skew, got %v", got)
	}
}

func TestSkew25Delta_OneSideEmptyReturnsZero(t *testing.T) {
	smile := []DeltaIV{{Delta: 0.25, IV: 0.7, IsCall: false}}
	if got := Skew25Delta(smile); got != 0 {
		t.Errorf("one-sided smile: got %v, want 0", got)
	}
}

// ─── ATMIVFromChain ────────────────────────────────────────────────────────

func TestATMIVFromChain_NearestStrike(t *testing.T) {
	strikes := []ChainStrike{
		{Strike: 75000, CallIV: 0.50, PutIV: 0.55, Forward: 76200},
		{Strike: 76000, CallIV: 0.45, PutIV: 0.50, Forward: 76200},
		{Strike: 76500, CallIV: 0.42, PutIV: 0.48, Forward: 76200}, // nearest to 76200 forward
		{Strike: 77000, CallIV: 0.40, PutIV: 0.45, Forward: 76200},
	}
	// Distance: |75000−76200|=1200, |76000−76200|=200, |76500−76200|=300, |77000−76200|=800
	// Wait — 76000 is closest (200 < 300). Let me re-check.
	// Yes, 76000 wins. ATM IV = (0.45 + 0.50)/2 = 0.475
	got := ATMIVFromChain(strikes)
	if !approxEq(got, 0.475) {
		t.Errorf("ATM IV: got %v, want 0.475", got)
	}
}

func TestATMIVFromChain_HandlesNaNSides(t *testing.T) {
	strikes := []ChainStrike{
		{Strike: 76000, CallIV: math.NaN(), PutIV: 0.55, Forward: 76000},
	}
	got := ATMIVFromChain(strikes)
	if !approxEq(got, 0.55) {
		t.Errorf("NaN call: got %v, want 0.55", got)
	}
}

// ─── RealizedVolatility ────────────────────────────────────────────────────

func TestRealizedVolatility_ConstantReturnsZero(t *testing.T) {
	returns := []float64{0.01, 0.01, 0.01, 0.01, 0.01}
	got := RealizedVolatility(returns, 252)
	if got != 0 {
		t.Errorf("constant returns: got %v, want 0", got)
	}
}

func TestRealizedVolatility_KnownDistribution(t *testing.T) {
	// Returns: -0.01, 0.0, 0.01. Mean = 0; sample variance = ((-0.01)^2 + 0 + 0.01^2)/2 = 0.0001
	// RV (annualised at 252) = sqrt(252·0.0001) = sqrt(0.0252) ≈ 0.15875
	returns := []float64{-0.01, 0.0, 0.01}
	got := RealizedVolatility(returns, 252)
	expected := math.Sqrt(252 * 0.0001)
	if !approxEq(got, expected) {
		t.Errorf("RV: got %v, want %v", got, expected)
	}
}

func TestRealizedVolatility_SkipsNaN(t *testing.T) {
	returns := []float64{-0.01, math.NaN(), 0.01}
	got := RealizedVolatility(returns, 252)
	// After skipping NaN: [-0.01, 0.01], same as above pair → variance 0.0002, RV = sqrt(252·0.0002)
	expected := math.Sqrt(252 * 0.0002)
	if !approxEq(got, expected) {
		t.Errorf("RV with NaN: got %v, want %v", got, expected)
	}
}

// ─── RealizedVsImplied ─────────────────────────────────────────────────────

func TestRealizedVsImplied_Ratio(t *testing.T) {
	// IV=0.6, RV=0.4: IV is 50% richer than RV.
	got := RealizedVsImplied(0.6, 0.4)
	if !approxEq(got, 0.5) {
		t.Errorf("IV/RV ratio: got %v, want 0.5", got)
	}
}

func TestRealizedVsImplied_ZeroRVReturnsZero(t *testing.T) {
	if got := RealizedVsImplied(0.5, 0); got != 0 {
		t.Errorf("RV=0: got %v, want 0", got)
	}
}

// ─── PercentileRank ────────────────────────────────────────────────────────

func TestPercentileRank_Median(t *testing.T) {
	dist := []float64{1, 2, 3, 4, 5}
	got := PercentileRank(dist, 3) // 2 below 3
	if !approxEq(got, 2.0/5.0) {
		t.Errorf("median rank: got %v, want %v", got, 2.0/5.0)
	}
}

func TestPercentileRank_AboveMax(t *testing.T) {
	dist := []float64{1, 2, 3}
	got := PercentileRank(dist, 99) // all 3 below
	if !approxEq(got, 1.0) {
		t.Errorf("above max: got %v, want 1.0", got)
	}
}

func TestPercentileRank_BelowMin(t *testing.T) {
	dist := []float64{10, 20, 30}
	got := PercentileRank(dist, 0) // 0 below
	if !approxEq(got, 0.0) {
		t.Errorf("below min: got %v, want 0.0", got)
	}
}

func TestPercentileRank_EmptyReturnsHalf(t *testing.T) {
	if got := PercentileRank(nil, 5); got != 0.5 {
		t.Errorf("empty distribution: got %v, want 0.5", got)
	}
}

// ─── Funding8hEquivalent ───────────────────────────────────────────────────

func TestFunding8hEquivalent_Linear(t *testing.T) {
	// Hourly funding of 0.001 → 8h equivalent = 0.001·8 = 0.008.
	got := Funding8hEquivalent(0.001, 1)
	if !approxEq(got, 0.008) {
		t.Errorf("hourly→8h: got %v, want 0.008", got)
	}
	// 4h funding of 0.002 → 8h = 0.002·2 = 0.004.
	got = Funding8hEquivalent(0.002, 4)
	if !approxEq(got, 0.004) {
		t.Errorf("4h→8h: got %v, want 0.004", got)
	}
	// 8h funding of 0.0005 → unchanged.
	got = Funding8hEquivalent(0.0005, 8)
	if !approxEq(got, 0.0005) {
		t.Errorf("8h→8h: got %v, want 0.0005", got)
	}
}

func TestFunding8hEquivalent_ZeroIntervalReturnsZero(t *testing.T) {
	if got := Funding8hEquivalent(0.001, 0); got != 0 {
		t.Errorf("zero interval: got %v, want 0", got)
	}
}

// ─── BestVenueByMetric ─────────────────────────────────────────────────────

func TestBestVenueByMetric_Highest(t *testing.T) {
	quotes := []VenueQuote{
		{Venue: "a", Value: 0.01, Weight: 1},
		{Venue: "b", Value: 0.05, Weight: 1},
		{Venue: "c", Value: 0.03, Weight: 1},
	}
	v, val := BestVenueByMetric(quotes, true)
	if v != "b" || !approxEq(val, 0.05) {
		t.Errorf("highest: got %s=%v, want b=0.05", v, val)
	}
}

func TestBestVenueByMetric_Lowest(t *testing.T) {
	quotes := []VenueQuote{
		{Venue: "a", Value: 0.01, Weight: 1},
		{Venue: "b", Value: 0.05, Weight: 1},
		{Venue: "c", Value: -0.02, Weight: 1},
	}
	v, val := BestVenueByMetric(quotes, false)
	if v != "c" || !approxEq(val, -0.02) {
		t.Errorf("lowest: got %s=%v, want c=-0.02", v, val)
	}
}

func TestBestVenueByMetric_StableTieBreak(t *testing.T) {
	quotes := []VenueQuote{
		{Venue: "first", Value: 0.05, Weight: 1},
		{Venue: "second", Value: 0.05, Weight: 1}, // tied
	}
	v, _ := BestVenueByMetric(quotes, true)
	if v != "first" {
		t.Errorf("tie should go to first input: got %s", v)
	}
}
