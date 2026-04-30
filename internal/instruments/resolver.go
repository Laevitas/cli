// Package instruments resolves a (currency, market, margin) tuple
// to per-venue contract names so dashboards and screeners can build
// concrete WebSocket subscriptions without hand-maintaining a
// venue→symbol mapping table.
//
// The resolver is a thin wrapper over GET /api/v1/instruments. Same
// data the `laevitas instruments list` command surfaces, just decoded
// into a typed slice and post-processed for "one canonical contract
// per venue" semantics.
//
// ─── Per-venue preference ──────────────────────────────────────────
//
// "BTC perp-linear" isn't unique on every venue — binance and bybit
// both list BTCUSDT (USDT-quoted) and BTCUSDC (USDC-quoted) linear
// perps. The resolver picks ONE contract per venue using a quote-
// currency preference order:
//
//	USDT → USDC → USD → first available
//
// Why this order? USDT-margined perps dominate volume on every major
// derivatives venue (binance, bybit, okx, kraken). USDC is the
// second-most-liquid stablecoin, used by deribit and hyperliquid.
// USD covers coinbase. "First available" is the safety net for
// anything else.
//
// Caller can override with an explicit preference: pass `preferQuote
// = "USDC"` to get USDC contracts where they exist, skip the venue
// where they don't. Default ("") triggers the cascade above.
//
// Same logic applies to spot (where USDT > USDC > USD is the same
// preference order) and to perp-inverse (where the practical answer
// is "the venue's only inverse contract" — the cascade still runs
// in case a venue lists multiple inverse pairs).
package instruments

import (
	"fmt"
	"sort"

	"github.com/laevitas/cli/internal/api"
)

// Instrument is the JSON shape returned by GET /api/v1/instruments
// for the fields the resolver cares about. The full payload has 30+
// fields; we decode only what we need so adding a new server-side
// field doesn't churn this struct.
type Instrument struct {
	Exchange       string `json:"exchange"`
	InstrumentName string `json:"instrument_name"`
	BaseCurrency   string `json:"base_currency"`
	QuoteCurrency  string `json:"quote_currency"`
	MarketType     string `json:"market_type"` // singular: perpetual / future / option / spot
	MarginType     string `json:"margin_type"` // linear / inverse / "" (spot)
	SubExchange   string `json:"sub_exchange"` // empty = main venue; non-empty = sub-venue fork
	Status         string `json:"status"`
}

// instrumentsEnvelope mirrors the standard REST success envelope
// for the instruments endpoint. Failure cases bubble up via
// api.Client.Do's error wrapping; we only need success-shape decode.
type instrumentsEnvelope struct {
	Success bool         `json:"success"`
	Data    []Instrument `json:"data"`
}

// VenueContract is the resolver's output: one contract per venue.
// Exchange and InstrumentName are the only fields callers need to
// build WS channels (`book.<market>.<exchange>.<instrument>`); the
// extras (QuoteCurrency, MarginType) are echoed back for diagnostics
// and "what got picked" displays in the dashboard footer.
type VenueContract struct {
	Exchange       string
	InstrumentName string
	QuoteCurrency  string
	MarginType     string
}

// Query bundles the resolver inputs. Constructed once per dashboard
// open; passed by value through the resolver call. Lower-cased
// canonical tokens expected (NormalizeMarket / NormalizeMargin
// already applied at the CLI entry point).
//
// Spot has no MarginType — leave it empty. Predictions/options have
// neither MarginType nor (typically) a meaningful per-venue cascade,
// but the resolver still works on them: it just returns the first
// matching contract per venue.
type Query struct {
	BaseCurrency string // canonical: "BTC", "ETH" — passed verbatim to ?base_currency
	Market       string // canonical (plural): "perpetuals", "spot", "futures"
	Margin       string // canonical: "linear", "inverse", or "" (spot/options)
	PreferQuote  string // explicit override: "USDT", "USDC", ...; empty triggers cascade
}

// defaultQuotePreference is the per-venue cascade applied when
// Query.PreferQuote is empty. Caller can override either by setting
// PreferQuote (strict — only that quote currency) or by editing
// this list (loose — different cascade).
//
// Order rationale: USDT-margined perps dominate volume on binance,
// bybit, okx, kraken; USDC covers deribit, hyperliquid; USD covers
// coinbase and a handful of US-regulated venues. Anything past
// "USD" falls into "first available" which is rare but graceful.
var defaultQuotePreference = []string{"USDT", "USDC", "USD"}

// Resolve performs the API call and applies the per-venue preference
// rule. Returns one VenueContract per venue that lists the
// requested product, sorted by exchange name for deterministic
// ordering across runs.
//
// On API error (network down, auth failure, rate limit), the error
// bubbles up unchanged from api.Client. Empty-result is NOT an
// error — caller decides whether "no venues list this product" is
// expected (e.g. user typed a typo'd currency) or a graceful empty
// state.
func Resolve(client *api.Client, q Query) ([]VenueContract, error) {
	if !api.IsCanonicalMarket(q.Market) {
		return nil, fmt.Errorf("instruments.Resolve: market %q is not canonical — call api.NormalizeMarket at the CLI entry", q.Market)
	}
	if q.Margin != "" && !api.IsCanonicalMargin(q.Margin) {
		return nil, fmt.Errorf("instruments.Resolve: margin %q is not canonical — call api.NormalizeMargin at the CLI entry", q.Margin)
	}

	params := &api.RequestParams{
		BaseCurrency: q.BaseCurrency,
		MarketType:   api.MarketRESTToken(q.Market), // canonical → singular for REST
		MarginType:   q.Margin,
		Status:       "active", // never resolve to delisted contracts
	}
	// Strict quote filter when caller specified one. Empty ⇒ no
	// server-side filter, we apply the cascade client-side instead
	// so we still see every venue's options.
	if q.PreferQuote != "" {
		params.QuoteCurrency = q.PreferQuote
	}

	var env instrumentsEnvelope
	if err := client.GetJSON(api.InstrumentsList, params, &env); err != nil {
		return nil, fmt.Errorf("instruments list: %w", err)
	}

	return pickPerVenue(env.Data, q.PreferQuote), nil
}

// pickPerVenue applies the per-venue preference cascade. Groups the
// API result by exchange, then for each venue picks the contract
// whose quote currency comes earliest in the preference order. With
// PreferQuote set, only contracts in that quote currency reach this
// function (the API pre-filtered) so the cascade is a no-op — we
// just pick the first contract per venue.
//
// Sub-exchange variants (sub_exchange != "") are dropped before
// cascade. These are parallel sub-venue books (e.g. hyperliquid's
// `hyna:ETH`, a forked sub-exchange) that the WS gateway exposes
// under different routing — including them here would have the
// resolver pick a sub-venue contract while the panel subscribes
// using the main exchange tag, producing a silent no-data state.
//
// Stable sort by exchange so two consecutive resolves on unchanged
// data produce identical output (matters for caching layers and for
// snapshot tests).
func pickPerVenue(items []Instrument, preferQuote string) []VenueContract {
	byVenue := make(map[string][]Instrument)
	for _, it := range items {
		// Drop sub-exchange forks — see function doc.
		if it.SubExchange != "" {
			continue
		}
		byVenue[it.Exchange] = append(byVenue[it.Exchange], it)
	}

	out := make([]VenueContract, 0, len(byVenue))
	for venue, contracts := range byVenue {
		picked := pickOneContract(contracts, preferQuote)
		if picked == nil {
			continue
		}
		out = append(out, VenueContract{
			Exchange:       venue,
			InstrumentName: picked.InstrumentName,
			QuoteCurrency:  picked.QuoteCurrency,
			MarginType:     picked.MarginType,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Exchange < out[j].Exchange })
	return out
}

// pickOneContract returns the single contract from `contracts` that
// best matches the preference. With an explicit PreferQuote the
// caller already filtered server-side, so we apply the tie-break
// (canonical-name preference) over whatever came back. Without one,
// walk the default cascade tier by tier and pick the canonical
// contract at the first matching tier.
//
// Returns nil if `contracts` is empty (callers handle nil as
// "venue contributes nothing").
func pickOneContract(contracts []Instrument, preferQuote string) *Instrument {
	if len(contracts) == 0 {
		return nil
	}
	if preferQuote != "" {
		// Server-side filter already narrowed; the caller can have
		// gotten back e.g. hyperliquid's [ETH, ETH-USD] both at
		// quote=USD. Tie-break to the WS-canonical name.
		return preferCanonical(contracts)
	}
	for _, q := range defaultQuotePreference {
		var sameTier []Instrument
		for _, c := range contracts {
			if c.QuoteCurrency == q {
				sameTier = append(sameTier, c)
			}
		}
		if len(sameTier) > 0 {
			return preferCanonical(sameTier)
		}
	}
	// Cascade exhausted with no hit — fall back to first available
	// in the input. This path triggers only for venues whose quote
	// currency is outside our preference list (rare; e.g. a perp
	// quoted in BTC).
	return &contracts[0]
}

// preferCanonical breaks ties between contracts at the same quote
// tier by preferring the WS-canonical form. Heuristic: pick the
// instrument name that looks most like a venue's "main" contract
// rather than an alias.
//
// Hyperliquid's registry returns both `ETH` and `ETH-USD` for the
// same product; the WS gateway only routes `ETH-USD`. In general,
// a contract name containing a separator (`-`, `_`, `:`) is more
// specific than a bare base-currency token, and venues that expose
// both tend to use the dashed form as the WS-canonical name.
//
// The heuristic isn't perfect — a venue could in principle do the
// opposite — but it matches every venue we currently support
// (binance BTCUSDT vs nothing, okx BTC-USDT-SWAP vs nothing, hyperliquid
// ETH-USD vs ETH alias). If we hit a venue where it goes wrong,
// add a per-venue override here rather than removing the heuristic
// (it's right far more often than it's wrong).
//
// Returns the longest name that contains at least one separator;
// falls back to the first input if no candidate matches.
func preferCanonical(contracts []Instrument) *Instrument {
	if len(contracts) == 1 {
		return &contracts[0]
	}
	bestIdx := -1
	for i, c := range contracts {
		if !nameHasSeparator(c.InstrumentName) {
			continue
		}
		if bestIdx == -1 || len(c.InstrumentName) > len(contracts[bestIdx].InstrumentName) {
			bestIdx = i
		}
	}
	if bestIdx == -1 {
		return &contracts[0]
	}
	return &contracts[bestIdx]
}

// nameHasSeparator is true when s contains one of the separators
// venues use in their canonical instrument names: `-`, `_`, `:`.
// Used by preferCanonical to bias toward the more-specific form
// when a venue exposes both an alias (e.g. `ETH`) and the canonical
// (`ETH-USD`).
func nameHasSeparator(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '-', '_', ':':
			return true
		}
	}
	return false
}
