package api

import (
	"net/url"
	"testing"
)

func TestNormalizeInstrument(t *testing.T) {
	tests := []struct {
		name   string
		market string
		raw    string
		want   string
	}{
		{name: "perps uppercase", market: "perpetuals", raw: "btcusdt", want: "BTCUSDT"},
		{name: "futures uppercase", market: "futures", raw: "btc-26jun26", want: "BTC-26JUN26"},
		{name: "options uppercase", market: "options", raw: "btc-26jun26-100000-c", want: "BTC-26JUN26-100000-C"},
		{name: "spot uppercase", market: "spot", raw: "ethusdc", want: "ETHUSDC"},
		{name: "namespaced crypto preserves lowercase prefix", market: "perpetuals", raw: "xyz:sndk-usd", want: "xyz:SNDK-USD"},
		{name: "namespaced crypto repairs uppercase prefix", market: "perpetuals", raw: "XYZ:SNDK-USD", want: "xyz:SNDK-USD"},
		{name: "predictions pass through", market: "predictions", raw: "some-CaseSensitive-Slug", want: "some-CaseSensitive-Slug"},
		{name: "unknown pass through trimmed", market: "", raw: "  some-CaseSensitive-Slug  ", want: "some-CaseSensitive-Slug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeInstrument(tt.market, tt.raw); got != tt.want {
				t.Fatalf("NormalizeInstrument(%q, %q) = %q, want %q", tt.market, tt.raw, got, tt.want)
			}
		})
	}
}

func TestBuildURLNormalizesInstrumentForCryptoEndpoints(t *testing.T) {
	c := &Client{baseURL: "https://example.test"}
	rawURL := c.buildURL(PerpsOHLCVT, &RequestParams{
		InstrumentName: "btcusdt",
		Exchange:       "binance",
	})
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if got := u.Query().Get("instrument_name"); got != "BTCUSDT" {
		t.Fatalf("instrument_name = %q, want BTCUSDT", got)
	}
}

func TestBuildURLPreservesPredictionInstrumentCase(t *testing.T) {
	c := &Client{baseURL: "https://example.test"}
	want := "some-CaseSensitive-Slug"
	rawURL := c.buildURL(PredictionsOHLCVT, &RequestParams{
		InstrumentName: want,
	})
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if got := u.Query().Get("instrument_name"); got != want {
		t.Fatalf("instrument_name = %q, want %q", got, want)
	}
}

func TestBuildURLPreservesHyperliquidNamespaceCase(t *testing.T) {
	c := &Client{baseURL: "https://example.test"}
	rawURL := c.buildURL(PerpsOHLCVT, &RequestParams{
		InstrumentName: "xyz:SNDK-USD",
		Exchange:       "hyperliquid",
	})
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if got := u.Query().Get("instrument_name"); got != "xyz:SNDK-USD" {
		t.Fatalf("instrument_name = %q, want xyz:SNDK-USD", got)
	}
}
