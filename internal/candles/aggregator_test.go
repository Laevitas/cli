package candles

import (
	"testing"
	"time"
)

// mustTime is a tiny helper to build UTC times for test trades —
// time.Parse with RFC3339 inline is verbose to read at the call site.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestAddSingleTradeCreatesCandle: the basic shape — one trade in,
// one candle out, Open=High=Low=Close=price, Volume=size,
// TradeCount=1, BucketStart at the minute floor.
func TestAddSingleTradeCreatesCandle(t *testing.T) {
	a := New(60)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:42Z"), Price: 78500, Size: 1.5})

	cs := a.Candles()
	if len(cs) != 1 {
		t.Fatalf("got %d candles, want 1", len(cs))
	}
	c := cs[0]
	if !c.BucketStart.Equal(mustTime("2026-05-04T14:23:00Z")) {
		t.Errorf("BucketStart = %v, want 14:23:00", c.BucketStart)
	}
	if c.Open != 78500 || c.High != 78500 || c.Low != 78500 || c.Close != 78500 {
		t.Errorf("OHLC mismatch: %+v", c)
	}
	if c.Volume != 1.5 || c.TradeCount != 1 {
		t.Errorf("Volume/Count mismatch: %+v", c)
	}
}

// TestSameBucketUpdatesInPlace: two trades in the same 1m window
// merge into one candle with correct OHLCV math.
func TestSameBucketUpdatesInPlace(t *testing.T) {
	a := New(60)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:30Z"), Price: 78600, Size: 2})
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:45Z"), Price: 78400, Size: 0.5})

	cs := a.Candles()
	if len(cs) != 1 {
		t.Fatalf("got %d candles, want 1 (all in same minute)", len(cs))
	}
	c := cs[0]
	if c.Open != 78500 {
		t.Errorf("Open = %v, want 78500 (first trade)", c.Open)
	}
	if c.High != 78600 {
		t.Errorf("High = %v, want 78600", c.High)
	}
	if c.Low != 78400 {
		t.Errorf("Low = %v, want 78400", c.Low)
	}
	if c.Close != 78400 {
		t.Errorf("Close = %v, want 78400 (last trade)", c.Close)
	}
	if c.Volume != 3.5 {
		t.Errorf("Volume = %v, want 3.5", c.Volume)
	}
	if c.TradeCount != 3 {
		t.Errorf("TradeCount = %v, want 3", c.TradeCount)
	}
}

// TestNewBucketAppendsCandle: a trade in a new minute appends a
// fresh candle without touching the previous one.
func TestNewBucketAppendsCandle(t *testing.T) {
	a := New(60)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:24:05Z"), Price: 78600, Size: 1})

	cs := a.Candles()
	if len(cs) != 2 {
		t.Fatalf("got %d candles, want 2", len(cs))
	}
	if cs[0].Close != 78500 || cs[1].Open != 78600 {
		t.Errorf("bucket boundary not respected: %+v", cs)
	}
}

// TestLateTradeDropped: a trade older than the head bucket is silently
// dropped (we don't mutate already-exposed candles).
func TestLateTradeDropped(t *testing.T) {
	a := New(60)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:24:10Z"), Price: 78500, Size: 1})
	// This trade is from 14:22 — older than the 14:24 bucket. Drop.
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:22:30Z"), Price: 78400, Size: 5})

	cs := a.Candles()
	if len(cs) != 1 {
		t.Fatalf("got %d candles, want 1 (late trade should be dropped)", len(cs))
	}
	if cs[0].Volume != 1 {
		t.Errorf("Volume = %v, want 1 (late trade should not have been merged)", cs[0].Volume)
	}
}

// TestSameBucketOutOfOrderTrade: an out-of-order trade arriving
// within the same 1m bucket as the head must contribute to volume,
// trade count, high, and low — but must NOT overwrite Close. The
// canonical Close stays at the latest-by-time trade's price.
//
// Codex's round-1 step-1 review flagged this: the gateway protocol
// doesn't contractually guarantee per-channel monotonicity, and the
// silent-wrong-Close failure mode would never surface in unit tests
// without an explicit case for it.
func TestSameBucketOutOfOrderTrade(t *testing.T) {
	a := New(60)
	// Latest trade by time → Close should anchor here.
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:30Z"), Price: 78500, Size: 1})
	// Out-of-order arrival, EARLIER timestamp, HIGHER price (would
	// expand High but should NOT become Close).
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78700, Size: 2})
	// Out-of-order arrival, EARLIER timestamp, LOWER price (would
	// expand Low but should NOT become Close).
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:15Z"), Price: 78300, Size: 0.5})

	cs := a.Candles()
	if len(cs) != 1 {
		t.Fatalf("got %d candles, want 1", len(cs))
	}
	c := cs[0]

	// Open: first trade we received (78500). Order-of-arrival decides
	// Open; it's never overwritten regardless of timestamp.
	if c.Open != 78500 {
		t.Errorf("Open = %v, want 78500 (first trade we received)", c.Open)
	}
	// Close: latest-by-TIMESTAMP trade (78500 at 14:23:30, never
	// displaced because the later arrivals had earlier timestamps).
	if c.Close != 78500 {
		t.Errorf("Close = %v, want 78500 (latest by timestamp); out-of-order trade overwrote it", c.Close)
	}
	// High/low: order-independent, fold all in.
	if c.High != 78700 {
		t.Errorf("High = %v, want 78700", c.High)
	}
	if c.Low != 78300 {
		t.Errorf("Low = %v, want 78300", c.Low)
	}
	if c.Volume != 3.5 {
		t.Errorf("Volume = %v, want 3.5", c.Volume)
	}
	if c.TradeCount != 3 {
		t.Errorf("TradeCount = %v, want 3", c.TradeCount)
	}
}

// TestSameBucketEqualTimestampsTreatedAsLatest: trades with equal
// timestamps to the current latest both update Close (last-write-
// wins for equal-time prints). This covers the realistic case where
// the gateway emits tied timestamps for batched fills.
func TestSameBucketEqualTimestampsTreatedAsLatest(t *testing.T) {
	a := New(60)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:30Z"), Price: 78500, Size: 1})
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:30Z"), Price: 78600, Size: 1})

	cs := a.Candles()
	if cs[0].Close != 78600 {
		t.Errorf("Close = %v, want 78600 (equal-timestamp trade should advance Close)", cs[0].Close)
	}
}

// TestCapacityEvictsOldest: pushing beyond capacity evicts the
// oldest candle. Order of remaining candles is preserved
// (oldest-first).
func TestCapacityEvictsOldest(t *testing.T) {
	a := New(3)
	for i := 0; i < 5; i++ {
		t0 := mustTime("2026-05-04T14:00:00Z").Add(time.Duration(i) * time.Minute)
		a.Add(Trade{Timestamp: t0, Price: float64(78000 + i), Size: 1})
	}

	cs := a.Candles()
	if len(cs) != 3 {
		t.Fatalf("got %d candles, want 3 (capacity)", len(cs))
	}
	// Only the last 3 (i=2,3,4) should remain; the price encoding lets
	// us verify cheaply.
	if cs[0].Open != 78002 || cs[1].Open != 78003 || cs[2].Open != 78004 {
		t.Errorf("eviction order wrong: %+v", cs)
	}
}

// TestCandlesIsDefensiveCopy: mutating the returned slice does not
// affect subsequent reads.
func TestCandlesIsDefensiveCopy(t *testing.T) {
	a := New(10)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})

	cs := a.Candles()
	cs[0].Close = 99999

	cs2 := a.Candles()
	if cs2[0].Close != 78500 {
		t.Fatalf("returned slice was not a defensive copy; got Close=%v", cs2[0].Close)
	}
}

// TestDownsample5m: ten 1m candles spanning 14:00 → 14:09 collapse
// into two 5m candles (14:00–14:04 and 14:05–14:09) with correct
// OHLCV math.
func TestDownsample5m(t *testing.T) {
	a := New(60)
	for i := 0; i < 10; i++ {
		t0 := mustTime("2026-05-04T14:00:00Z").Add(time.Duration(i) * time.Minute)
		a.Add(Trade{Timestamp: t0, Price: float64(78000 + i*10), Size: 1})
	}

	cs := a.Downsample(5 * time.Minute)
	if len(cs) != 2 {
		t.Fatalf("got %d 5m candles, want 2", len(cs))
	}
	// First 5m bucket (14:00 → 14:04): prices 78000, 78010, 78020, 78030, 78040
	first := cs[0]
	if !first.BucketStart.Equal(mustTime("2026-05-04T14:00:00Z")) {
		t.Errorf("first BucketStart = %v, want 14:00", first.BucketStart)
	}
	if first.Open != 78000 || first.Close != 78040 {
		t.Errorf("first OC = %v / %v, want 78000 / 78040", first.Open, first.Close)
	}
	if first.High != 78040 || first.Low != 78000 {
		t.Errorf("first HL = %v / %v, want 78040 / 78000", first.High, first.Low)
	}
	if first.Volume != 5 || first.TradeCount != 5 {
		t.Errorf("first Vol/Count = %v / %v, want 5 / 5", first.Volume, first.TradeCount)
	}
	// Second 5m bucket (14:05 → 14:09): prices 78050, 78060, 78070, 78080, 78090
	second := cs[1]
	if !second.BucketStart.Equal(mustTime("2026-05-04T14:05:00Z")) {
		t.Errorf("second BucketStart = %v, want 14:05", second.BucketStart)
	}
	if second.Open != 78050 || second.Close != 78090 {
		t.Errorf("second OC = %v / %v, want 78050 / 78090", second.Open, second.Close)
	}
}

// TestDownsampleEmpty: downsampling with no candles returns empty.
func TestDownsampleEmpty(t *testing.T) {
	a := New(60)
	if cs := a.Downsample(5 * time.Minute); len(cs) != 0 {
		t.Fatalf("got %d candles from empty aggregator, want 0", len(cs))
	}
}

func TestAddUsesConfiguredTimeframe(t *testing.T) {
	a := New(10)
	a.SetTimeframe(5 * time.Minute)

	base := mustTime("2026-05-04T14:23:42Z")
	a.Add(Trade{Timestamp: base, Price: 100, Size: 1})
	a.Add(Trade{Timestamp: base.Add(time.Minute), Price: 110, Size: 2})
	a.Add(Trade{Timestamp: base.Add(2 * time.Minute), Price: 120, Size: 3})

	cs := a.Candles()
	if len(cs) != 2 {
		t.Fatalf("configured 5m buckets emitted %d candles, want 2: %+v", len(cs), cs)
	}
	if !cs[0].BucketStart.Equal(mustTime("2026-05-04T14:20:00Z")) {
		t.Fatalf("first bucket start = %s, want 14:20", cs[0].BucketStart)
	}
	if cs[0].Open != 100 || cs[0].Close != 110 || cs[0].Volume != 3 {
		t.Fatalf("first 5m bucket = %+v, want merged first two trades", cs[0])
	}
	if !cs[1].BucketStart.Equal(mustTime("2026-05-04T14:25:00Z")) || cs[1].Close != 120 {
		t.Fatalf("second 5m bucket = %+v, want 14:25 close 120", cs[1])
	}
}

// TestDownsampleSubMinuteFloors: timeframes smaller than 1m round
// up to 1m (the canonical resolution); downsample with 1m is the
// identity transform.
func TestDownsampleSubMinuteFloors(t *testing.T) {
	a := New(60)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})

	cs := a.Downsample(30 * time.Second)
	if len(cs) != 1 {
		t.Fatalf("got %d candles, want 1 (sub-minute should round to 1m)", len(cs))
	}
	if cs[0].Volume != 1 {
		t.Errorf("downsample mangled candle: %+v", cs[0])
	}
}

// TestLatest: returns the head candle, with a presence boolean.
func TestLatest(t *testing.T) {
	a := New(10)
	if _, ok := a.Latest(); ok {
		t.Fatal("Latest on empty aggregator should return ok=false")
	}
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:24:05Z"), Price: 78600, Size: 1})
	c, ok := a.Latest()
	if !ok {
		t.Fatal("Latest should return ok=true after Add")
	}
	if c.Close != 78600 {
		t.Errorf("Latest close = %v, want 78600", c.Close)
	}
}

// TestReset: clears the buffer; future Adds start fresh.
func TestReset(t *testing.T) {
	a := New(10)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})
	a.Reset()
	if len(a.Candles()) != 0 {
		t.Fatal("Reset did not clear candles")
	}
}

// TestSeedReplacesAndSortsAndDedupes: Seed sorts unsorted input,
// collapses duplicate-bucket entries (last write wins), respects
// capacity by keeping the newest N.
func TestSeedReplacesAndSortsAndDedupes(t *testing.T) {
	a := New(3)
	// Pre-existing candle that should be wiped by Seed.
	a.Add(Trade{Timestamp: mustTime("2026-05-04T13:00:00Z"), Price: 1, Size: 1})

	seeded := []Candle{
		{BucketStart: mustTime("2026-05-04T14:02:00Z"), Open: 102, Close: 102, High: 102, Low: 102, Volume: 1, TradeCount: 1},
		{BucketStart: mustTime("2026-05-04T14:00:00Z"), Open: 100, Close: 100, High: 100, Low: 100, Volume: 1, TradeCount: 1},
		// Duplicate of 14:02 — last write wins, so this Close=999 should win.
		{BucketStart: mustTime("2026-05-04T14:02:00Z"), Open: 102, Close: 999, High: 999, Low: 102, Volume: 5, TradeCount: 5},
		{BucketStart: mustTime("2026-05-04T14:01:00Z"), Open: 101, Close: 101, High: 101, Low: 101, Volume: 1, TradeCount: 1},
		{BucketStart: mustTime("2026-05-04T14:03:00Z"), Open: 103, Close: 103, High: 103, Low: 103, Volume: 1, TradeCount: 1},
	}
	a.Seed(seeded)

	cs := a.Candles()
	if len(cs) != 3 {
		t.Fatalf("got %d candles, want 3 (capacity)", len(cs))
	}
	// Capacity is 3; with deduped seed of 4 unique buckets (14:00, 14:01,
	// 14:02, 14:03), we keep the newest 3: 14:01, 14:02, 14:03.
	if !cs[0].BucketStart.Equal(mustTime("2026-05-04T14:01:00Z")) {
		t.Errorf("after seed cs[0] = %v, want 14:01", cs[0].BucketStart)
	}
	// The deduped 14:02 should reflect the LAST seed entry (Close=999),
	// not the first.
	if cs[1].Close != 999 {
		t.Errorf("dedup did not pick last write: cs[1].Close = %v, want 999", cs[1].Close)
	}
	if !cs[2].BucketStart.Equal(mustTime("2026-05-04T14:03:00Z")) {
		t.Errorf("after seed cs[2] = %v, want 14:03", cs[2].BucketStart)
	}
}

// TestSeedEmptyClearsState: an empty/nil seed wipes existing candles.
// This matters when a panel re-seeds after SelectionChangedMsg and
// the new instrument has no historical data; the old data must not
// survive.
func TestSeedEmptyClearsState(t *testing.T) {
	a := New(10)
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})
	if len(a.Candles()) != 1 {
		t.Fatalf("setup failed: expected 1 candle")
	}

	a.Seed(nil)
	if got := len(a.Candles()); got != 0 {
		t.Fatalf("Seed(nil) did not clear: got %d candles, want 0", got)
	}

	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:23:10Z"), Price: 78500, Size: 1})
	a.Seed([]Candle{})
	if got := len(a.Candles()); got != 0 {
		t.Fatalf("Seed([]Candle{}) did not clear: got %d candles, want 0", got)
	}
}

// TestSeedDedupeStableLastWins: when multiple seed entries share a
// BucketStart, the LAST entry in input order wins. Earlier dedup
// used sort.Slice which is unstable — could pick an arbitrary
// duplicate.
func TestSeedDedupeStableLastWins(t *testing.T) {
	a := New(10)
	seeded := []Candle{
		{BucketStart: mustTime("2026-05-04T14:00:00Z"), Open: 1, Close: 1, High: 1, Low: 1, Volume: 1, TradeCount: 1},
		// Three duplicates of 14:01 — last (Close=300) must win.
		{BucketStart: mustTime("2026-05-04T14:01:00Z"), Open: 100, Close: 100, High: 100, Low: 100, Volume: 1, TradeCount: 1},
		{BucketStart: mustTime("2026-05-04T14:01:00Z"), Open: 100, Close: 200, High: 200, Low: 100, Volume: 2, TradeCount: 2},
		{BucketStart: mustTime("2026-05-04T14:01:00Z"), Open: 100, Close: 300, High: 300, Low: 100, Volume: 3, TradeCount: 3},
		{BucketStart: mustTime("2026-05-04T14:02:00Z"), Open: 2, Close: 2, High: 2, Low: 2, Volume: 1, TradeCount: 1},
	}
	a.Seed(seeded)

	cs := a.Candles()
	if len(cs) != 3 {
		t.Fatalf("got %d candles, want 3 after dedup", len(cs))
	}
	if cs[1].Close != 300 || cs[1].Volume != 3 {
		t.Errorf("dedup did not pick last entry: got Close=%v Volume=%v, want 300 / 3", cs[1].Close, cs[1].Volume)
	}
}

// TestBucketFloorEpochAlignment: bucket boundaries align to the
// epoch, not to first-trade time. A 5m view always emits buckets
// starting at minutes :00, :05, :10 — never :03, :08, :13.
func TestBucketFloorEpochAlignment(t *testing.T) {
	a := New(60)
	// First trade at 14:03:42 — if we bucketed relative to first trade,
	// the 5m bucket would start at 14:03. We should bucket at 14:00.
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:03:42Z"), Price: 78500, Size: 1})
	a.Add(Trade{Timestamp: mustTime("2026-05-04T14:08:30Z"), Price: 78600, Size: 1})

	cs := a.Downsample(5 * time.Minute)
	if len(cs) != 2 {
		t.Fatalf("got %d 5m candles, want 2", len(cs))
	}
	if !cs[0].BucketStart.Equal(mustTime("2026-05-04T14:00:00Z")) {
		t.Errorf("5m bucket misaligned: cs[0] = %v, want 14:00", cs[0].BucketStart)
	}
	if !cs[1].BucketStart.Equal(mustTime("2026-05-04T14:05:00Z")) {
		t.Errorf("5m bucket misaligned: cs[1] = %v, want 14:05", cs[1].BucketStart)
	}
}
