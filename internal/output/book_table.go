package output

// Inline table renderer for the L2 book snapshot shape.
//
// The snapshot payload (asks/bids arrays + microprice + metadata)
// can't be rendered as a normal tabular column — the table printer
// would dump the raw Go map literal `[map[price:78487 size:4.65] …]`
// into a single cell, which is unreadable. This renderer detects
// the snapshot shape on the raw envelope bytes and produces a
// centre-price ladder block instead:
//
//   binance · BTCUSDT · linear · 2026-05-02T16:16:19Z   microprice 78,487.35
//
//        ask        size
//   78,488.00       0.002
//   78,487.80       0.017
//   78,487.70       0.002
//   78,487.50       0.006
//   78,487.40       5.134
//   ─── spread 0.10 ───
//   78,487.30       4.650
//   78,487.20       0.019
//   78,487.10       0.126
//   78,486.80       0.146
//   78,486.70       0.103
//
// Multiple records (e.g. -n 5) render as separate blocks with a
// blank line between. Default --limit on orderbook-raw is 1 (set
// in cmdutil.ApplySnapshotDefaults) so the typical case is one
// block — the multi-record path is a fallback for users who
// explicitly asked for more.
//
// Why a separate detector + renderer instead of teaching toRows
// about asks/bids? Because the shape is fundamentally non-tabular:
// asks and bids are "rows" themselves, not "cells". Trying to
// flatten them into the existing table grid would either lose
// information (one row per snapshot, asks/bids as scalar
// summaries) or duplicate metadata for every level. The ladder
// block keeps each level on its own line where it belongs.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// bookLevel is the decoded shape of one entry in asks/bids. The
// API ships levels in two forms across products — object form
// `{"price": 78487.4, "size": 5.134}` (most products today) and
// tuple form `[78487.4, 5.134]` (predictions, historically). We
// decode both via UnmarshalJSON so the renderer doesn't care.
type bookLevel struct {
	Price float64
	Size  float64
}

// UnmarshalJSON accepts the two on-the-wire shapes for a book
// level. Object form is preferred today; tuple form is the legacy
// predictions shape that may still appear via cached responses or
// older endpoints.
func (b *bookLevel) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] == '[' {
		var tuple [2]float64
		if err := json.Unmarshal(data, &tuple); err != nil {
			return err
		}
		b.Price, b.Size = tuple[0], tuple[1]
		return nil
	}
	var obj struct {
		Price float64 `json:"price"`
		Size  float64 `json:"size"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	b.Price, b.Size = obj.Price, obj.Size
	return nil
}

// snapshotDisplayDefault is the per-side level count the inline
// ladder renders when the user didn't pass --depth. The full wire
// payload carries 100 levels per side, which doesn't fit a typical
// terminal scrollback — so the human-facing display caps at 20
// (closest to the spread; deeper levels still scroll into view via
// the shell's pager).
//
// JSON output and WS NDJSON pipelines are NOT affected — those
// always emit whatever depth the wire carries (or whatever
// --depth the user explicitly requested). Agents piping for
// programmatic use still see full data; the default only narrows
// the human-facing table.
//
// When --depth N is explicitly passed, ApplyBookFilter has already
// trimmed asks/bids to N before this renderer runs — so the cap
// here only takes effect on the no-flag case.
const snapshotDisplayDefault = 20

// snapshotRecord is the subset of fields the ladder renderer
// reads. Other fields (instrument_type, currency, etc.) pass
// through to JSON output untouched but aren't displayed in the
// ladder block — there's not enough horizontal space and the
// header line above the ladder already names the venue and
// instrument.
type snapshotRecord struct {
	Exchange       string      `json:"exchange"`
	InstrumentName string      `json:"instrument_name"`
	MarginType     string      `json:"margin_type"`
	Date           string      `json:"date"`
	Microprice     float64     `json:"microprice"`
	Asks           []bookLevel `json:"asks"`
	Bids           []bookLevel `json:"bids"`
}

// renderBookSnapshotTable detects the snapshot book shape inside
// the response envelope and renders one ladder block per record.
// Returns (rendered string, true) on a hit; ("", false) when the
// payload doesn't look like a snapshot — caller falls back to the
// generic table renderer.
//
// Detection rule: envelope `data` is a non-empty array, and the
// first element is an object with both `asks` and `bids` keys.
// Time-series stats orderbook (`bid_liq_10_avg`, etc.) doesn't
// have `asks`/`bids` arrays, so it bypasses this renderer
// naturally.
func (p *Printer) renderBookSnapshotTable(raw []byte) (string, bool) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", false
	}
	// Try array shape first (typical envelope `data: [...]`).
	var arr []json.RawMessage
	if err := json.Unmarshal(env.Data, &arr); err != nil {
		return "", false
	}
	if len(arr) == 0 {
		return "", false
	}
	// Probe the first element for asks/bids keys without committing
	// to a full decode.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(arr[0], &probe); err != nil {
		return "", false
	}
	_, hasAsks := probe["asks"]
	_, hasBids := probe["bids"]
	if !hasAsks || !hasBids {
		return "", false
	}

	var b strings.Builder
	for i, rec := range arr {
		var snap snapshotRecord
		if err := json.Unmarshal(rec, &snap); err != nil {
			continue
		}
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatSnapshotBlock(snap, p.StatsTier))
	}
	return b.String(), true
}

// formatSnapshotBlock renders one ladder block. Layout:
//
//	<exchange · instrument · margin · date>   microprice <px>
//
//	     ask        size
//	   78,488.00    0.002
//	   ...
//	   ─── spread <delta> ───
//	   78,487.30    4.650
//	   ...
//
// Asks are printed worst-price first (top), best-price last (just
// above the spread separator) — matches the dash book ladder so
// muscle memory carries between TUI and table modes. Bids best-
// first below the spread, worst-last.
func formatSnapshotBlock(snap snapshotRecord, userDepth int) string {
	// Apply the display-time cap when the user did NOT pass --depth.
	// userDepth comes from the printer's StatsTier (which is set
	// from BookFilterFlags.Depth in cmdutil.runAndPrintWith). Zero
	// means "user didn't ask" → cap to snapshotDisplayDefault so a
	// table-mode `orderbook-raw` fits one screen. Non-zero means the
	// user requested a specific depth — ApplyBookFilter has already
	// trimmed the wire payload to that count, so leave the slice
	// untouched here.
	//
	// JSON / CSV / WS NDJSON paths bypass this renderer entirely, so
	// agents always get whatever depth was on the wire.
	if userDepth == 0 {
		if len(snap.Asks) > snapshotDisplayDefault {
			snap.Asks = snap.Asks[:snapshotDisplayDefault]
		}
		if len(snap.Bids) > snapshotDisplayDefault {
			snap.Bids = snap.Bids[:snapshotDisplayDefault]
		}
	}

	var b strings.Builder

	// Header line — venue identity + microprice, in brand grey.
	header := snap.Exchange
	if snap.InstrumentName != "" {
		header += " · " + snap.InstrumentName
	}
	if snap.MarginType != "" {
		header += " · " + snap.MarginType
	}
	if snap.Date != "" {
		header += " · " + snap.Date
	}
	b.WriteString(BrandGreyMid + header + Reset)
	if snap.Microprice > 0 {
		b.WriteString("   ")
		b.WriteString(BrandGreyMid + "microprice" + Reset + " ")
		b.WriteString(FormatBookPrice(snap.Microprice))
	}
	b.WriteString("\n\n")

	// Pre-compute per-side cumulative liquidity. Same convention as
	// the WS book ladder and the dashboard ladder: cumulative grows
	// as the user reads outward from the spread. asks[0] is best-ask
	// (lowest price) so askCum[i] = sum of asks[0..i] inclusive;
	// bids[0] is best-bid so bidCum[i] = sum of bids[0..i] inclusive.
	// Reading the cum column tells the trader "to fill at this
	// price-or-better, you walk through this much liquidity".
	askCum := make([]float64, len(snap.Asks))
	{
		acc := 0.0
		for i, l := range snap.Asks {
			acc += l.Size
			askCum[i] = acc
		}
	}
	bidCum := make([]float64, len(snap.Bids))
	{
		acc := 0.0
		for i, l := range snap.Bids {
			acc += l.Size
			bidCum[i] = acc
		}
	}

	// Column header for the ladder block.
	b.WriteString(fmt.Sprintf("       %-12s %-10s %s\n", "ask", "size", "cum"))

	// Asks: worst → best (top → bottom). API delivers best-first
	// (lowest ask first), so we reverse for display.
	for i := len(snap.Asks) - 1; i >= 0; i-- {
		l := snap.Asks[i]
		b.WriteString(fmt.Sprintf("   %s%-12s%s   %-10s %s%s%s\n",
			Red, FormatBookPrice(l.Price), Reset,
			FormatBookSize(l.Size),
			BrandGreyMid, FormatBookSize(askCum[i]), Reset,
		))
	}

	// Spread separator. Computed as best-ask - best-bid.
	spread := 0.0
	if len(snap.Asks) > 0 && len(snap.Bids) > 0 {
		spread = snap.Asks[0].Price - snap.Bids[0].Price
	}
	b.WriteString(fmt.Sprintf("   %s─── spread %s ───%s\n",
		BrandGreyMid, FormatBookPrice(spread), Reset,
	))

	// Bids: best → worst (top → bottom). API delivers best-first
	// (highest bid first) — display order matches.
	for i, l := range snap.Bids {
		b.WriteString(fmt.Sprintf("   %s%-12s%s   %-10s %s%s%s\n",
			BrandGreen, FormatBookPrice(l.Price), Reset,
			FormatBookSize(l.Size),
			BrandGreyMid, FormatBookSize(bidCum[i]), Reset,
		))
	}

	return b.String()
}
