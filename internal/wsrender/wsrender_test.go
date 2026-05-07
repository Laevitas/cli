package wsrender

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/laevitas/cli/internal/wsclient"
)

func makeWSTrade(channel string, payload map[string]any) wsclient.Event {
	data, _ := json.Marshal(payload)
	return wsclient.Event{Channel: channel, Data: data}
}

func TestFilterTradeTapeEventsUsesSharedMinimumNotional(t *testing.T) {
	events := []wsclient.Event{
		makeWSTrade("trades.perpetuals.binance.BTCUSDT", map[string]any{
			"price": 100.0, "coin_amount": 50.0,
		}),
		makeWSTrade("trades.perpetuals.binance.BTCUSDT", map[string]any{
			"price": 100.0, "coin_amount": 150.0,
		}),
	}

	got := filterTradeTapeEvents(events, 10_000)
	if len(got) != 1 {
		t.Fatalf("filtered events = %d, want 1", len(got))
	}
	if bytes.Equal(got[0].Data, events[0].Data) {
		t.Fatal("small trade was retained")
	}
}

func TestTradeEventNotionalPrefersUSDFields(t *testing.T) {
	ev := makeWSTrade("trades.spot.binance.BTCUSDT", map[string]any{
		"price": 100.0, "amount": 1.0, "quote_amount": 12_345.0,
	})
	if got := tradeEventNotionalUSD(ev); got != 12_345 {
		t.Fatalf("notional = %v, want quote_amount", got)
	}
}

func TestModelTapeFilterOnlyActiveOnTradeStreams(t *testing.T) {
	trades := newModel(NewLiveTable([]string{"trades.perpetuals.binance.BTCUSDT"}))
	if !trades.hasTradeStreams() || trades.footerSurface() != "tape" {
		t.Fatalf("trade model surface = %q, hasTradeStreams=%v", trades.footerSurface(), trades.hasTradeStreams())
	}

	ticker := newModel(NewLiveTable([]string{"ohlc.ticker.perpetuals.binance.BTCUSDT"}))
	if ticker.hasTradeStreams() || ticker.footerSurface() != "stream" {
		t.Fatalf("ticker model surface = %q, hasTradeStreams=%v", ticker.footerSurface(), ticker.hasTradeStreams())
	}
}
