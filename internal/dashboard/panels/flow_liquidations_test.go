package panels

// FlowLiquidationsPanel state-machine tests. Same lifecycle
// invariants as the other flow detail panels; row format and event
// payload are panel-specific.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

// makeLiqEvent builds a WS liquidation event with the canonical
// field set most venues use (amount_usd + side + price + date).
func makeLiqEvent(channel string, price, usd float64, side string) dashboard.FeedTickMsg {
	payload, _ := json.Marshal(map[string]any{
		"date":       "2026-05-04T14:23:42Z",
		"price":      price,
		"amount_usd": usd,
		"side":       side,
	})
	return dashboard.FeedTickMsg{
		Event: wsclient.Event{
			Channel: channel,
			Data:    payload,
		},
	}
}

func TestFlowLiqSubscriptionsSyncsSelection(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{})
	got := p.Subscriptions(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	want := "liquidations.perpetuals.binance.BTCUSDT"
	if len(got.Channels) != 1 || got.Channels[0] != want {
		t.Fatalf("Subscriptions = %v, want [%s]", got.Channels, want)
	}
	p.Update(makeLiqEvent(want, 78500, 1_500_000, "long"))
	if len(p.ring) != 1 {
		t.Errorf("ring = %d, want 1 (selection sync via Subscriptions)", len(p.ring))
	}
}

func TestFlowLiqSelectionChangedClearsRing(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeLiqEvent("liquidations.perpetuals.binance.BTCUSDT", 78500, 1_500_000, "long"))
	if len(p.ring) != 1 {
		t.Fatalf("setup failed: ring = %d", len(p.ring))
	}
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "deribit", Symbol: "ETH-PERPETUAL"},
	})
	if len(p.ring) != 0 {
		t.Errorf("ring survived selection change: %d", len(p.ring))
	}
}

func TestFlowLiqStaleEventDropped(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeLiqEvent("liquidations.perpetuals.deribit.BTC-PERPETUAL", 78500, 1_500_000, "long"))
	if len(p.ring) != 0 {
		t.Errorf("stale event added to ring: %d", len(p.ring))
	}
}

func TestFlowLiqCapabilitiesEmpty(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{})
	if got := p.Capabilities(); got != (keymap.Capabilities{}) {
		t.Errorf("expected zero Capabilities, got %+v", got)
	}
}

func TestFlowLiqViewWaitingPlaceholder(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	view := p.View(60, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "waiting") {
		t.Errorf("expected waiting placeholder, got:\n%s", view)
	}
}

func TestFlowLiqViewRendersLiquidations(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeLiqEvent("liquidations.perpetuals.binance.BTCUSDT", 78500, 1_500_000, "long"))

	view := p.View(60, 5, dashboard.PanelContext{})
	if !strings.Contains(view, "LONG") {
		t.Errorf("expected LONG label in view:\n%s", view)
	}
	if !strings.Contains(view, "$1.5M") {
		t.Errorf("expected $1.5M notional in view:\n%s", view)
	}
}

func TestFlowLiqViewBelowMinRendersCompactLiquidations(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeLiqEvent("liquidations.perpetuals.binance.BTCUSDT", 78500, 1_500_000, "long"))

	view := p.View(20, 3, dashboard.PanelContext{})
	if strings.Contains(view, "too small") {
		t.Errorf("unexpected too-small placeholder:\n%s", view)
	}
	if !strings.Contains(view, "LONG") || !strings.Contains(view, "$1.5M") {
		t.Errorf("expected compact liquidation content, got:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if got := output.VisibleWidth(line); got != 20 {
			t.Fatalf("line %d width = %d, want 20\n%s", i, got, view)
		}
	}
}

// TestFlowLiqGatedToDerivatives: only perpetuals and futures
// publish liquidations on the gateway. Other markets must return
// empty subscriptions even with a fully-populated selection — the
// safety net behind FlowPanel's panel-wiring decision.
func TestFlowLiqGatedToDerivatives(t *testing.T) {
	cases := []struct {
		market string
		want   bool // true = should subscribe
	}{
		{"perpetuals", true},
		{"futures", true},
		{"options", false},
		{"spot", false},
		{"predictions", false},
	}
	for _, c := range cases {
		p := NewFlowLiquidationsPanel(dashboard.Selection{})
		got := p.Subscriptions(dashboard.Selection{
			Market: c.market, Venue: "binance", Symbol: "BTCUSDT",
		})
		gotChannels := len(got.Channels) > 0
		if gotChannels != c.want {
			t.Errorf("market=%q: subscribed=%v, want=%v (channels=%v)",
				c.market, gotChannels, c.want, got.Channels)
		}
	}
}

// TestFlowLiqRejectsPartialPayload: events with zero price, zero
// notional, or unrecognised side must NOT be appended. An empty
// "{}" payload would otherwise render as SHORT $0 — pretending a
// liquidation occurred when none did.
func TestFlowLiqRejectsPartialPayload(t *testing.T) {
	p := NewFlowLiquidationsPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	channel := "liquidations.perpetuals.binance.BTCUSDT"

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("empty payload was appended; ring=%d, want 0", len(p.ring))
	}

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":0,"amount_usd":1500000,"side":"long"}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("zero-price liq was appended; ring=%d, want 0", len(p.ring))
	}

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500,"side":"long"}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("zero-notional liq was appended; ring=%d, want 0", len(p.ring))
	}

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500,"amount_usd":1500000}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("missing-side liq was appended; ring=%d, want 0", len(p.ring))
	}

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500,"amount_usd":1500000,"side":"sideways"}`),
	}})
	if len(p.ring) != 0 {
		t.Errorf("unknown-side liq was appended; ring=%d, want 0", len(p.ring))
	}

	// Sanity: a full event still lands.
	p.Update(makeLiqEvent(channel, 78500, 1_500_000, "long"))
	if len(p.ring) != 1 {
		t.Errorf("valid liq after invalid ones was rejected; ring=%d, want 1", len(p.ring))
	}
}

// TestFormatUSDBuckets locks the formatUSD output shape across
// magnitude buckets so width calculations stay stable.
func TestFormatUSDBuckets(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, ""},
		{500, "$500"},
		{1_500, "$1.5K"},
		{12_345, "$12K"},
		{500_000, "$500K"},
		{1_500_000, "$1.5M"},
		{42_000_000, "$42.0M"},
	}
	for _, c := range cases {
		got := formatUSD(c.in)
		if got != c.want {
			t.Errorf("formatUSD(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
