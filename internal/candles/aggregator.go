// Package candles aggregates streaming trade events into OHLCV candles.
//
// The aggregator maintains one active bucket series at a time. The
// default timeframe is 1 minute; callers can switch to coarser
// timeframes before seeding/adding trades when they want native
// 5m/15m/1h candles.
//
// The package is rendering-agnostic and dashboard-agnostic. It owns
// only the data: time buckets, OHLCV math, downsampling. ASCII
// rendering lives in render.go (also pure, no external deps).
// Callers (flow chart panel, future heatmap, future analytics
// commands) supply trades via Add and read candles via Candles.
//
// Concurrency: methods are NOT safe for concurrent use. The expected
// pattern is one writer goroutine (the panel's Update handler
// processing FeedTickMsg) and one reader (the same goroutine's View
// call). Callers needing concurrent access wrap with a mutex.
package candles

import (
	"sort"
	"time"
)

// Trade is one inbound trade event the aggregator consumes. The
// Timestamp must be UTC; the aggregator buckets purely by absolute
// time, no timezone awareness.
type Trade struct {
	Timestamp time.Time
	Price     float64
	Size      float64
}

// Candle is one OHLCV bucket. BucketStart is the inclusive start of
// the bucket window — for a 1m candle bucketing trades at 14:23:42,
// BucketStart is 14:23:00 UTC. End is BucketStart + Timeframe.
//
// Empty buckets (no trades in the window) are NOT represented; the
// Candles slice skips them. Callers that need gap-filled time series
// for chart rendering should walk the slice and emit explicit gaps
// between non-contiguous BucketStart values.
type Candle struct {
	BucketStart time.Time
	Open        float64
	High        float64
	Low         float64
	Close       float64
	Volume      float64
	TradeCount  int
}

// Aggregator buckets streaming trades into candles and supports
// downsampled views. Capacity bounds the internal ring; older
// candles drop off as new ones arrive. The bound keeps memory
// predictable for long-running dashboards.
type Aggregator struct {
	// capacity is the max number of active-timeframe candles retained.
	capacity int

	// timeframe is the active bucket size. It defaults to 1m and is
	// truncated to a whole-minute positive duration.
	timeframe time.Duration

	// candles is the ring of completed-or-in-progress candles,
	// ordered oldest-first. The newest is the in-progress bucket if
	// the most recent Trade landed inside its window.
	candles []*Candle

	// headLastTradeAt tracks the timestamp of the latest-by-time
	// trade folded into the head (in-progress) bucket. Used by Add
	// to detect same-bucket out-of-order trades: an older arrival
	// still contributes volume + count + high/low, but does NOT
	// overwrite Close. Reset whenever a new bucket pushes onto the
	// ring. Only tracked for the head bucket — older buckets are
	// frozen and don't need this.
	//
	// The gateway protocol doesn't contractually guarantee per-
	// channel trade-time monotonicity, so we don't assume it.
	headLastTradeAt time.Time
}

// New returns an aggregator that retains up to `capacity` candles.
// Capacity must be > 0. The default bucket timeframe is 1 minute.
func New(capacity int) *Aggregator {
	if capacity < 1 {
		capacity = 1
	}
	return &Aggregator{capacity: capacity, timeframe: time.Minute}
}

// SetTimeframe changes the active bucket size used by Add. Existing
// candles are left untouched; callers switching instruments or
// resolutions should Reset or Seed immediately after changing it.
func (a *Aggregator) SetTimeframe(tf time.Duration) {
	tf = tf.Truncate(time.Minute)
	if tf <= 0 {
		tf = time.Minute
	}
	a.timeframe = tf
}

func (a *Aggregator) activeTimeframe() time.Duration {
	if a.timeframe <= 0 {
		return time.Minute
	}
	return a.timeframe
}

// Add ingests one trade. Two out-of-order cases are handled:
//
//   - Late trade in an older bucket (timestamp < head bucket start):
//     silently dropped. The only safe alternative is mutating a
//     bucket we may have already exposed via Candles(), and that
//     breaks read stability.
//   - Late trade in the same bucket as head (timestamp ≥ head bucket
//     start but < latest seen trade in head bucket): contributes
//     volume + trade count + high/low expansion, but does NOT
//     overwrite Close. Open is set on bucket creation and never
//     updated regardless. This guards against gateway feed orderings
//     where adjacent trades arrive transposed.
func (a *Aggregator) Add(t Trade) {
	bucketStart := bucketFloor(t.Timestamp, a.activeTimeframe())

	if len(a.candles) > 0 {
		head := a.candles[len(a.candles)-1]
		if bucketStart.Before(head.BucketStart) {
			// Late, in an older bucket. Drop.
			return
		}
		// Same bucket as head — update in place.
		if bucketStart.Equal(head.BucketStart) {
			updateCandle(head, t, &a.headLastTradeAt)
			return
		}
	}

	// New bucket. Push it; evict the oldest if we're at capacity.
	c := &Candle{
		BucketStart: bucketStart,
		Open:        t.Price,
		High:        t.Price,
		Low:         t.Price,
		Close:       t.Price,
		Volume:      t.Size,
		TradeCount:  1,
	}
	a.candles = append(a.candles, c)
	a.headLastTradeAt = t.Timestamp
	if len(a.candles) > a.capacity {
		// Evict from the front. Slice trick: copy the tail into a new
		// slice so the head element can be GC'd. Not the tightest
		// performance shape but capacity is small (≤ a few hundred)
		// and Add is called at most a few times per second per
		// instrument.
		a.candles = append([]*Candle(nil), a.candles[1:]...)
	}
}

// Candles returns the 1m bucket series, oldest first. The returned
// slice is a defensive copy of the headers — callers can read
// freely without affecting the aggregator's internal state. Body
// values inside each Candle are by value (stack-copied) so future
// Add calls won't mutate them.
func (a *Aggregator) Candles() []Candle {
	out := make([]Candle, len(a.candles))
	for i, c := range a.candles {
		out[i] = *c
	}
	return out
}

// Downsample returns the current candle series rolled up into `tf`-sized
// candles. The timeframe must be a positive integer multiple of 1
// minute (5m, 15m, 1h, etc.); other values are rounded down to the
// nearest minute. Bucketing is absolute-clock-aligned (not relative
// to first trade), so a 5m view always emits buckets starting at
// :00, :05, :10, etc.
//
// The downsample is computed on every call — there is no internal
// cache. Cost is O(n) over the current series, n bounded by capacity, so
// even at capacity 1000 the downsample is ~25µs. If profiling later
// shows this dominates render time, add a render-side cache keyed
// on (newest 1m bucket end, timeframe).
func (a *Aggregator) Downsample(tf time.Duration) []Candle {
	tf = tf.Truncate(time.Minute)
	if tf <= 0 {
		tf = time.Minute
	}
	if tf == time.Minute || len(a.candles) == 0 {
		return a.Candles()
	}

	// Group 1m candles into tf-sized buckets keyed by their floor.
	groups := make(map[time.Time][]*Candle)
	keys := []time.Time{}
	for _, c := range a.candles {
		k := bucketFloor(c.BucketStart, tf)
		if _, exists := groups[k]; !exists {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], c)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })

	out := make([]Candle, 0, len(keys))
	for _, k := range keys {
		merged := mergeCandles(groups[k])
		merged.BucketStart = k
		out = append(out, merged)
	}
	return out
}

// Latest returns the in-progress (head) candle and a boolean
// indicating whether one exists. Convenience for callers that only
// need the current bucket — e.g. a stats bar showing latest price
// without pulling the full series.
func (a *Aggregator) Latest() (Candle, bool) {
	if len(a.candles) == 0 {
		return Candle{}, false
	}
	return *a.candles[len(a.candles)-1], true
}

// Reset clears all retained candles and resets the head-bucket
// trade-time tracker. Used by panels that reset their state on
// SelectionChangedMsg — when the user drills into a different
// instrument, the previous instrument's candle history is no longer
// relevant.
func (a *Aggregator) Reset() {
	a.candles = nil
	a.headLastTradeAt = time.Time{}
}

// Seed bulk-loads candles from an external source (typically a REST
// `ohlcvt -n N` response used to warm the chart so the first frame
// isn't empty). Existing candles are replaced, not merged — callers
// pass the full intended state. Candles are sorted by BucketStart
// ascending; duplicate BucketStarts are collapsed (last write wins).
//
// An empty or nil slice clears all retained candles. This matters
// when a panel re-seeds after a SelectionChangedMsg and the new
// instrument has no historical candles available — the old
// instrument's history must not survive. Equivalent to Reset() in
// that case.
//
// Capacity is honoured: only the newest `capacity` candles are kept.
//
// The head-bucket trade-time tracker is reset because seed candles
// don't carry per-trade timestamps; the next live trade after seed
// is treated as the first trade in the head bucket for ordering
// purposes (becomes Close, sets the new tracker baseline). For seed
// data that overlaps the live feed window this means a fresh live
// trade will correctly overwrite the seeded Close.
func (a *Aggregator) Seed(seeded []Candle) {
	a.headLastTradeAt = time.Time{}
	if len(seeded) == 0 {
		a.candles = nil
		return
	}
	// Copy + sort + dedupe. SliceStable so equal-BucketStart entries
	// retain their input order — the dedupe loop below picks the LAST
	// element of each group as the winner, which only matches the
	// caller's intent ("last write wins") when stable order is
	// guaranteed. sort.Slice (pdqsort) is not stable for equal keys.
	cp := make([]Candle, len(seeded))
	copy(cp, seeded)
	sort.SliceStable(cp, func(i, j int) bool {
		return cp[i].BucketStart.Before(cp[j].BucketStart)
	})
	deduped := make([]*Candle, 0, len(cp))
	for i := range cp {
		c := cp[i]
		if len(deduped) > 0 && deduped[len(deduped)-1].BucketStart.Equal(c.BucketStart) {
			deduped[len(deduped)-1] = &c
			continue
		}
		deduped = append(deduped, &c)
	}
	if len(deduped) > a.capacity {
		deduped = deduped[len(deduped)-a.capacity:]
	}
	a.candles = deduped
}

// ─── helpers ────────────────────────────────────────────────────────────────

// bucketFloor returns the start of the bucket containing t, rounded
// to a multiple of d from the unix epoch. Using epoch-relative
// rounding keeps buckets stable across calls — the 5m bucket
// containing 14:23 is always 14:20, regardless of when the
// aggregator was created.
func bucketFloor(t time.Time, d time.Duration) time.Time {
	return t.UTC().Truncate(d)
}

// updateCandle folds a trade into an existing bucket. Latest-by-time
// trade becomes Close; high/low expand; volume + count accumulate
// regardless of trade ordering. Open is set when the bucket is
// created and never updated.
//
// lastTradeAt is the caller's tracker for the latest-by-time trade
// timestamp seen in this bucket. Updated by reference: a same-bucket
// out-of-order arrival (older than lastTradeAt) does NOT overwrite
// Close, and lastTradeAt itself doesn't move backwards.
func updateCandle(c *Candle, t Trade, lastTradeAt *time.Time) {
	if !t.Timestamp.Before(*lastTradeAt) {
		// In-order or simultaneous: trade is the new latest, so it
		// becomes Close.
		c.Close = t.Price
		*lastTradeAt = t.Timestamp
	}
	// High/low/volume/count are order-independent — fold them in
	// regardless of whether the trade was the latest or an
	// out-of-order arrival.
	if t.Price > c.High {
		c.High = t.Price
	}
	if t.Price < c.Low {
		c.Low = t.Price
	}
	c.Volume += t.Size
	c.TradeCount++
}

// mergeCandles collapses N 1m candles into one. Caller has already
// confirmed all candles share a downsample-bucket key; we just take
// open from the earliest, close from the latest, max high, min low,
// summed volume + count. The returned BucketStart is the earliest
// 1m candle's start — caller overrides with the downsample bucket
// floor.
func mergeCandles(group []*Candle) Candle {
	if len(group) == 0 {
		return Candle{}
	}
	// Group came from a map, ordering is non-deterministic. Sort by
	// BucketStart so Open / Close come from the right candles.
	sort.Slice(group, func(i, j int) bool {
		return group[i].BucketStart.Before(group[j].BucketStart)
	})
	out := *group[0] // copy
	for i := 1; i < len(group); i++ {
		c := group[i]
		out.Close = c.Close
		if c.High > out.High {
			out.High = c.High
		}
		if c.Low < out.Low {
			out.Low = c.Low
		}
		out.Volume += c.Volume
		out.TradeCount += c.TradeCount
	}
	return out
}
