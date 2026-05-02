package panels

// Venue strip — the right-hand pane of the book dashboard. Stacks
// a CONSOLIDATED summary card on top of one card per discovered
// venue, with a "waiting on …" hint at the bottom for curated
// venues yet to fire.
//
// This file owns the bordered-card composition rules. Two patterns
// are load-bearing here and worth keeping in sync if you copy the
// pattern to another dashboard:
//
//  1. Compose row content via lipgloss.JoinHorizontal — never `+`
//     concatenation of `.Render()` outputs. lipgloss measures
//     widths through JoinHorizontal/JoinVertical; raw concat
//     produces nested ANSI byte sequences whose visible width
//     diverges from the byte length, and the bordered card breaks
//     when the inner content wraps mid-escape.
//
//  2. Set Width(N) on the bordered style and treat N as *content*
//     width. lipgloss adds 2 cells of padding and 2 cells of
//     border on top. Avoid combining Width with MaxWidth — they
//     interact badly with pre-styled content.
//
// (See feedback memory entry "lipgloss multi-pane TUI width
// measurement" for the diagnostic story behind these rules.)

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/laevitas/cli/internal/agg"
	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/output"
)

// renderStrip paints the right-side panel as a vertical stack of
// lipgloss bordered cards. Order:
//
//  1. CONSOLIDATED   — brand-green border, the cross-venue summary,
//     most important so it reads first
//  2. per-venue card — one per discovered venue, bordered in the
//     venue's brand colour so each card is attributable at a glance
//  3. "waiting on …" line for curated venues yet to fire
func (p *BookPanel) renderStrip(w, h int, books map[string]*api.BookSnapshot, venues []string, ctx dashboard.PanelContext) string {
	grey := output.BrandGreyMid
	reset := output.Reset

	cards := make([]string, 0, len(venues)+2)
	cards = append(cards, p.renderConsolidatedCard(books, venues, w))
	for _, v := range venues {
		cards = append(cards, p.renderVenueCard(v, books[v], w))
	}

	stack := lipgloss.JoinVertical(lipgloss.Left, cards...)

	missing := p.missingCuratedVenues(books)
	if len(missing) > 0 {
		stack += "\n" + grey + "waiting on: " + strings.Join(missing, ", ") + " " + ctx.SpinnerFrame + reset
	}
	return stack
}

// renderVenueCard formats one venue's block: name + total liquidity
// on the title row, two stat rows (best bid/ask, spread+imbalance),
// bordered in the venue's brand colour.
//
// Icons (●▲◆■★✦▼) were dropped from the title in v0.8.3 because
// every glyph in the curated palette is East-Asian-Ambiguous —
// lipgloss measures them as width 1 but Windows Terminal renders
// them as width 2, breaking the border math. The venue's brand
// colour on the name is enough visual identification.
func (p *BookPanel) renderVenueCard(venue string, snap *api.BookSnapshot, w int) string {
	if snap == nil {
		return ""
	}
	vc, _ := output.VenueColor(venue)
	bidLiq, askLiq, imb := snap.LiquidityForTier(p.depthTier)
	totalLiq := bidLiq + askLiq
	bb, ba := snap.BestLevels()

	// Per-venue spread is always non-negative — single-venue books
	// can't cross. The arb concept only applies to CONSOLIDATED.
	spread := 0.0
	if ba.Price > 0 && bb.Price > 0 {
		spread = ba.Price - bb.Price
	}

	venueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(vc.Hex)).Bold(true)
	greyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreyMidHex))
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreenHex))
	redStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandRedHex))

	// Title row: VENUE + instrument name + total liquidity in the
	// base currency. Showing snap.InstrumentName (e.g. ETHUSDT,
	// ETH-USDC-PERP, ETH-PERP) lets the user see exactly which
	// contract is contributing on each venue — the resolver can
	// pick different quote currencies per venue (USDT on binance,
	// USDC on deribit/hyperliquid), and the user needs to know
	// which one they're looking at.
	title := lipgloss.JoinHorizontal(lipgloss.Top,
		venueStyle.Render(strings.ToUpper(venue)),
		"  ",
		greyStyle.Render(snap.InstrumentName+"  "+output.FormatBookSize(totalLiq)+" "+snap.Currency),
	)
	row1 := lipgloss.JoinHorizontal(lipgloss.Top,
		greyStyle.Render("bid "),
		greenStyle.Render(output.FormatBookPrice(bb.Price)),
		greyStyle.Render("   ask "),
		redStyle.Render(output.FormatBookPrice(ba.Price)),
	)
	row2 := lipgloss.JoinHorizontal(lipgloss.Top,
		greyStyle.Render("spread "),
		output.FormatBookPrice(spread),
		greyStyle.Render("   imb "),
		colorImbalanceLG(imb),
	)

	body := lipgloss.JoinVertical(lipgloss.Left, title, row1, row2)
	return cardStyle(w, vc.Hex).Render(body)
}

// renderConsolidatedCard wraps the cross-venue summary in a card
// with the brand-green border so it stands out from the per-venue
// cards.
func (p *BookPanel) renderConsolidatedCard(books map[string]*api.BookSnapshot, venues []string, w int) string {
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreyLightHex)).Bold(true)
	header := titleStyle.Render("CONSOLIDATED")
	body := p.renderConsolidatedBlock(books, venues, w)

	return cardStyle(w, brandGreenHex).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, body),
	)
}

// renderConsolidatedBlock formats the cross-venue summary shown
// inside the CONSOLIDATED card. Three rows:
//
//	spread     8.30   1.09 bps          ← when consolidated book non-crossed
//	liq        bid 3.89   ask 39.66
//	imb        -82.1%
//
// When the book is *crossed* (best bid on venue A above best ask
// on venue B), the first row swaps to:
//
//	ARB +4.40   buy bybit / sell binance
//
// The arb row reads green; everything else stays brand-grey labels
// + neutral values. We don't repeat best-bid / best-ask textually —
// the aggregated ladder already shows them with venue attribution.
func (p *BookPanel) renderConsolidatedBlock(books map[string]*api.BookSnapshot, venues []string, _ int) string {
	greyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreyMidHex))
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreenHex))

	vbs := buildVenueBooks(books, venues, p.depthTier)
	q := agg.CrossVenueSpread(vbs)

	totalBidLiq := 0.0
	totalAskLiq := 0.0
	for _, snap := range books {
		bl, al, _ := snap.LiquidityForTier(p.depthTier)
		totalBidLiq += bl
		totalAskLiq += al
	}
	imb := 0.0
	if (totalBidLiq + totalAskLiq) > 0 {
		imb = (totalBidLiq - totalAskLiq) / (totalBidLiq + totalAskLiq)
	}

	var row1 string
	if q.Arb > 0 {
		row1 = lipgloss.JoinHorizontal(lipgloss.Top,
			greyStyle.Render("ARB    "),
			greenStyle.Render("+"+output.FormatBookPrice(q.Arb)),
			greyStyle.Render("  buy "),
			q.BuyVenue,
			greyStyle.Render(" / sell "),
			q.SellVenue,
		)
	} else {
		row1 = lipgloss.JoinHorizontal(lipgloss.Top,
			greyStyle.Render("spread "),
			output.FormatBookPrice(q.Spread),
			greyStyle.Render("  "+output.FormatNum(q.SpreadBps, 2)+" bps"),
		)
	}

	row2 := lipgloss.JoinHorizontal(lipgloss.Top,
		greyStyle.Render("liq    bid "),
		output.FormatBookSize(totalBidLiq),
		greyStyle.Render("  ask "),
		output.FormatBookSize(totalAskLiq),
	)

	row3 := lipgloss.JoinHorizontal(lipgloss.Top,
		greyStyle.Render("imb    "),
		colorImbalanceLG(imb),
	)

	return lipgloss.JoinVertical(lipgloss.Left, row1, row2, row3)
}

// renderConsolidatedLine is the narrow-terminal one-line variant
// of the consolidated block. Same spread/arb semantic as the full
// block: when crossed, the line shows ARB instead of spread.
func (p *BookPanel) renderConsolidatedLine(w int, books map[string]*api.BookSnapshot, venues []string) string {
	grey := output.BrandGreyMid
	green := output.BrandGreen
	red := output.Red
	reset := output.Reset
	vbs := buildVenueBooks(books, venues, p.depthTier)
	q := agg.CrossVenueSpread(vbs)
	if q.Arb > 0 {
		return grey + "consolidated   bid " + reset + green + output.FormatBookPrice(q.BestBid.Price) + reset +
			grey + "   ask " + reset + red + output.FormatBookPrice(q.BestAsk.Price) + reset +
			grey + "   ARB " + reset + green + "+" + output.FormatBookPrice(q.Arb) + reset
	}
	return grey + "consolidated   bid " + reset + green + output.FormatBookPrice(q.BestBid.Price) + reset +
		grey + "   ask " + reset + red + output.FormatBookPrice(q.BestAsk.Price) + reset +
		grey + "   spread " + reset + output.FormatBookPrice(q.Spread)
}

// staleAfter is the wait window past first-feed-arrived after which
// a missing venue is annotated as "stale" in the footer. Below this
// threshold the wait is silent — most healthy venues fire their
// first snapshot within ~1-2 seconds of the connection going up.
//
// dropAfter is the harder threshold past which a venue is removed
// from the footer entirely. With proven feed health (≥1 other venue
// received), a venue that hasn't arrived this far in is almost
// certainly suffering a registry/gateway coverage gap, not a transient
// slow start.
//
// Tuning history:
//   - v0.8.3: 5s/30s. Agent feedback (Ahab) reported 30s felt too
//     patient — venues with no working WS feed kept showing "stale"
//     across a typical agent's observation window. Tightened to 3s/15s
//     in v0.8.4 so the drop fires within the expected interaction
//     budget without false-positive on genuinely-slow venues.
const (
	staleAfter = 3 * time.Second
	dropAfter  = 15 * time.Second
)

// missingCuratedVenues returns venues we expect snapshots from but
// haven't seen yet. Surfaced in the strip footer ("waiting on …").
//
// Two sources of truth for the venue pool, in priority order:
//
//  1. expectedVenues — set by the resolver in currency mode. Lists
//     every venue that the API said carries this exact product
//     (e.g. BTC perp-linear → binance, bybit, okx, hyperliquid).
//     Coinbase (no USDT perp), kraken (no USDT perp), deribit
//     (USDC-only) won't be in this set, so we won't tell the user
//     we're waiting on them.
//
//  2. curated palette — fallback for literal mode (legacy `dash
//     book perpetuals BTCUSDT` path). Reads output.CuratedVenueNames
//     since we have no per-product venue list.
//
// Three time-based stages (gated on feed-health, i.e. p.startAt
// being non-zero meaning at least one venue's first snapshot has
// arrived — that proves the connection works, so subsequent waits
// reflect per-venue silence, not connection latency):
//
//   - Active wait     (age < 5s):    plain venue name
//   - Stale wait      (5s < age):    "venue (stale Ns)"
//   - Drop entirely   (age > 30s):   not in the returned slice
//
// Pre-feed-health (startAt zero), every missing venue stays in
// active-wait — we have no idea yet whether the connection itself
// is up.
func (p *BookPanel) missingCuratedVenues(books map[string]*api.BookSnapshot) []string {
	var pool []string
	if len(p.expectedVenues) > 0 {
		pool = make([]string, 0, len(p.expectedVenues))
		for v := range p.expectedVenues {
			pool = append(pool, v)
		}
		sort.Strings(pool)
	} else {
		pool = output.CuratedVenueNames()
	}

	// Compute "age since the connection proved healthy" for the
	// stale/drop stage gate. Zero when we've not yet seen the first
	// snapshot from any venue.
	var age time.Duration
	if !p.startAt.IsZero() {
		age = time.Since(p.startAt)
	}

	missing := []string{}
	for _, v := range pool {
		if _, has := books[v]; has {
			continue
		}
		// Drop entirely past the hard window — proven not coming.
		if age > dropAfter {
			continue
		}
		// Stale annotation when feed is healthy and venue is past
		// the soft window. Format reads "venue (stale Ns)".
		if age > staleAfter {
			secs := int(age.Seconds())
			missing = append(missing, fmt.Sprintf("%s (stale %ds)", v, secs))
			continue
		}
		// Active wait — plain venue name, no annotation.
		missing = append(missing, v)
	}
	if len(missing) > 3 {
		missing = missing[:3]
		missing = append(missing, "…")
	}
	return missing
}

// cardStyle returns the bordered style used for every strip card.
// One place to tweak padding, border, and width clamp.
//
// Width(innerW) is the *content* width; lipgloss adds 2 cells of
// padding (Padding(0,1)) and 2 cells of border on top, so the
// rendered card occupies innerW+4 cells. cardCapW=50 fits the
// worst-case CONSOLIDATED ARB row ("ARB +X.XXXX  buy binance /
// sell bybit" ≈ 38 visible cells) plus padding+border without
// wrapping. MaxWidth is deliberately not used — it interacts
// badly with pre-styled (ANSI-containing) content and was the
// root cause of broken borders in early v0.8.3 dev.
func cardStyle(width int, borderColor string) lipgloss.Style {
	const cardCapW = 50
	target := width
	if target > cardCapW {
		target = cardCapW
	}
	innerW := target - 4
	if innerW < 10 {
		innerW = 10
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1).
		Width(innerW)
}

// colorImbalanceLG is the lipgloss-rendered version of
// output.ColorImbalance — same green/red sign convention but
// emitted through a lipgloss style so the cell composes cleanly
// inside a bordered card via JoinHorizontal.
func colorImbalanceLG(imb float64) string {
	pct := imb * 100
	sign := "+"
	col := brandGreenHex
	if imb < 0 {
		sign = ""
		col = brandRedHex
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(col)).
		Render(fmt.Sprintf("%s%.1f%%", sign, pct))
}

// Brand hex constants colocated here so the lipgloss styles can
// use them without round-tripping through the ANSI escape strings
// in internal/output. Single source of truth for the dashboard
// panel's colour palette; matches output.BrandGreen / BrandGreyMid
// / BrandGreyLight / Red byte-for-byte.
const (
	brandGreenHex     = "#46be52"
	brandGreyMidHex   = "#475057"
	brandGreyLightHex = "#ececec"
	brandRedHex       = "#ff0000"
)
