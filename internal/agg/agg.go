// Package agg implements pure cross-venue aggregation primitives over
// market data snapshots. Every function here is a closed-form
// computation: no I/O, no goroutines, no shared state. Heavy unit-test
// coverage lives in agg_test.go.
//
// These primitives are the math layer the dashboard renderers
// (laevitas dash book / chain / perps / vol) call into. By keeping them
// independent of any UI framework we get three things:
//
//   - Reusability: the same ConsolidatedBBO function feeds the book
//     dashboard, the perp screener, and any future research notebook.
//   - Testability: every primitive is a `func(args) result` that can be
//     pinned with a table-driven test against hand-calculated values.
//   - Replaceability: if a primitive turns out to be wrong (e.g. the
//     skew formula needs to use 35-delta instead of 25-delta), one
//     function changes; no UI code touches it.
//
// Naming convention: every primitive name starts with the *output*
// concept ("Best…", "Aggregated…", "WeightedMid…", "TermStructure…"),
// not the input. That reads better at the call site (one verb per
// line) and groups related primitives in the godoc index.
package agg

import (
	"math"
	"sort"
)

// ─── shared input shapes ────────────────────────────────────────────────────

// VenueLevel is one (price, size) pair on a single venue's book. The
// renderer's `bookLevel` is the wire shape; we deliberately keep agg's
// input type local so the package has no upstream import.
type VenueLevel struct {
	Price float64
	Size  float64
}

// VenueBook is one venue's full top-N book at a point in time. The
// caller is responsible for passing pre-normalised inputs:
//
//   - Asks must be sorted ascending by price (best ask first).
//   - Bids must be sorted descending by price (best bid first).
//   - Empty venues (Asks and Bids both empty) are skipped — they
//     contribute nothing and simplify caller plumbing.
//
// The Venue field is the lowercase exchange tag ("binance", "okx").
// Every primitive that returns per-venue attribution echoes this
// string back so the renderer can colour-code by venue without doing
// its own bookkeeping.
type VenueBook struct {
	Venue string
	Asks  []VenueLevel
	Bids  []VenueLevel
}

// VenueQuote is the per-venue scalar metric form, used by primitives
// that don't need depth (perp screener, term-structure, percentile
// rank, weighted-metric helpers). Value carries the metric (funding,
// IV, basis…); Weight is the OI / volume / whatever the caller wants
// to weight by. Set Weight to 1.0 for unweighted operations.
type VenueQuote struct {
	Venue  string
	Value  float64
	Weight float64
}

// ─── 1. Consolidated best bid/ask ──────────────────────────────────────────

// BestQuote is the consolidated top-of-book on one side. Price is the
// best price across all venues, Size is its size (only that one
// venue's contribution — combine via AggregatedDepth if you want
// total size at the price). Venue echoes the venue holding the best
// price. Empty struct (Price == 0) means no input had any quotes on
// that side.
type BestQuote struct {
	Venue string
	Price float64
	Size  float64
}

// CrossVenueQuote captures the consolidated cost-to-cross plus any
// arb opportunity that exists when two venues' tops are crossed
// against each other. On a single venue the book is non-crossed by
// construction (best bid < best ask, always), but cross-venue the
// best bid on venue A can sit above the best ask on venue B.
//
// Spread semantics:
//   - If the consolidated book is non-crossed (best bid < best ask),
//     Spread = bestAsk - bestBid (the real cost to cross), Arb = 0.
//   - If crossed (best bid > best ask), Spread = 0 (you can't pay a
//     negative spread to cross), Arb = bestBid - bestAsk (the
//     instant-fill profit if you bought from BuyVenue and sold to
//     SellVenue).
//
// SpreadBps is computed against the midprice when non-crossed; zero
// when crossed (an arb has no spread basis).
type CrossVenueQuote struct {
	BestBid   BestQuote
	BestAsk   BestQuote
	Spread    float64 // ≥ 0 when non-crossed; 0 when crossed
	SpreadBps float64
	Arb       float64 // > 0 when crossed; 0 otherwise
	BuyVenue  string  // venue where you'd buy (lowest ask); empty when no arb
	SellVenue string  // venue where you'd sell (highest bid); empty when no arb
}

// ConsolidatedBBO returns the global best bid and best ask across
// every venue, plus a CrossVenueQuote that splits cost-to-cross
// from arb opportunity (the two are mutually exclusive — see the
// CrossVenueQuote doc for the cases).
//
// Tie-breaks on price go to the larger size (more "real" liquidity);
// ties on price+size go to the venue that appears first in the input
// slice (stable on caller's ordering). On the bid side "best" is the
// highest price; on the ask side "best" is the lowest. A venue with
// an empty book on a side simply doesn't contribute on that side;
// if no venue has any quotes on a side, the returned BestQuote on
// that side is zero-value.
func ConsolidatedBBO(books []VenueBook) (bestBid, bestAsk BestQuote) {
	for _, vb := range books {
		if len(vb.Bids) > 0 {
			b := vb.Bids[0]
			if b.Price > bestBid.Price ||
				(b.Price == bestBid.Price && b.Size > bestBid.Size) {
				bestBid = BestQuote{Venue: vb.Venue, Price: b.Price, Size: b.Size}
			}
		}
		if len(vb.Asks) > 0 {
			a := vb.Asks[0]
			betterPrice := bestAsk.Price == 0 || a.Price < bestAsk.Price
			samePriceLargerSize := a.Price == bestAsk.Price && a.Size > bestAsk.Size
			if betterPrice || samePriceLargerSize {
				bestAsk = BestQuote{Venue: vb.Venue, Price: a.Price, Size: a.Size}
			}
		}
	}
	return bestBid, bestAsk
}

// CrossVenueSpread computes the consolidated cost-to-cross or arb
// opportunity from the global BBO. Returns a CrossVenueQuote with
// non-overlapping Spread / Arb fields (see the CrossVenueQuote doc).
//
// Callers that need both the BestQuote pair AND the spread/arb math
// should prefer this over recomputing from ConsolidatedBBO at every
// call site.
func CrossVenueSpread(books []VenueBook) CrossVenueQuote {
	bb, ba := ConsolidatedBBO(books)
	q := CrossVenueQuote{BestBid: bb, BestAsk: ba}
	if bb.Price <= 0 || ba.Price <= 0 {
		return q
	}
	if ba.Price >= bb.Price {
		// Non-crossed: classic consolidated spread.
		q.Spread = ba.Price - bb.Price
		mid := (ba.Price + bb.Price) / 2
		if mid > 0 {
			q.SpreadBps = q.Spread / mid * 10_000
		}
		return q
	}
	// Crossed: best bid > best ask across two different venues. The
	// "spread" you'd pay is zero (you'd actually receive money to
	// cross); the arb is the difference. Buy at the lower ask, sell
	// at the higher bid.
	q.Arb = bb.Price - ba.Price
	q.BuyVenue = ba.Venue   // where you'd buy (lowest ask)
	q.SellVenue = bb.Venue  // where you'd sell (highest bid)
	return q
}

// ─── 2. Aggregated depth (price-by-price merge) ────────────────────────────

// AggregatedLevel is one price level after consolidation across
// venues. Size is the sum across all venues at that price; Sources
// names every venue contributing (in the order they appeared in the
// input slice). Useful for renderers that want a per-cell tooltip
// like "76,500 (binance 0.4 + bybit 0.6 + okx 0.2)".
type AggregatedLevel struct {
	Price   float64
	Size    float64
	Sources []string
}

// AggregatedDepth merges every venue's books into a single sorted
// depth ladder per side. Sides keep their natural ordering: asks
// ascending, bids descending. Levels at the same price across venues
// are summed into one entry, with Sources listing the contributing
// venues.
//
// This is the core of the aggregated-ladder dashboard view: instead
// of stacking N separate ladders side by side, the renderer draws
// one consolidated ladder and uses Sources to colour the size cell.
func AggregatedDepth(books []VenueBook) (asks, bids []AggregatedLevel) {
	asks = mergeSide(books, true)
	bids = mergeSide(books, false)
	return asks, bids
}

func mergeSide(books []VenueBook, ascending bool) []AggregatedLevel {
	byPrice := make(map[float64]*AggregatedLevel)
	for _, vb := range books {
		var levels []VenueLevel
		if ascending {
			levels = vb.Asks
		} else {
			levels = vb.Bids
		}
		for _, l := range levels {
			if l.Size <= 0 {
				continue
			}
			cur, ok := byPrice[l.Price]
			if !ok {
				cur = &AggregatedLevel{Price: l.Price}
				byPrice[l.Price] = cur
			}
			cur.Size += l.Size
			cur.Sources = append(cur.Sources, vb.Venue)
		}
	}
	out := make([]AggregatedLevel, 0, len(byPrice))
	for _, lv := range byPrice {
		out = append(out, *lv)
	}
	sort.Slice(out, func(i, j int) bool {
		if ascending {
			return out[i].Price < out[j].Price
		}
		return out[i].Price > out[j].Price
	})
	return out
}

// ─── 3. Weighted mid prices ────────────────────────────────────────────────

// VolumeWeightedMid returns the size-weighted mid using top-of-book
// from each venue. The classical microprice formula adapted to a
// multi-venue setting:
//
//	mid = Σ(bid·askSize + ask·bidSize) / Σ(bidSize + askSize)
//
// over every venue with both a bid and an ask. Reads as "the price
// you'd hit if you swept the consolidated book proportionally to
// each side's density." Returns 0 when no venue has both sides
// quoted.
func VolumeWeightedMid(books []VenueBook) float64 {
	num, den := 0.0, 0.0
	for _, vb := range books {
		if len(vb.Bids) == 0 || len(vb.Asks) == 0 {
			continue
		}
		b, a := vb.Bids[0], vb.Asks[0]
		num += b.Price*a.Size + a.Price*b.Size
		den += b.Size + a.Size
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// ─── 4. Weight-aggregated metrics ──────────────────────────────────────────

// WeightedMean computes Σ(value·weight) / Σ(weight) over the input
// quotes. Returns 0 when total weight is zero (all venues empty or
// all weights == 0). Used by OIWeightedMetric and VolumeWeightedMetric
// — those are just named call sites for this function with a
// specific weight semantic.
func WeightedMean(quotes []VenueQuote) float64 {
	num, den := 0.0, 0.0
	for _, q := range quotes {
		if q.Weight <= 0 {
			continue
		}
		num += q.Value * q.Weight
		den += q.Weight
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// OIWeightedMetric is the canonical "consolidated funding" computation:
// for each venue, weight the metric by its open interest. Caller
// passes VenueQuote{Value: fundingRate, Weight: openInterest, …}
// — which is exactly the shape the API returns.
func OIWeightedMetric(quotes []VenueQuote) float64 {
	return WeightedMean(quotes)
}

// VolumeWeightedMetric is OIWeightedMetric's sibling — same math,
// different intent. Use it when "where is the action" matters more
// than "where is the inventory" (e.g. weighting basis by 24h volume
// to highlight venues that are actively trading the spread).
func VolumeWeightedMetric(quotes []VenueQuote) float64 {
	return WeightedMean(quotes)
}

// ─── 5. Cross-venue divergence ─────────────────────────────────────────────

// Divergence is max(value) − min(value) across the venues in quotes,
// plus the venues holding those extremes. Returns zero-valued
// extremes when fewer than 2 venues are quoted (no divergence to
// report). Used to surface arb opportunities or "is one venue lying
// to me" warnings on the perp screener.
type DivergenceResult struct {
	Spread       float64
	HighVenue    string
	HighValue    float64
	LowVenue     string
	LowValue     float64
}

// CrossVenueDivergence computes the spread between the highest and
// lowest values across venues. If fewer than 2 quotes pass the
// non-zero-weight filter, returns a zero-value result (Spread == 0)
// — callers should check that before using HighVenue / LowVenue.
func CrossVenueDivergence(quotes []VenueQuote) DivergenceResult {
	first := true
	var r DivergenceResult
	count := 0
	for _, q := range quotes {
		if q.Weight <= 0 {
			continue
		}
		count++
		if first {
			r.HighVenue, r.HighValue = q.Venue, q.Value
			r.LowVenue, r.LowValue = q.Venue, q.Value
			first = false
			continue
		}
		if q.Value > r.HighValue {
			r.HighVenue, r.HighValue = q.Venue, q.Value
		}
		if q.Value < r.LowValue {
			r.LowVenue, r.LowValue = q.Venue, q.Value
		}
	}
	if count < 2 {
		return DivergenceResult{}
	}
	r.Spread = r.HighValue - r.LowValue
	return r
}

// ─── 6. Term-structure interpolation (IV / basis) ──────────────────────────

// TermPoint is a single observation on a term-structure curve —
// usually IV at a given expiry, or basis at a given tenor. TenorDays
// is the time-to-expiry in days (fractional OK); Value is the metric
// at that tenor.
type TermPoint struct {
	TenorDays float64
	Value     float64
}

// TermStructureInterp returns the value at tenorDays interpolated
// linearly between the nearest TermPoints in points. Points are
// expected to be sorted by TenorDays; if not, the function sorts a
// local copy so callers can pass unsorted data without surprise.
//
// For IV the convention is "linear in variance" — i.e. interpolate
// IV² and take the square root. The forIV flag toggles this:
//
//   - forIV == true:  interpolate Value² over time, return √result
//   - forIV == false: interpolate Value linearly (basis, funding…)
//
// Below the shortest tenor or above the longest, the function
// flat-extrapolates (returns the endpoint value). Real markets do
// weirder things at short tenors, but flat is the honest default.
func TermStructureInterp(points []TermPoint, tenorDays float64, forIV bool) float64 {
	if len(points) == 0 {
		return 0
	}
	if len(points) == 1 {
		return points[0].Value
	}
	sorted := make([]TermPoint, len(points))
	copy(sorted, points)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].TenorDays < sorted[j].TenorDays
	})
	if tenorDays <= sorted[0].TenorDays {
		return sorted[0].Value
	}
	if tenorDays >= sorted[len(sorted)-1].TenorDays {
		return sorted[len(sorted)-1].Value
	}
	for i := 1; i < len(sorted); i++ {
		if tenorDays > sorted[i].TenorDays {
			continue
		}
		lo, hi := sorted[i-1], sorted[i]
		t := (tenorDays - lo.TenorDays) / (hi.TenorDays - lo.TenorDays)
		if forIV {
			loV2, hiV2 := lo.Value*lo.Value, hi.Value*hi.Value
			v2 := loV2 + t*(hiV2-loV2)
			if v2 < 0 {
				return 0
			}
			return math.Sqrt(v2)
		}
		return lo.Value + t*(hi.Value-lo.Value)
	}
	return sorted[len(sorted)-1].Value
}

// ─── 7. Skew (25-delta put IV − 25-delta call IV) ──────────────────────────

// DeltaIV is one point on a smile: the delta (in absolute value, so
// 0.25 for both 25Δ call and 25Δ put) and the IV at that delta. The
// IsCall flag distinguishes the two sides — a put with delta = 0.25
// and a call with delta = 0.25 are different points on the smile.
type DeltaIV struct {
	Delta  float64 // absolute value, 0..1
	IV     float64
	IsCall bool
}

// Skew25Delta returns 25-delta put IV minus 25-delta call IV at a
// given tenor. Inputs are interpolated to delta = 0.25 on each side
// using TermStructureInterp's "linear in variance" convention. If
// either side has no points, the function returns 0.
//
// Convention: positive skew means puts are more expensive than calls
// (the typical "fear premium" for downside protection). Most equity-
// and crypto-like markets show positive skew most of the time;
// negative skew flags unusual upside demand.
func Skew25Delta(smile []DeltaIV) float64 {
	var puts, calls []TermPoint
	for _, p := range smile {
		// Use Delta as the "tenor" axis for interpolation — it's just
		// the abscissa we're interpolating on.
		tp := TermPoint{TenorDays: p.Delta, Value: p.IV}
		if p.IsCall {
			calls = append(calls, tp)
		} else {
			puts = append(puts, tp)
		}
	}
	if len(puts) == 0 || len(calls) == 0 {
		return 0
	}
	putIV := TermStructureInterp(puts, 0.25, true)
	callIV := TermStructureInterp(calls, 0.25, true)
	return putIV - callIV
}

// ─── 8. ATM IV from chain ──────────────────────────────────────────────────

// ChainStrike is one row of a chain at one expiry: a strike with its
// call IV, put IV, and the forward / spot reference price the chain
// is centred on. CallIV and PutIV are NaN when that side isn't
// quoted — callers should use math.IsNaN to skip.
type ChainStrike struct {
	Strike  float64
	CallIV  float64
	PutIV   float64
	Forward float64 // forward price the chain is centred on
}

// ATMIVFromChain returns the IV at the strike nearest the forward
// price, averaging call and put IV when both are quoted. Used by the
// vol metrics dashboard to track "what's the headline IV right now"
// without needing a full surface fit. Returns 0 when the chain is
// empty or no forward price was provided.
func ATMIVFromChain(strikes []ChainStrike) float64 {
	if len(strikes) == 0 {
		return 0
	}
	forward := strikes[0].Forward
	if forward <= 0 {
		return 0
	}
	bestIdx := 0
	bestDist := math.Abs(strikes[0].Strike - forward)
	for i, s := range strikes[1:] {
		d := math.Abs(s.Strike - forward)
		if d < bestDist {
			bestDist = d
			bestIdx = i + 1
		}
	}
	atm := strikes[bestIdx]
	switch {
	case !math.IsNaN(atm.CallIV) && !math.IsNaN(atm.PutIV):
		return (atm.CallIV + atm.PutIV) / 2
	case !math.IsNaN(atm.CallIV):
		return atm.CallIV
	case !math.IsNaN(atm.PutIV):
		return atm.PutIV
	}
	return 0
}

// ─── 9. Realized vs implied ────────────────────────────────────────────────

// RealizedVolatility returns annualised RV computed from a series of
// log returns. Returns are expected to be already-log-differenced
// price observations sampled at a regular cadence; periodsPerYear is
// the annualisation factor (252 for daily equity, 365·24 for hourly
// crypto, etc.). NaN-returns are skipped.
//
// Standard sample-stdev formula:
//
//	RV = sqrt(periodsPerYear · Σ(r − r̄)² / (n − 1))
//
// Returns 0 when fewer than 2 valid observations are present.
func RealizedVolatility(returns []float64, periodsPerYear float64) float64 {
	clean := make([]float64, 0, len(returns))
	for _, r := range returns {
		if !math.IsNaN(r) && !math.IsInf(r, 0) {
			clean = append(clean, r)
		}
	}
	if len(clean) < 2 {
		return 0
	}
	mean := 0.0
	for _, r := range clean {
		mean += r
	}
	mean /= float64(len(clean))
	sumSq := 0.0
	for _, r := range clean {
		d := r - mean
		sumSq += d * d
	}
	variance := sumSq / float64(len(clean)-1)
	return math.Sqrt(periodsPerYear * variance)
}

// RealizedVsImplied is the simple ratio IV / RV − 1, expressed as a
// signed percentage (× 100 done by caller for display). Positive
// means IV > RV (options are "rich"); negative means options are
// "cheap" relative to recent realised. Returns 0 when rv == 0 to
// avoid a division-by-zero.
func RealizedVsImplied(impliedVol, realizedVol float64) float64 {
	if realizedVol == 0 {
		return 0
	}
	return impliedVol/realizedVol - 1
}

// ─── 10. Percentile rank ───────────────────────────────────────────────────

// PercentileRank returns the percentile (0..1) that current sits at
// within the trailing distribution. A value at the median returns
// 0.5; the maximum observed returns 1.0; the minimum returns 0.0.
// Empty distributions return 0.5 (no information; centre of the
// scale is the honest default).
//
// The function copies and sorts the input — caller's slice is not
// mutated.
func PercentileRank(distribution []float64, current float64) float64 {
	if len(distribution) == 0 {
		return 0.5
	}
	sorted := make([]float64, 0, len(distribution))
	for _, v := range distribution {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			sorted = append(sorted, v)
		}
	}
	if len(sorted) == 0 {
		return 0.5
	}
	sort.Float64s(sorted)
	below := 0
	for _, v := range sorted {
		if v < current {
			below++
		} else {
			break
		}
	}
	return float64(below) / float64(len(sorted))
}

// ─── 11. Funding 8h-equivalent ─────────────────────────────────────────────

// Funding8hEquivalent normalises a funding rate to an 8-hour cadence
// — the de facto industry standard — so rates from venues with
// different funding intervals are directly comparable on a screener.
//
// rate is the per-interval rate (Binance perps charge every 8h on
// the dot, others vary). intervalHours is the venue's actual
// settlement cadence; pass 1 for hourly funding (Bitfinex), 8 for
// the standard, 4 for some inverse perps, etc.
//
// Conversion is linear because funding payments are additive over
// time at the venue's quoted rate; we don't compound. That's the
// market convention even though it's mathematically loose.
func Funding8hEquivalent(rate float64, intervalHours float64) float64 {
	if intervalHours <= 0 {
		return 0
	}
	return rate * (8.0 / intervalHours)
}

// ─── 12. Best venue by metric ──────────────────────────────────────────────

// BestVenueByMetric returns the venue with the highest (or lowest)
// metric value. Useful for "which venue has the best funding right
// now" / "which venue's basis is widest." Returns an empty venue
// string when the input is empty or every quote has zero weight.
//
// highest == true picks the maximum; highest == false picks the
// minimum. Ties go to the first venue in input order (stable).
func BestVenueByMetric(quotes []VenueQuote, highest bool) (venue string, value float64) {
	first := true
	for _, q := range quotes {
		if q.Weight <= 0 {
			continue
		}
		if first {
			venue, value = q.Venue, q.Value
			first = false
			continue
		}
		if highest {
			if q.Value > value {
				venue, value = q.Venue, q.Value
			}
		} else {
			if q.Value < value {
				venue, value = q.Venue, q.Value
			}
		}
	}
	return venue, value
}
