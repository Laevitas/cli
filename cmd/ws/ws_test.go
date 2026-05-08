package ws

import (
	"strings"
	"testing"
)

func TestWSArgHintStreamColonPairForgotMarket(t *testing.T) {
	got := wsArgHint([]string{"trades", "binance:BTCUSDT"})
	if !strings.Contains(got, "forgot the market") {
		t.Fatalf("hint missing forgotten-market diagnosis:\n%s", got)
	}
	if !strings.Contains(got, "laevitas ws perpetuals trades binance:BTCUSDT") {
		t.Fatalf("hint missing canonical try command:\n%s", got)
	}
}

func TestWSArgHintStreamExchangeBareInstrument(t *testing.T) {
	got := wsArgHint([]string{"trades", "binance", "BTCUSDT"})
	if !strings.Contains(got, "forgot the market") || !strings.Contains(got, "joined with `:`") {
		t.Fatalf("hint missing market/colon diagnosis:\n%s", got)
	}
	if !strings.Contains(got, "laevitas ws perpetuals trades binance:BTCUSDT") {
		t.Fatalf("hint missing canonical try command:\n%s", got)
	}
}

func TestWSArgHintStreamOnlyForgotMarketAndTarget(t *testing.T) {
	got := wsArgHint([]string{"trades"})
	if !strings.Contains(got, "forgot the market") || !strings.Contains(got, "exchange:instrument") {
		t.Fatalf("hint missing market/target diagnosis:\n%s", got)
	}
	if !strings.Contains(got, "laevitas ws perpetuals trades binance:BTCUSDT") {
		t.Fatalf("hint missing canonical try command:\n%s", got)
	}
}

func TestWSArgHintStreamExchangeOnlyForgotMarketAndInstrument(t *testing.T) {
	got := wsArgHint([]string{"trades", "binance"})
	if !strings.Contains(got, "forgot the market") || !strings.Contains(got, "exchange:instrument") {
		t.Fatalf("hint missing market/target diagnosis:\n%s", got)
	}
	if !strings.Contains(got, "laevitas ws perpetuals trades binance:BTCUSDT") {
		t.Fatalf("hint missing canonical try command:\n%s", got)
	}
}

func TestWSArgHintStreamExchangeColonPairDropsExtraExchange(t *testing.T) {
	got := wsArgHint([]string{"trades", "binance", "binance:BTCUSDT"})
	if !strings.Contains(got, "forgot the market") || !strings.Contains(got, "exchange belongs inside") {
		t.Fatalf("hint missing extra-exchange diagnosis:\n%s", got)
	}
	if !strings.Contains(got, "laevitas ws perpetuals trades binance:BTCUSDT") {
		t.Fatalf("hint missing canonical try command:\n%s", got)
	}
}

func TestValidateArgsUsesWSArgHintBeforeExactArgs(t *testing.T) {
	err := validateArgs(Cmd, []string{"trades", "binance:BTCUSDT"})
	if err == nil {
		t.Fatal("validateArgs returned nil, want hint error")
	}
	if !strings.Contains(err.Error(), "forgot the market") {
		t.Fatalf("validateArgs did not surface wsArgHint:\n%s", err)
	}
}
