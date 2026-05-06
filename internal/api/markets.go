// Markets vocabulary — one normaliser, called at every CLI input
// boundary, so the rest of the codebase only ever sees canonical
// tokens.
//
// ─── canonical forms (the only tokens internal code should see) ───
//
//   perpetuals       (NOT perpetual, NOT perp)
//   futures          (NOT future, NOT fut)
//   options          (NOT option, NOT opt)
//   spot
//   predictions      (NOT prediction, NOT predict, NOT polymarket)
//   linear           (margin type — settle in stablecoin)
//   inverse          (margin type — settle in base asset)
//
// User input (CLI flag values, positional args) is normalised at the
// entry point — every cobra Run func that accepts a market type or
// margin type calls api.NormalizeMarket / api.NormalizeMargin
// before storing the value. Internal code (resolvers, panels,
// channel builders, REST clients) MUST work with the canonical form
// only.
//
// Why plural for the canonical form? It matches the WS channel
// segments (`book.perpetuals.<venue>.<instrument>`) and the existing
// CLI top-level args (`laevitas ws perpetuals book ...`, `laevitas dash book
// perpetuals BTCUSDT`) — both already in user muscle memory. The
// REST API uses singular as the filter value (`?market_type=perpetual`),
// which is the odd layer; the normaliser hands you `MarketRESTToken`
// to translate canonical → REST when you need it.
//
// Adding a new market type? Three places to touch:
//   1. Add the canonical form to canonicalMarkets below.
//   2. Add aliases the user might type to marketAliases.
//   3. Add the REST translation if it's not identity (most are).
//
// Adding a new margin type? Two places: canonicalMargins and
// marginAliases. Margin canonical form already matches what the
// REST `?margin_type=` filter expects, so no translation function
// is needed today (unlike MarketRESTToken). If a future API rev
// diverges, add a MarginRESTToken function alongside MarketRESTToken.
package api

import "strings"

// canonical market tokens — the SET of strings internal code may
// see after normalisation. Using a set rather than constants so
// validators can range over it.
var canonicalMarkets = map[string]struct{}{
	"perpetuals":  {},
	"futures":     {},
	"options":     {},
	"spot":        {},
	"predictions": {},
}

// marketAliases maps every form the user might reasonably type to
// the canonical form. Lookup is case-insensitive (caller normalises
// to lowercase before lookup). Includes the canonical form itself
// as an identity entry so a single Lookup call always works.
var marketAliases = map[string]string{
	// perpetuals
	"perpetuals": "perpetuals",
	"perpetual":  "perpetuals",
	"perp":       "perpetuals",
	"perps":      "perpetuals",
	"swap":       "perpetuals",
	"swaps":      "perpetuals",
	// futures
	"futures": "futures",
	"future":  "futures",
	"fut":     "futures",
	"dated":   "futures", // some traders call dated futures just "dated"
	// options
	"options": "options",
	"option":  "options",
	"opt":     "options",
	"opts":    "options",
	// spot
	"spot": "spot",
	// predictions / Polymarket
	"predictions": "predictions",
	"prediction":  "predictions",
	"predict":     "predictions",
	"poly":        "predictions",
	"polymarket":  "predictions",
}

// canonicalMargins is the SET of canonical margin-type tokens.
var canonicalMargins = map[string]struct{}{
	"linear":  {},
	"inverse": {},
}

// marginAliases maps user input to canonical margin form. Same
// case-insensitive convention as marketAliases.
var marginAliases = map[string]string{
	"linear":  "linear",
	"lin":     "linear",
	"usdt":    "linear", // colloquial: "USDT-margined" = linear
	"usdc":    "linear",
	"stable":  "linear",
	"inverse": "inverse",
	"inv":     "inverse",
	"coin":    "inverse", // "coin-margined" = inverse
	"coins":   "inverse",
	"crypto":  "inverse",
}

// NormalizeMarket converts any user input to the canonical market
// form. Returns the canonical token and true on hit; empty string
// and false on unknown input. Caller should surface the unknown-input
// case as a CLI error so the user sees it immediately rather than
// silently filtering wrong data.
//
// Examples:
//
//	NormalizeMarket("perp")        → "perpetuals", true
//	NormalizeMarket("Perpetuals")  → "perpetuals", true
//	NormalizeMarket("PERPETUAL")   → "perpetuals", true
//	NormalizeMarket("derivatives") → "", false
func NormalizeMarket(s string) (string, bool) {
	canonical, ok := marketAliases[strings.ToLower(strings.TrimSpace(s))]
	return canonical, ok
}

// NormalizeMargin converts any user input to the canonical margin
// form. Same contract as NormalizeMarket: returns canonical + true
// on hit, empty + false on unknown input.
//
// Examples:
//
//	NormalizeMargin("usdt")    → "linear", true
//	NormalizeMargin("inverse") → "inverse", true
//	NormalizeMargin("coin")    → "inverse", true
//	NormalizeMargin("xyz")     → "", false
func NormalizeMargin(s string) (string, bool) {
	canonical, ok := marginAliases[strings.ToLower(strings.TrimSpace(s))]
	return canonical, ok
}

// NormalizeInstrument normalizes a user-supplied instrument name for
// the product family that owns it. Crypto venues use uppercase
// instrument names across perps, futures, options, and spot; prediction
// markets can carry case-sensitive slugs / IDs, so those pass through.
func NormalizeInstrument(market, raw string) string {
	instrument := strings.TrimSpace(raw)
	switch market {
	case "perpetuals", "futures", "options", "spot":
		return strings.ToUpper(instrument)
	default:
		return instrument
	}
}

// MarketFromEndpoint returns the canonical market implied by a REST
// endpoint path. Cross-product endpoints return empty string because
// their market is request-param dependent rather than path dependent.
func MarketFromEndpoint(path string) string {
	switch {
	case strings.HasPrefix(path, "/api/v1/perpetuals/"):
		return "perpetuals"
	case strings.HasPrefix(path, "/api/v1/futures/"):
		return "futures"
	case strings.HasPrefix(path, "/api/v1/options/"):
		return "options"
	case strings.HasPrefix(path, "/api/v1/spot/"):
		return "spot"
	case strings.HasPrefix(path, "/api/v1/predictions/"):
		return "predictions"
	default:
		return ""
	}
}

// IsCanonicalMarket is true when s is already the canonical form.
// Useful for assertions in internal code that wants to verify it
// received a normalised token.
func IsCanonicalMarket(s string) bool {
	_, ok := canonicalMarkets[s]
	return ok
}

// IsCanonicalMargin is true when s is already the canonical margin
// form.
func IsCanonicalMargin(s string) bool {
	_, ok := canonicalMargins[s]
	return ok
}

// MarketRESTToken translates canonical (plural) market form to the
// singular form the REST API's ?market_type= filter expects. The
// REST and WS layers disagree on plurality for legacy reasons; this
// is the one place that translation happens. Caller should pass a
// canonical token; bad input returns the input unchanged.
//
// Mapping:
//
//	perpetuals  → perpetual
//	futures     → future
//	options     → option
//	spot        → spot           (no change)
//	predictions → prediction
func MarketRESTToken(canonical string) string {
	switch canonical {
	case "perpetuals":
		return "perpetual"
	case "futures":
		return "future"
	case "options":
		return "option"
	case "predictions":
		return "prediction"
	}
	return canonical
}

// CanonicalMarkets returns the canonical market tokens in a stable
// order. Useful for help text generation and validator ranges.
func CanonicalMarkets() []string {
	return []string{"perpetuals", "futures", "options", "spot", "predictions"}
}

// CanonicalMargins returns the canonical margin tokens in a stable
// order.
func CanonicalMargins() []string {
	return []string{"linear", "inverse"}
}
