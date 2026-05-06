package output

import (
	"strings"
	"testing"
)

func TestEmptyHint(t *testing.T) {
	tests := []struct {
		name string
		ctx  EmptyContext
		want string
	}{
		{
			name: "deribit linear perp naming mismatch",
			ctx:  EmptyContext{Exchange: "deribit", Instrument: "BTCUSDT"},
			want: "Hint: BTCUSDT looks like a Binance/Bybit-style linear perp symbol; Deribit names its BTC perp BTC-PERPETUAL. Try BTC-PERPETUAL on deribit, or BTCUSDT --exchange binance.",
		},
		{
			name: "non deribit perpetual naming mismatch",
			ctx:  EmptyContext{Exchange: "binance", Instrument: "BTC-PERPETUAL"},
			want: "Hint: BTC-PERPETUAL is Deribit-style perp naming. Try BTC-PERPETUAL --exchange deribit.",
		},
		{
			name: "non deribit option naming mismatch",
			ctx:  EmptyContext{Exchange: "binance", Instrument: "BTC-27JUN25-100000-C"},
			want: "Hint: BTC-27JUN25-100000-C looks like a Deribit option name. Try BTC-27JUN25-100000-C --exchange deribit.",
		},
		{
			name: "no hint",
			ctx:  EmptyContext{Exchange: "binance", Instrument: "BTCUSDT"},
			want: "",
		},
		{
			name: "liquidations endpoint coverage hint",
			ctx:  EmptyContext{Endpoint: "/api/v1/perpetuals/liquidations", Exchange: "deribit", Instrument: "BTC-PERPETUAL"},
			want: "Hint: deribit may not publish liquidation events to this gateway. Try --exchange binance or --exchange bybit for known coverage.",
		},
		{
			name: "structural symbol hint wins over liquidations coverage hint",
			ctx:  EmptyContext{Endpoint: "/api/v1/perpetuals/liquidations", Exchange: "deribit", Instrument: "BTCUSDT"},
			want: "Hint: BTCUSDT looks like a Binance/Bybit-style linear perp symbol; Deribit names its BTC perp BTC-PERPETUAL. Try BTC-PERPETUAL on deribit, or BTCUSDT --exchange binance.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EmptyHint(tt.ctx); got != tt.want {
				t.Fatalf("EmptyHint() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderEmptyContext(t *testing.T) {
	got := RenderEmptyContext(EmptyContext{
		Endpoint:   "/api/v1/perpetuals/ohlcvt",
		Instrument: "BTCUSDT",
		Exchange:   "deribit",
		Start:      "2026-04-28T00:00:00Z",
		End:        "2026-05-05T00:00:00Z",
		Resolution: "1m",
	})

	for _, want := range []string{
		"No data for /api/v1/perpetuals/ohlcvt · BTCUSDT on deribit · 2026-04-28T00:00:00Z → 2026-05-05T00:00:00Z (7d) · 1m",
		"Hint: BTCUSDT looks like a Binance/Bybit-style linear perp symbol",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderEmptyContext() = %q, missing %q", got, want)
		}
	}
}
