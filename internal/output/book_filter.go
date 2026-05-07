package output

// Agent-friendly filters for L2 book payloads — shared by every
// command that surfaces a "snapshot" book shape (asks/bids arrays
// + tier liquidity stats). Today: `ws book` (streaming) and `spot
// orderbook-raw` (REST). Future: any new endpoint that returns the
// same canonical book shape gets the flags for free by calling
// AddBookFilterFlags + ApplyBookFilter.
//
// REST/WS feature parity rule (see CLAUDE.md "Feature parity"):
// any flag we add to one transport for a given data shape lands on
// every other transport surfacing the same shape, and the
// implementation lives here so REST and WS literally call the same
// trim function. Agents shouldn't have to learn two flag dialects.
//
// What does NOT belong here:
//   - The historical depth metrics commands (perps/futures/spot
//     orderbook) return time-series of liquidity *stats* — there's
//     no asks/bids array to trim and the tier fields ARE the data,
//     not noise. Wiring AddBookFilterFlags into those would expose
//     a flag that silently does nothing, which is worse than not
//     offering the flag at all.

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// BookFilterFlags is the shared flag bundle for any command that
// emits the snapshot book shape. Wire it in via AddBookFilterFlags
// and apply via ApplyBookFilter so flag names, defaults, help
// strings, and trim semantics stay identical across REST and WS.
type BookFilterFlags struct {
	// Depth, when > 0, trims asks/bids to top-N levels per side
	// before emit. Zero = full book (the wire payload, untouched).
	Depth int
	// Compact, when true, strips the tier-aggregate fields
	// (ask_liquidity_*, bid_liquidity_*, imbalance_*) from the
	// payload, leaving raw asks/bids + microprice + metadata.
	// Microprice is preserved because it's a single scalar that
	// can't be derived from a depth-trimmed book without redoing
	// the size-weighted formula.
	Compact bool
}

// compactStripFields lists the tier-aggregate fields removed when
// --compact is set. Listed explicitly (allow-deny pivot) so future
// fields the API adds pass through by default; opting in to compact
// never silently drops something an agent might depend on.
//
// Two naming conventions in the wild:
//   - WS book stream uses `ask_liquidity_10` / `imbalance_10`.
//   - REST snapshot endpoints (`spot orderbook-raw`) use the same
//     names without underscore variants.
//
// The list covers the canonical names; if a payload uses a
// different naming, --compact is a no-op for those fields, which
// is the safer failure mode.
var compactStripFields = []string{
	"ask_liquidity_10", "ask_liquidity_20", "ask_liquidity_50", "ask_liquidity_100",
	"bid_liquidity_10", "bid_liquidity_20", "bid_liquidity_50", "bid_liquidity_100",
	"imbalance_10", "imbalance_20", "imbalance_50", "imbalance_100",
}

// AddBookFilterFlags registers --depth and --compact on cmd, bound
// to the given BookFilterFlags struct. Call from each command's
// init() function for any command that emits a snapshot book.
//
// Help strings are stable across surfaces so an agent reading
// `--help` for `ws book` and `spot orderbook-raw` sees the same
// vocabulary. Defaults are off so existing pipelines see no
// behaviour change until they opt in.
func AddBookFilterFlags(cmd *cobra.Command, f *BookFilterFlags) {
	cmd.Flags().IntVar(&f.Depth, "depth", 0,
		"On snapshot books (orderbook-raw, ws book): trim asks/bids to top-N levels per side. On stats books (orderbook): pick tier columns to surface (10/20/50/100). 0 = full payload.")
	cmd.Flags().BoolVar(&f.Compact, "compact", false,
		"On snapshot books: strip tier-aggregate fields (ask_liquidity_*, bid_liquidity_*, imbalance_*); preserves asks/bids/microprice/metadata. On stats books: reserved (no-op today).")
}

// Active reports whether either flag is set. Callers gate the
// trim path on this so the no-flag case stays a true zero-cost
// passthrough (no decode/re-encode round-trip).
func (f BookFilterFlags) Active() bool {
	return f.Depth > 0 || f.Compact
}

// Validate rejects obviously-invalid flag combinations early, before
// any HTTP work happens. Same semantics across REST and WS so agents
// see consistent errors regardless of transport.
//
// Negative depth is rejected because there's no useful interpretation
// — neither "top-N levels" nor "tier N" works with N < 0 and silently
// treating it as 0 hides the typo. Zero stays valid (means "full
// payload" — explicit and identical to no-flag).
func (f BookFilterFlags) Validate() error {
	if f.Depth < 0 {
		return fmt.Errorf("--depth must be >= 0; got %d", f.Depth)
	}
	return nil
}

// AllowedDepthTiers is the set of tier values both --depth flags
// accept. Mirrors the wire payload's pre-computed tier columns
// (bid_liq_10/20/50/100, ask_liq_*, imbalance_*) and the
// ladder.NextDepthTier cycle in the TUI. Same vocabulary on both
// shapes:
//   - On orderbook-raw / ws book: --depth N trims asks/bids to
//     top-N levels.
//   - On orderbook (stats): --depth N selects the tier-N columns
//     (bid_liq_<N>, ask_liq_<N>, imbalance_<N>) for the compact
//     table view; non-tier values pass through unchanged in JSON.
//
// Other --depth values (e.g. 25) are accepted on the snapshot
// shape but no-op on the stats shape — there's no tier-25 column
// in the wire payload to surface.
var AllowedDepthTiers = []int{10, 20, 50, 100}

// IsAllowedDepthTier reports whether n matches a wire-payload
// tier. Used by stats commands to decide whether --depth N can
// pick a tier or whether to fall back to the default tier (10).
func IsAllowedDepthTier(n int) bool {
	for _, t := range AllowedDepthTiers {
		if n == t {
			return true
		}
	}
	return false
}

// ApplyBookFilter trims a single book-shaped JSON payload according
// to the flags. Operates on json.RawMessage so it works for both
// the WS event's `.data` and the REST envelope's `.data[i]` —
// either caller hands us the inner payload, gets back the trimmed
// payload, re-marshals around it.
//
// Resilience: any decode error returns the input unchanged. A book
// payload the CLI can't introspect is rare (we already decode it
// elsewhere for the TUI ladder); skipping the trim on a malformed
// payload is safer than corrupting the caller's stream.
//
// Implementation note: we decode into map[string]json.RawMessage
// rather than a typed struct so server-added fields pass through
// untouched. --depth alone never drops a field; --compact only
// drops the explicit list above.
func ApplyBookFilter(payload json.RawMessage, f BookFilterFlags) json.RawMessage {
	if !f.Active() {
		return payload
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return payload
	}

	if f.Depth > 0 {
		if a, ok := fields["asks"]; ok {
			fields["asks"] = trimLevels(a, f.Depth)
		}
		if b, ok := fields["bids"]; ok {
			fields["bids"] = trimLevels(b, f.Depth)
		}
	}

	if f.Compact {
		for _, k := range compactStripFields {
			delete(fields, k)
		}
	}

	out, err := json.Marshal(fields)
	if err != nil {
		return payload
	}
	return out
}

// trimLevels truncates an asks/bids JSON array to the first `depth`
// entries. Returns the input unchanged if it can't be decoded as an
// array — a non-array shape means we don't know how to trim safely
// (e.g. a future schema with named-bucket levels would need
// different handling; we'd rather pass through than mangle).
//
// Each level is held as json.RawMessage so the inner shape (tuple
// vs object form) survives untouched; we only adjust the outer
// slice length.
func trimLevels(raw json.RawMessage, depth int) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var levels []json.RawMessage
	if err := json.Unmarshal(raw, &levels); err != nil {
		return raw
	}
	if len(levels) <= depth {
		return raw
	}
	out, err := json.Marshal(levels[:depth])
	if err != nil {
		return raw
	}
	return out
}
