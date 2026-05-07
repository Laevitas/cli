package panels

// FlowTapePanel state-machine tests. Same lifecycle invariants as
// FlowBookPanel (selection drives channel, stale events dropped,
// snapshot/state cleared on selection change) but applied to the
// trades channel and the ring-of-trades data shape.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

// makeTradeEvent builds a WS event payload for one trade. Uses the
// `coin_amount` field (perps convention) for the unit quantity
// since that's what FlowTapePanel resolves first.
func makeTradeEvent(channel string, price, coinAmt float64, direction string) dashboard.FeedTickMsg {
	payload, _ := json.Marshal(map[string]any{
		"date":        "2026-05-04T14:23:42Z",
		"price":       price,
		"coin_amount": coinAmt,
		"direction":   direction,
	})
	return dashboard.FeedTickMsg{
		Event: wsclient.Event{
			Channel: channel,
			Data:    payload,
		},
	}
}

// makeTradeEventAt is makeTradeEvent with a caller-controlled
// timestamp offset (seconds added to the canonical 14:23:42 base).
// Used by candle-aggregator tests that need two trades in
// different 1m buckets — pass an offset >= 60 to the second call
// and the aggregator emits two candles.
func makeTradeEventAt(channel string, price, coinAmt float64, direction string, offsetSeconds int) dashboard.FeedTickMsg {
	base := time.Date(2026, 5, 4, 14, 23, 42, 0, time.UTC)
	ts := base.Add(time.Duration(offsetSeconds) * time.Second)
	payload, _ := json.Marshal(map[string]any{
		"date":        ts.Format(time.RFC3339),
		"price":       price,
		"coin_amount": coinAmt,
		"direction":   direction,
	})
	return dashboard.FeedTickMsg{
		Event: wsclient.Event{
			Channel: channel,
			Data:    payload,
		},
	}
}

func TestFlowTapeSubscriptionsSyncsSelection(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{})
	got := p.Subscriptions(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	if len(got.Channels) != 1 || got.Channels[0] != "trades.perpetuals.binance.BTCUSDT" {
		t.Fatalf("Subscriptions = %v, want [trades.perpetuals.binance.BTCUSDT]", got.Channels)
	}
	// First matching tick after Subscriptions-driven sync — should land.
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))
	if len(p.ring) != 1 {
		t.Errorf("ring = %d trades, want 1 (selection sync via Subscriptions)", len(p.ring))
	}
}

func TestFlowTapeSelectionChangedClearsRing(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))
	if len(p.ring) != 1 {
		t.Fatalf("setup failed: ring = %d", len(p.ring))
	}
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "deribit", Symbol: "ETH-PERPETUAL"},
	})
	if len(p.ring) != 0 {
		t.Errorf("ring survived selection change: %d trades", len(p.ring))
	}
	if got := p.stats.window(time.Date(2026, 5, 4, 14, 24, 0, 0, time.UTC), 5*60); got.count != 0 {
		t.Errorf("stats survived selection change: %+v", got)
	}
}

func TestFlowTapeStaleEventDropped(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	// Wrong-channel event.
	p.Update(makeTradeEvent("trades.perpetuals.deribit.BTC-PERPETUAL", 78500, 0.5, "buy"))
	if len(p.ring) != 0 {
		t.Errorf("stale event added to ring: %d trades", len(p.ring))
	}
}

func TestFlowLargePrintsFiltersVisibleTradesOnly(t *testing.T) {
	p := NewFlowLargePrintsPanel(dashboard.Selection{
		Market: "spot", Venue: "binance", Symbol: "BTCUSDT",
	}, 10_000)

	p.Update(makeTradeEvent("trades.spot.binance.BTCUSDT", 100, 50, "buy"))  // $5k, hidden by default
	p.Update(makeTradeEvent("trades.spot.binance.BTCUSDT", 100, 150, "buy")) // $15k, visible

	if len(p.ring) != 2 {
		t.Fatalf("ring = %d, want both trades retained", len(p.ring))
	}
	visible := p.visibleTrades()
	if len(visible) != 1 || visible[0].size != 150 {
		t.Fatalf("visible trades = %+v, want only the $15k print", visible)
	}
	stats := p.stats.window(time.Date(2026, 5, 4, 14, 24, 0, 0, time.UTC), 5*60)
	if stats.count != 2 {
		t.Fatalf("stats count = %d, want both trades counted", stats.count)
	}
}

func teaKey(key string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}

func TestFlowTapeFilterCycle(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{})

	p.Update(teaKey("F"))
	if p.minUSD != 1_000 {
		t.Fatalf("first F minUSD = %v, want 1000", p.minUSD)
	}
	p.Update(teaKey("F"))
	if p.minUSD != 10_000 {
		t.Fatalf("second F minUSD = %v, want 10000", p.minUSD)
	}
}

func TestFlowTapeFilterResetsToDefaultOnSelectionChange(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(teaKey("F"))
	if p.minUSD != 1_000 {
		t.Fatalf("setup minUSD = %v, want 1000", p.minUSD)
	}
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "bybit", Symbol: "ETHUSDT"},
	})
	if p.minUSD != 0 {
		t.Fatalf("selection reset minUSD = %v, want default 0", p.minUSD)
	}
}

// TestFlowLargePrintsIgnoresFilterCycle: LARGE PRINTS pane is locked
// at its constructor threshold and `F` is a no-op there. The pane is
// opinionated by design and shouldn't have its threshold adjusted
// from inside the dashboard. Users wanting a different filter shape
// on spot use the regular TAPE pane (starts at "all", cycles via F)
// or the WS NDJSON feed.
func TestFlowLargePrintsIgnoresFilterCycle(t *testing.T) {
	p := NewFlowLargePrintsPanel(dashboard.Selection{
		Market: "spot", Venue: "binance", Symbol: "BTCUSDT",
	}, 100_000)
	if p.minUSD != 100_000 {
		t.Fatalf("constructor minUSD = %v, want 100000", p.minUSD)
	}
	p.Update(teaKey("F"))
	if p.minUSD != 100_000 {
		t.Fatalf("F on locked pane changed minUSD: %v, want 100000", p.minUSD)
	}
	p.Update(teaKey("F"))
	p.Update(teaKey("F"))
	if p.minUSD != 100_000 {
		t.Fatalf("repeated F on locked pane changed minUSD: %v, want 100000", p.minUSD)
	}
}

// TestFlowTapeRingNewestFirst: the ring stores newest-at-front so
// View can iterate naturally and emit oldest at the bottom.
func TestFlowTapeRingNewestFirst(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 100, 1, "buy"))
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 200, 2, "sell"))
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 300, 3, "buy"))

	if len(p.ring) != 3 {
		t.Fatalf("ring = %d, want 3", len(p.ring))
	}
	// Newest (300) at front, oldest (100) at back.
	if p.ring[0].price != 300 {
		t.Errorf("ring[0].price = %v, want 300 (newest)", p.ring[0].price)
	}
	if p.ring[2].price != 100 {
		t.Errorf("ring[2].price = %v, want 100 (oldest)", p.ring[2].price)
	}
}

// TestFlowTapeCapacityEvictsOldest: pushing beyond flowTapeCapacity
// drops the oldest trade.
func TestFlowTapeCapacityEvictsOldest(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	for i := 0; i < flowTapeCapacity+5; i++ {
		p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", float64(i), 1, "buy"))
	}
	if len(p.ring) != flowTapeCapacity {
		t.Errorf("ring = %d, want %d (capacity)", len(p.ring), flowTapeCapacity)
	}
	// Oldest retained should be at index capacity-1; price index
	// 5 is the oldest survivor (5..capacity+4 are the kept ones).
	oldestKept := p.ring[flowTapeCapacity-1].price
	if oldestKept != 5 {
		t.Errorf("oldest kept price = %v, want 5 (capacity eviction)", oldestKept)
	}
}

func TestTapeStatsRingUsesFiveMinuteWallClockWindow(t *testing.T) {
	now := time.Date(2026, 5, 4, 14, 25, 0, 0, time.UTC)
	var stats tapeStatsRing
	stats.add(now.Add(-30*time.Second), 100, "buy")
	stats.add(now.Add(-90*time.Second), 200, "sell")
	stats.add(now.Add(-180*time.Second), 300, "buy")

	five := stats.window(now, 5*60)
	if five.count != 3 || five.buyUSD != 400 || five.sellUSD != 200 {
		t.Fatalf("5m window = %+v, want all three trades", five)
	}
}

func TestTapeStatsRingEvictsOutsideFiveMinutes(t *testing.T) {
	now := time.Date(2026, 5, 4, 14, 25, 0, 0, time.UTC)
	var stats tapeStatsRing
	stats.add(now.Add(-301*time.Second), 1000, "buy")
	stats.add(now.Add(-299*time.Second), 100, "sell")

	five := stats.window(now, 5*60)
	if five.count != 1 || five.buyUSD != 0 || five.sellUSD != 100 {
		t.Fatalf("5m window = %+v, want only t-299s sell $100", five)
	}
}

func TestBuildTapeStatsCompactFiveMinuteNet(t *testing.T) {
	now := time.Date(2026, 5, 4, 14, 25, 0, 0, time.UTC)
	var stats tapeStatsRing
	stats.add(now.Add(-90*time.Second), 420_000, "buy")
	stats.add(now.Add(-30*time.Second), 20_000, "sell")

	lines := buildTapeStats(&stats, now, 24, 0)
	if len(lines) != 1 {
		t.Fatalf("compact stats lines = %d, want 1", len(lines))
	}
	if strings.Contains(lines[0], "1m") || strings.Contains(lines[0], "idle") {
		t.Fatalf("compact stats should only render 5m net:\n%s", lines[0])
	}
	if !strings.Contains(lines[0], "5m") || !strings.Contains(lines[0], "NET") || !strings.Contains(lines[0], "$400K") {
		t.Fatalf("compact stats missing 5m net:\n%s", lines[0])
	}
	if got := output.VisibleWidth(lines[0]); got != 24 {
		t.Fatalf("compact stats width = %d, want 24", got)
	}
}

func TestBuildTapeStatsUsesSideBreakdownWhenWidthAllows(t *testing.T) {
	now := time.Date(2026, 5, 4, 14, 25, 0, 0, time.UTC)
	var stats tapeStatsRing
	stats.add(now.Add(-30*time.Second), 100_000, "buy")
	stats.add(now.Add(-90*time.Second), 25_000, "sell")

	lines := buildTapeStats(&stats, now, 80, 0)
	if len(lines) != 1 {
		t.Fatalf("stats lines = %d, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "5m") || !strings.Contains(lines[0], "BUY") || !strings.Contains(lines[0], "SELL") {
		t.Fatalf("expanded line missing 5m side breakdown:\n%s", lines[0])
	}
}

func TestFlowTapeCapabilitiesAdvertiseFilter(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{})
	if got := p.Capabilities(); !got.TapeFilter {
		t.Errorf("expected TapeFilter capability, got %+v", got)
	}
}

func TestFlowLargePrintsCapabilitiesDoNotAdvertiseFilter(t *testing.T) {
	p := NewFlowLargePrintsPanel(dashboard.Selection{}, 100_000)
	if got := p.Capabilities(); got.TapeFilter {
		t.Errorf("large prints advertised TapeFilter capability: %+v", got)
	}
}

func TestFlowTapeViewWaitingPlaceholder(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	view := p.View(60, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "waiting for trades") && !strings.Contains(view, "waiting") {
		t.Errorf("expected waiting placeholder, got:\n%s", view)
	}
}

func TestFlowTapeViewRendersTrades(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))

	view := p.View(60, 5, dashboard.PanelContext{})
	if !strings.Contains(view, "BUY") {
		t.Errorf("expected BUY label in view:\n%s", view)
	}
	if !strings.Contains(view, "78,500") && !strings.Contains(view, "78500") {
		t.Errorf("expected price 78500 in view:\n%s", view)
	}
}

func TestFlowTapeViewAppliesDisplayFilter(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.minUSD = 10_000
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 111, 45, "buy"))   // <$10k
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 222, 100, "sell")) // >$10k

	view := p.View(80, 6, dashboard.PanelContext{})
	if strings.Contains(view, "111.00") || strings.Contains(view, "$5.0K") {
		t.Fatalf("small trade leaked through filter:\n%s", view)
	}
	if !strings.Contains(view, "222.00") {
		t.Fatalf("large trade missing from filtered view:\n%s", view)
	}
	if !strings.Contains(view, "min") || !strings.Contains(view, "$10K") {
		t.Fatalf("filter label missing from stats:\n%s", view)
	}
}

// TestFlowTapeRejectsPartialPayload: events that decode to zero
// price, zero size, or unrecognised direction must NOT be appended
// to the ring. Without this guard an empty `{}` event would
// render as "00:00:00 SELL 0 0", because direction defaults to ""
// (which is != "buy", so the SELL branch fires).
func TestFlowTapeRejectsPartialPayload(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	channel := "trades.perpetuals.binance.BTCUSDT"

	// Empty payload — every field zero/empty.
	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("empty payload was appended; ring=%d, want 0", len(p.ring))
	}

	// Price zero, size present, direction valid.
	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":0,"coin_amount":1,"direction":"buy"}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("zero-price trade was appended; ring=%d, want 0", len(p.ring))
	}

	// Price present, size zero, direction valid.
	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500,"direction":"buy"}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("zero-size trade was appended; ring=%d, want 0", len(p.ring))
	}

	// Price + size present, direction empty/garbage.
	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500,"coin_amount":1,"direction":""}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("empty-direction trade was appended; ring=%d, want 0", len(p.ring))
	}
	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500,"coin_amount":1,"direction":"hodl"}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("unknown-direction trade was appended; ring=%d, want 0", len(p.ring))
	}

	// Sanity: a fully valid event still lands.
	p.Update(makeTradeEvent(channel, 78500, 0.5, "buy"))
	if len(p.ring) != 1 {
		t.Errorf("valid trade after invalid ones was rejected; ring=%d, want 1", len(p.ring))
	}
}

func TestFlowTapeViewBelowMinRendersCompactTape(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))
	view := p.View(20, 4, dashboard.PanelContext{}) // below 40-wide minimum
	if strings.Contains(view, "too small") {
		t.Errorf("unexpected too-small placeholder at narrow width:\n%s", view)
	}
	if !strings.Contains(view, "78,500") {
		t.Errorf("expected compact trade price, got:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := output.VisibleWidth(line); got != 20 {
			t.Fatalf("line %d width = %d, want 20\n%s", i, got, view)
		}
	}
}
