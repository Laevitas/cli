package dash

import (
	"strings"
	"testing"
)

// ParseFlowArgs covers the inputs the cobra layer will hand it.
// We use table tests because most cases are independent
// pairs of (input → expected output / error fragment).

func TestParseFlowArgs(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantCcy     string
		wantMarket  string
		wantErrFrag string
	}{
		// ─── happy paths ────────────────────────────────────────────────
		{
			name:       "perpetuals BTC canonical",
			args:       []string{"perpetuals", "BTC"},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
		},
		{
			name:       "lowercase currency uppercased",
			args:       []string{"perpetuals", "btc"},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
		},
		{
			name:       "perp alias normalises to canonical",
			args:       []string{"perp", "ETH"},
			wantCcy:    "ETH",
			wantMarket: "perpetuals",
		},
		{
			name:       "swap alias normalises to canonical",
			args:       []string{"swap", "SOL"},
			wantCcy:    "SOL",
			wantMarket: "perpetuals",
		},
		{
			name:       "currency surrounded by whitespace is trimmed",
			args:       []string{"perpetuals", "  BTC  "},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
		},
		{
			name:       "market surrounded by whitespace is trimmed",
			args:       []string{"  perp  ", "BTC"},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
		},

		// ─── error paths ────────────────────────────────────────────────
		{
			name:        "no args at all",
			args:        []string{},
			wantErrFrag: "market and currency",
		},
		{
			name:        "only one arg",
			args:        []string{"perpetuals"},
			wantErrFrag: "market and currency",
		},
		{
			name:        "swapped order: currency-first rejected",
			args:        []string{"BTC", "perpetuals"},
			wantErrFrag: "unknown market",
		},
		{
			name:        "currency with digits rejected",
			args:        []string{"perpetuals", "BTC1"},
			wantErrFrag: "invalid currency",
		},
		{
			name:        "currency with hyphen rejected",
			args:        []string{"perpetuals", "BT-C"},
			wantErrFrag: "invalid currency",
		},
		{
			name:        "very long currency rejected",
			args:        []string{"perpetuals", "VERYLONGSYM"},
			wantErrFrag: "invalid currency",
		},
		{
			name:        "blank currency rejected",
			args:        []string{"perpetuals", "   "},
			wantErrFrag: "invalid currency",
		},
		{
			name:        "unknown market rejected",
			args:        []string{"wibble", "BTC"},
			wantErrFrag: "unknown market",
		},
		{
			name:        "futures market rejected for v0.10.0",
			args:        []string{"futures", "BTC"},
			wantErrFrag: "perpetuals only",
		},
		{
			name:        "options market rejected",
			args:        []string{"options", "BTC"},
			wantErrFrag: "perpetuals only",
		},
		{
			name:        "spot market rejected",
			args:        []string{"spot", "BTC"},
			wantErrFrag: "perpetuals only",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFlowArgs(tc.args)
			if tc.wantErrFrag != "" {
				if err == nil {
					t.Fatalf("expected error containing %q; got success %+v", tc.wantErrFrag, got)
				}
				if !strings.Contains(err.Error(), tc.wantErrFrag) {
					t.Errorf("error %q missing fragment %q", err.Error(), tc.wantErrFrag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Currency != tc.wantCcy {
				t.Errorf("Currency = %q, want %q", got.Currency, tc.wantCcy)
			}
			if got.Market != tc.wantMarket {
				t.Errorf("Market = %q, want %q", got.Market, tc.wantMarket)
			}
		})
	}
}
