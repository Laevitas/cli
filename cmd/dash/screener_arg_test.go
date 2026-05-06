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
		name         string
		args         []string
		exchange     string
		sortKey      string
		sortAsc      bool
		wantCcy      string
		wantExchange string
		wantMarket   string
		wantSort     string
		wantDesc     bool
		wantErrFrag  string
	}{
		// ─── happy paths ────────────────────────────────────────────────
		{
			name:       "perpetuals BTC canonical",
			args:       []string{"perpetuals", "BTC"},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
			wantSort:   "volume",
			wantDesc:   true,
		},
		{
			name:       "lowercase currency uppercased",
			args:       []string{"perpetuals", "btc"},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
			wantSort:   "volume",
			wantDesc:   true,
		},
		{
			name:       "perp alias normalises to canonical",
			args:       []string{"perp", "ETH"},
			wantCcy:    "ETH",
			wantMarket: "perpetuals",
			wantSort:   "volume",
			wantDesc:   true,
		},
		{
			name:       "swap alias normalises to canonical",
			args:       []string{"swap", "SOL"},
			wantCcy:    "SOL",
			wantMarket: "perpetuals",
			wantSort:   "volume",
			wantDesc:   true,
		},
		{
			name:       "currency surrounded by whitespace is trimmed",
			args:       []string{"perpetuals", "  BTC  "},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
			wantSort:   "volume",
			wantDesc:   true,
		},
		{
			name:       "market surrounded by whitespace is trimmed",
			args:       []string{"  perp  ", "BTC"},
			wantCcy:    "BTC",
			wantMarket: "perpetuals",
			wantSort:   "volume",
			wantDesc:   true,
		},
		{
			name:       "futures supported",
			args:       []string{"futures", "BTC"},
			sortKey:    "basis",
			wantCcy:    "BTC",
			wantMarket: "futures",
			wantSort:   "basis",
			wantDesc:   true,
		},
		{
			name:         "spot exchange-only supported",
			args:         []string{"spot"},
			exchange:     "Binance",
			sortKey:      "quote-volume",
			sortAsc:      true,
			wantExchange: "binance",
			wantMarket:   "spot",
			wantSort:     "quote-volume",
			wantDesc:     false,
		},
		{
			name:         "spot currency and exchange narrow scope",
			args:         []string{"spot", "btc"},
			exchange:     "binance",
			sortKey:      "liquidity",
			wantCcy:      "BTC",
			wantExchange: "binance",
			wantMarket:   "spot",
			wantSort:     "liquidity",
			wantDesc:     true,
		},

		// ─── error paths ────────────────────────────────────────────────
		{
			name:        "no args at all",
			args:        []string{},
			wantErrFrag: "market is required",
		},
		{
			name:        "only one arg",
			args:        []string{"perpetuals"},
			wantErrFrag: "currency or --exchange",
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
			name:        "options market rejected",
			args:        []string{"options", "BTC"},
			wantErrFrag: "supports perpetuals, futures, and spot",
		},
		{
			name:        "invalid sort for spot",
			args:        []string{"spot", "BTC"},
			sortKey:     "funding",
			wantErrFrag: "invalid sort",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFlowArgs(tc.args, tc.exchange, tc.sortKey, tc.sortAsc)
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
			if got.Exchange != tc.wantExchange {
				t.Errorf("Exchange = %q, want %q", got.Exchange, tc.wantExchange)
			}
			if got.Market != tc.wantMarket {
				t.Errorf("Market = %q, want %q", got.Market, tc.wantMarket)
			}
			if got.Sort != tc.wantSort {
				t.Errorf("Sort = %q, want %q", got.Sort, tc.wantSort)
			}
			if got.SortDesc != tc.wantDesc {
				t.Errorf("SortDesc = %v, want %v", got.SortDesc, tc.wantDesc)
			}
		})
	}
}
