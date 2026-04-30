package output

// Venue palette — seven brand-faithful hues plus a fallback grey for
// long-tail / unknown venues. Used by the dashboard renderers (lvt
// dash book, laevitas dash flow) to attribute aggregated liquidity, trade
// tape rows, and per-venue gauge bars to specific exchanges.
//
// Design intent:
//
//   - Each venue's colour stays the same across every dashboard so
//     muscle memory carries between sessions. We deliberately did
//     NOT pick a per-market top-N because the user pointed out that
//     "binance is gold here and amber there" would be confusing.
//   - Brand-faithful where possible: hex values are sourced from each
//     venue's official brand guidelines and only adjusted when two
//     would have collided on a dark terminal background. See the
//     per-entry comments above venuePalette for which were sourced
//     vs adjusted.
//   - Brand green (#46be52) stays reserved for "ours / positive" so
//     no venue palette entry lands on it.
//   - Each hue also has an associated icon (●▲◆■★✦▼) so users with
//     red-green or blue-yellow colour blindness can still distinguish
//     venues by shape — the renderer prefixes the venue's abbreviation
//     with its icon in the venue-strip and tape views.
//   - Long-tail venues (nado, bullish, derive, polymarket) fall back
//     to grey-with-actual-tag (e.g. ○ NDO, ○ BUL) rather than the
//     generic "···" so they're still distinguishable in dense rows.
//
// Adding a new venue: append a VenuePaletteEntry to venuePalette.
// Don't reorder — the indices are stable visual identity, and a
// reorder shuffles every dashboard's colour assignments.
type VenuePaletteEntry struct {
	Name     string // canonical lowercase name, matches wire `exchange` field
	FG       string // ANSI escape for foreground colour
	BG       string // ANSI escape for matching background (used for badges)
	Hex      string // "#rrggbb" — for lipgloss borders / non-ANSI consumers
	Icon     string // colourblind-safe shape prefix
	ShortTag string // 3-letter abbreviation for tight columns
}

// venuePalette holds the seven primary venue colours plus a grey
// fallback for unknown / long-tail venues. Order is stable across
// every dashboard — the top-N venues a market exposes always paint
// the same colour regardless of which dashboard the user opens, so
// muscle memory carries between sessions. Adding a venue means
// appending; reordering breaks every saved screenshot's colour
// identity.
//
// Brand-faithful where it doesn't sacrifice perceptual distance.
// First cut had bybit at amber #ffa500, very close to binance's
// gold #f3ba2f — the two collided on dark terminals (you couldn't
// tell binance gold from bybit amber from a metre away, which is
// fatal when those two are the most-paired venues in crypto).
// v0.8.3 swap: bybit moves to magenta #ff4081. That gives ~120°
// of hue separation from binance and works regardless of brand
// purity. Every other venue stays brand-faithful.
//
//	binance     #f3ba2f   gold — official brand
//	bybit       #ff4081   magenta — perceptual-distance pick (bybit brand is amber but conflicted with binance)
//	okx         #64b5f6   sky-blue — picked to stay distinguishable; OKX's own brand is monochrome black/white
//	coinbase    #0052ff   royal-blue — official brand
//	deribit     #1a4fe0   electric-blue — official brand mid-blue
//	hyperliquid #97fce4   mint/teal — official brand
//	kraken      #7132f5   purple — official brand
//
// Three blues (okx sky / deribit electric / coinbase royal) coexist;
// they're distinguishable by saturation and brightness on a dark bg
// but it's the tightest spot in the palette. Each venue carries a
// distinct icon (●▲◆■★✦▼) so colour-blind users navigate by shape.
var venuePalette = []VenuePaletteEntry{
	{
		Name: "binance", ShortTag: "BIN", Icon: "●",
		FG: "\033[38;2;243;186;47m", BG: "\033[48;2;243;186;47m", Hex: "#f3ba2f",
	},
	{
		Name: "bybit", ShortTag: "BYB", Icon: "▲",
		FG: "\033[38;2;255;64;129m", BG: "\033[48;2;255;64;129m", Hex: "#ff4081",
	},
	{
		Name: "okx", ShortTag: "OKX", Icon: "◆",
		FG: "\033[38;2;100;181;246m", BG: "\033[48;2;100;181;246m", Hex: "#64b5f6",
	},
	{
		Name: "coinbase", ShortTag: "COIN", Icon: "★",
		FG: "\033[38;2;0;82;255m", BG: "\033[48;2;0;82;255m", Hex: "#0052ff",
	},
	{
		Name: "deribit", ShortTag: "DBT", Icon: "■",
		FG: "\033[38;2;26;79;224m", BG: "\033[48;2;26;79;224m", Hex: "#1a4fe0",
	},
	{
		Name: "hyperliquid", ShortTag: "HYP", Icon: "✦",
		FG: "\033[38;2;151;252;228m", BG: "\033[48;2;151;252;228m", Hex: "#97fce4",
	},
	{
		Name: "kraken", ShortTag: "KRK", Icon: "▼",
		FG: "\033[38;2;113;50;245m", BG: "\033[48;2;113;50;245m", Hex: "#7132f5",
	},
}

// VenueColor returns the palette entry for a given venue name. Lookup
// is case-insensitive. For known venues the curated entry from
// venuePalette is returned; for long-tail / unknown venues we fall
// back to a grey colour but still echo the actual venue name in
// ShortTag (e.g. "○ NDO" for nado) so the row stays distinguishable
// in dense displays. The bool return signals whether the entry was
// a known curated venue (true) or a fallback (false) — useful when
// the renderer wants to render the legend differently for the two
// classes.
func VenueColor(name string) (VenuePaletteEntry, bool) {
	lower := lower(name)
	for _, e := range venuePalette {
		if e.Name == lower {
			return e, true
		}
	}
	// Long-tail fallback: grey colour, generic icon, but ShortTag
	// derived from the venue's actual name (uppercased, capped at
	// 4 chars) so users still know which venue is which.
	tag := upperShort(lower)
	return VenuePaletteEntry{
		Name:     lower,
		ShortTag: tag,
		Icon:     "○",
		FG:       BrandGreyMid,
		BG:       "\033[48;2;71;80;87m",
		Hex:      "#475057",
	}, false
}

// upperShort returns an uppercase abbreviation of s, capped at 4
// characters. Used to build a stable tag for long-tail venues — e.g.
// "polymarket" → "POLY", "nado" → "NADO", "bullish" → "BULL".
// Picks letters from the start of the name; that's the convention
// every trading screen uses.
func upperShort(s string) string {
	if len(s) > 4 {
		s = s[:4]
	}
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' {
			c -= 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

// VenuePalette returns the canonical primary palette in order. Used
// by the help overlay's "venue legend" section so users can see which
// colour means which venue without having to subscribe first.
func VenuePalette() []VenuePaletteEntry {
	return venuePalette
}

// CuratedVenueNames returns just the lowercase venue names from the
// palette in display order. Single source of truth for "which venues
// do we treat as primary" — the dashboard panels (book scan ordering,
// "waiting on …" hints) all read from this rather than maintaining
// their own duplicate list. Adding a venue means appending to
// venuePalette and every consumer picks it up.
func CuratedVenueNames() []string {
	out := make([]string, len(venuePalette))
	for i, e := range venuePalette {
		out[i] = e.Name
	}
	return out
}

// CuratedVenueIndex returns the rank of a venue in the curated
// palette, or -1 if the venue is long-tail. Used by sort comparators
// when display order should match palette order.
func CuratedVenueIndex(name string) int {
	lower := lower(name)
	for i, e := range venuePalette {
		if e.Name == lower {
			return i
		}
	}
	return -1
}

// lower is a tiny strings.ToLower replacement that avoids pulling in
// the strings import for one call site. Only handles ASCII —
// exchange names are always lowercase ASCII anyway.
func lower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
