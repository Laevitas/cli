package panels

// FlowChartPanel state-machine tests. The chart's data path goes
// through the candles.Aggregator (which has its own thorough tests
// in internal/candles); here we verify the panel-level lifecycle
// (selection sync, stale event filter, reset on change) and the
// fact that View renders something candle-shaped.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/candles"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

func TestFlowChartSubscriptionsSyncsSelection(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{})
	got := p.Subscriptions(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	want := "trades.perpetuals.binance.BTCUSDT"
	if len(got.Channels) != 1 || got.Channels[0] != want {
		t.Fatalf("Subscriptions = %v, want [%s]", got.Channels, want)
	}
	// First matching trade lands in the aggregator without a
	// SelectionChangedMsg.
	p.Update(makeTradeEvent(want, 78500, 0.5, "buy"))
	if c, ok := p.aggregator.Latest(); !ok || c.Close != 78500 {
		t.Errorf("aggregator did not receive trade after Subscriptions sync: latest=%+v ok=%v", c, ok)
	}
}

func TestFlowChartSelectionChangedResetsAggregator(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))
	if _, ok := p.aggregator.Latest(); !ok {
		t.Fatal("setup failed: expected candle in aggregator")
	}

	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "deribit", Symbol: "ETH-PERPETUAL"},
	})
	if _, ok := p.aggregator.Latest(); ok {
		t.Errorf("aggregator survived selection change")
	}
}

func TestFlowChartSeedMsgInstallsCandles(t *testing.T) {
	sel := dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"}
	p := NewFlowChartPanel(sel)
	key := flowChartSeedKey(sel, "1m")

	p.Update(flowChartSeedMsg{
		key: key,
		candles: []candles.Candle{
			{
				BucketStart: mustPanelTime("2026-05-04T13:00:00Z"),
				Open:        100,
				High:        110,
				Low:         90,
				Close:       105,
				Volume:      12.5,
				TradeCount:  3,
			},
		},
	})

	c, ok := p.aggregator.Latest()
	if !ok {
		t.Fatal("seed candles did not reach aggregator")
	}
	if c.Close != 105 || c.Volume != 12.5 || c.TradeCount != 3 {
		t.Fatalf("latest seeded candle = %+v, want close=105 volume=12.5 trades=3", c)
	}
}

func TestFlowChartStaleSeedMsgDropped(t *testing.T) {
	sel := dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"}
	p := NewFlowChartPanel(sel)

	p.Update(flowChartSeedMsg{
		key: "perpetuals|deribit|ETH-PERPETUAL",
		candles: []candles.Candle{
			{BucketStart: mustPanelTime("2026-05-04T13:00:00Z"), Open: 100, High: 110, Low: 90, Close: 105},
		},
	})

	if _, ok := p.aggregator.Latest(); ok {
		t.Fatal("stale seed candles reached aggregator")
	}
}

func TestParseFlowChartOHLCVTWrappedResponse(t *testing.T) {
	body := []byte(`{
		"data": [
			{"date":"2026-05-04T13:01:00Z","open":"101","high":"111","low":"99","close":"109","volume":"7.5","trades":4},
			{"date":"2026-05-04T13:00:00Z","open":100,"high":110,"low":90,"close":105,"volume":12.5,"trades":3},
			{"date":0,"open":1,"high":1,"low":1,"close":1}
		]
	}`)

	got, err := parseFlowChartOHLCVT(body, time.Minute)
	if err != nil {
		t.Fatalf("parseFlowChartOHLCVT returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d candles, want 2: %+v", len(got), got)
	}
	if !got[0].BucketStart.Equal(mustPanelTime("2026-05-04T13:00:00Z")) || got[0].Close != 105 {
		t.Fatalf("first candle = %+v, want sorted 13:00 close 105", got[0])
	}
	if !got[1].BucketStart.Equal(mustPanelTime("2026-05-04T13:01:00Z")) || got[1].Close != 109 || got[1].Volume != 7.5 || got[1].TradeCount != 4 {
		t.Fatalf("second candle = %+v, want parsed string fields", got[1])
	}
}

func TestFlowChartStaleEventDropped(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	// Wrong-channel event.
	p.Update(makeTradeEvent("trades.perpetuals.deribit.BTC-PERPETUAL", 78500, 0.5, "buy"))
	if _, ok := p.aggregator.Latest(); ok {
		t.Errorf("stale event reached aggregator")
	}
}

// TestFlowChartRejectsPartialPayload: zero-price trades must NOT
// reach the aggregator — they'd corrupt the candle's High/Low
// range and flatten the price scale.
func TestFlowChartRejectsPartialPayload(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	channel := "trades.perpetuals.binance.BTCUSDT"

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{}`),
	}})
	if _, ok := p.aggregator.Latest(); ok {
		t.Errorf("empty payload reached aggregator")
	}

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":0,"coin_amount":1}`),
	}})
	if _, ok := p.aggregator.Latest(); ok {
		t.Errorf("zero-price trade reached aggregator")
	}

	p.Update(dashboard.FeedTickMsg{Event: wsclient.Event{
		Channel: channel,
		Data:    json.RawMessage(`{"price":78500}`),
	}})
	if _, ok := p.aggregator.Latest(); ok {
		t.Errorf("zero-size trade reached aggregator")
	}

	// Valid trade lands.
	p.Update(makeTradeEvent(channel, 78500, 0.5, "buy"))
	if c, ok := p.aggregator.Latest(); !ok || c.Close != 78500 {
		t.Errorf("valid trade after invalid ones was rejected: latest=%+v ok=%v", c, ok)
	}
}

// TestFlowChartViewTinySafe: flow_chart.View skips width/height
// bounds (it delegates to candles.Render after candles exist), so
// the pre-candle waitingView path must be safe for tiny panes.
// width=0 / height=0 / width=1 / negative inputs must not panic
// or produce rows wider than width.
func TestFlowChartViewTinySafe(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	cases := []struct{ w, h int }{
		{0, 0}, {0, 5}, {5, 0}, {1, 1}, {3, 3}, {-1, -1},
	}
	for _, c := range cases {
		// Must not panic. Each row's visible width must fit.
		view := p.View(c.w, c.h, dashboard.PanelContext{})
		if c.w <= 0 || c.h <= 0 {
			if view != "" {
				t.Errorf("view(%d,%d) = %q, want empty for non-positive dims", c.w, c.h, view)
			}
			continue
		}
		// Every row must be ≤ width visible cells (could be < if label
		// got truncated — that's fine).
		for _, row := range strings.Split(view, "\n") {
			if got := len([]rune(row)); got > c.w {
				t.Errorf("view(%d,%d) row width = %d, exceeds requested width", c.w, c.h, got)
			}
		}
	}
}

func TestFlowChartCapabilitiesAdvertiseTimeframe(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{})
	if got := p.Capabilities(); !got.ChartTimeframe {
		t.Errorf("expected ChartTimeframe capability, got %+v", got)
	}
}

func TestFlowChartTimeframeCycleUsesNativeBuckets(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	channel := "trades.perpetuals.binance.BTCUSDT"
	p.Update(makeTradeEventAt(channel, 100, 1.0, "buy", 0))
	p.Update(makeTradeEventAt(channel, 110, 2.0, "buy", 60))

	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if p.chartTimeframe() != 5*time.Minute {
		t.Fatalf("timeframe after one cycle = %s, want 5m", p.chartTimeframe())
	}
	if len(p.chartCandles()) != 0 {
		t.Fatalf("timeframe cycle should clear old-resolution candles: %+v", p.chartCandles())
	}
	if p.seedKey != flowChartSeedKey(p.selection, "5m") {
		t.Fatalf("seed key = %q, want 5m resolution key", p.seedKey)
	}

	p.Update(flowChartSeedMsg{
		key: p.seedKey,
		candles: []candles.Candle{
			{BucketStart: mustPanelTime("2026-05-04T14:20:00Z"), Open: 100, High: 110, Low: 90, Close: 105, Volume: 1},
		},
	})
	p.Update(makeTradeEventAt(channel, 111, 2.0, "buy", 60))
	cs := p.chartCandles()
	if len(cs) != 1 {
		t.Fatalf("live trade in same 5m bucket created %d candles, want 1: %+v", len(cs), cs)
	}
	if cs[0].Open != 100 || cs[0].Close != 111 || cs[0].Volume != 3 {
		t.Fatalf("5m candle after live fold = %+v, want seeded candle updated by live trade", cs[0])
	}

	view := p.View(80, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "5m") {
		t.Fatalf("view missing 5m stats label:\n%s", view)
	}
}

// TestFlowChartViewWaitingPlaceholder: with no trades and no
// selection, the panel renders a centred-empty placeholder via
// waitingView. With a selection but no trades, the new
// "warming up" status copy fires — both states are valid
// pre-data placeholders that this test accepts.
func TestFlowChartViewWaitingPlaceholder(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	view := p.View(60, 12, dashboard.PanelContext{})
	// Either the legacy "waiting" copy or the post-#5 "warming up"
	// copy is acceptable — both signal "no chart yet" to the user.
	if !strings.Contains(view, "waiting") && !strings.Contains(view, "warming up") {
		t.Errorf("expected waiting/warming-up placeholder before any trades, got:\n%s", view)
	}
}

// TestFlowChartViewRendersAfterTrade: at least one candle in the
// aggregator → View no longer shows the waiting placeholder.
// candles.Render's own tests cover the chart shape; here we only
// verify the panel-side glue.
//
// Post-v0.10.0 the chart renders from the FIRST trade — the
// stretch path in candleColumns spreads a single candle across
// the full pane width. No "need 2+ candles" gate.
func TestFlowChartViewRendersAfterTrade(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	// First trade — fills the first 1m bucket.
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))
	// Second trade with a timestamp in a different 1m bucket so
	// the aggregator emits a second candle.
	p.Update(makeTradeEventAt("trades.perpetuals.binance.BTCUSDT", 78600, 0.5, "buy", 90))

	view := p.View(60, 12, dashboard.PanelContext{})
	if strings.Contains(view, "waiting") {
		t.Errorf("expected chart, still got waiting placeholder:\n%s", view)
	}
	// candles.Render emits the price label '78500' (formatted) on
	// the right edge — quick sanity check that rendering is alive.
	if !strings.Contains(view, "78500") && !strings.Contains(view, "78") {
		t.Errorf("expected price label hint in view:\n%s", view)
	}
}

func TestFlowChartViewColorsCandlesAndRendersVolume(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	channel := "trades.perpetuals.binance.BTCUSDT"
	p.Update(makeTradeEventAt(channel, 100, 1.0, "buy", 0))
	p.Update(makeTradeEventAt(channel, 110, 2.0, "buy", 15))
	p.Update(makeTradeEventAt(channel, 108, 3.0, "sell", 60))
	p.Update(makeTradeEventAt(channel, 101, 4.0, "sell", 75))

	view := p.View(60, 12, dashboard.PanelContext{})
	if !strings.Contains(view, output.BrandGreen) {
		t.Errorf("expected bullish candle/volume color, got:\n%s", view)
	}
	if !strings.Contains(view, output.Red) {
		t.Errorf("expected bearish candle/volume color, got:\n%s", view)
	}
	if !strings.Contains(view, output.FormatBookSize(7.0)) {
		t.Errorf("expected latest volume flag, got:\n%s", view)
	}
	if !strings.Contains(view, output.FormatBookPrice(101)) {
		t.Errorf("expected latest price flag, got:\n%s", view)
	}
	for _, row := range strings.Split(view, "\n") {
		if got := output.VisibleWidth(row); got > 60 {
			t.Errorf("row visible width = %d, exceeds 60: %q", got, row)
		}
	}
}

func TestFlowChartTimeAxisFollowsRightAlignedCandles(t *testing.T) {
	cs := []candles.Candle{
		{BucketStart: mustPanelTime("2026-05-04T13:02:00Z"), Open: 100, High: 101, Low: 99, Close: 100, Volume: 1},
		{BucketStart: mustPanelTime("2026-05-04T13:03:00Z"), Open: 100, High: 102, Low: 99, Close: 101, Volume: 1},
		{BucketStart: mustPanelTime("2026-05-04T13:04:00Z"), Open: 101, High: 103, Low: 100, Close: 102, Volume: 1},
		{BucketStart: mustPanelTime("2026-05-04T13:05:00Z"), Open: 102, High: 104, Low: 101, Close: 103, Volume: 1},
		{BucketStart: mustPanelTime("2026-05-04T13:06:00Z"), Open: 103, High: 105, Low: 102, Close: 104, Volume: 1},
		{BucketStart: mustPanelTime("2026-05-04T13:07:00Z"), Open: 104, High: 106, Low: 103, Close: 105, Volume: 1},
	}
	axis := buildTimeAxis(cs, 60, flowChartDefaultTimeframe)
	if strings.HasPrefix(axis, "13:02") {
		t.Fatalf("axis left-anchored sparse candles instead of right-aligning with chart slots: %q", axis)
	}
	if !strings.Contains(axis, "13:07") {
		t.Fatalf("axis missing latest candle label: %q", axis)
	}
	if got := output.VisibleWidth(axis); got != 60 {
		t.Fatalf("axis width = %d, want 60: %q", got, axis)
	}
}

func TestFlowChartTimeAxisDensityPolicy(t *testing.T) {
	base := mustPanelTime("2026-05-04T13:00:00Z")
	makeCandles := func(n int) []candles.Candle {
		cs := make([]candles.Candle, 0, n)
		for i := 0; i < n; i++ {
			cs = append(cs, candles.Candle{
				BucketStart: base.Add(time.Duration(i) * time.Minute),
				Open:        100,
				High:        101,
				Low:         99,
				Close:       100,
				Volume:      1,
			})
		}
		return cs
	}

	axis2 := buildTimeAxis(makeCandles(2), 80, flowChartDefaultTimeframe)
	if strings.Contains(axis2, "13:00") {
		t.Fatalf("2-candle axis should show latest only, got %q", axis2)
	}
	if !strings.Contains(axis2, "13:01") {
		t.Fatalf("2-candle axis missing latest label: %q", axis2)
	}

	axis5 := buildTimeAxis(makeCandles(5), 80, flowChartDefaultTimeframe)
	if !strings.Contains(axis5, "13:00") || !strings.Contains(axis5, "13:04") {
		t.Fatalf("5-candle axis should show first/latest, got %q", axis5)
	}
	if strings.Contains(axis5, "13:02") {
		t.Fatalf("5-candle axis should not show middle label, got %q", axis5)
	}

	axis10 := buildTimeAxis(makeCandles(10), 80, flowChartDefaultTimeframe)
	if !strings.Contains(axis10, "13:00") || !strings.Contains(axis10, "13:05") || !strings.Contains(axis10, "13:09") {
		t.Fatalf("10-candle axis should show first/middle/latest, got %q", axis10)
	}
}

func TestFlowChartViewNarrowRendersCompactStats(t *testing.T) {
	p := NewFlowChartPanel(dashboard.Selection{
		Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT",
	})
	p.Update(makeTradeEvent("trades.perpetuals.binance.BTCUSDT", 78500, 0.5, "buy"))
	p.Update(makeTradeEventAt("trades.perpetuals.binance.BTCUSDT", 78600, 0.5, "buy", 90))

	view := p.View(18, 2, dashboard.PanelContext{})
	if strings.Contains(view, "waiting") || strings.Contains(view, "too small") {
		t.Errorf("expected compact chart stats, got:\n%s", view)
	}
	if !strings.Contains(view, "C") || !strings.Contains(view, "78") {
		t.Errorf("expected close-price compact stats, got:\n%s", view)
	}
}

func mustPanelTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
