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

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
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

func TestFlowTapeCapabilitiesEmpty(t *testing.T) {
	p := NewFlowTapePanel(dashboard.Selection{})
	if got := p.Capabilities(); got != (keymap.Capabilities{}) {
		t.Errorf("expected zero Capabilities, got %+v", got)
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
