package panels

// FlowBookPanel state-machine tests.
//
// Verify the lifecycle Codex emphasised in earlier rounds:
//   - Subscriptions(sel) returns the correct channel for the current
//     selection, empty when incomplete.
//   - SelectionChangedMsg clears the cached snapshot.
//   - FeedTickMsg updates the snapshot only when the channel matches.
//   - Stale events from a previous selection are dropped.
//   - Capabilities is the zero value (panel is passive in v0.10.0).

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

// makeBookEvent builds a WS event with a minimal book snapshot
// payload. asks/bids each one level so the JSON is small and the
// rendering tests can assert specific cell contents.
func makeBookEvent(channel string, askPrice, bidPrice float64) dashboard.FeedTickMsg {
	payload := []byte(`{
		"timestamp": 1234567890,
		"exchange": "binance",
		"instrument_name": "BTCUSDT",
		"asks": [[` + ftoa(askPrice) + `, 1.0]],
		"bids": [[` + ftoa(bidPrice) + `, 2.0]]
	}`)
	return dashboard.FeedTickMsg{
		Event: wsclient.Event{
			Channel: channel,
			Data:    json.RawMessage(payload),
		},
	}
}

// ftoa is a tiny float-to-string helper for inline test JSON. Using
// strconv.FormatFloat directly would be more correct but for the
// integer-priced tests below this avoids %f-shaped artifacts.
func ftoa(f float64) string {
	if f == float64(int64(f)) {
		return intToString(int64(f))
	}
	// Fall back to a small fixed-precision form. Test inputs use
	// integer prices so this branch is rarely reached.
	return formatFixed(f)
}

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func formatFixed(f float64) string {
	// Two-decimal formatter without importing strconv to keep
	// the test deps minimal. Truncate, not round — sufficient for
	// the test payloads which use integer prices anyway.
	whole := int64(f)
	frac := int64((f - float64(whole)) * 100)
	if frac < 0 {
		frac = -frac
	}
	wholeS := intToString(whole)
	fracS := intToString(frac)
	if len(fracS) < 2 {
		fracS = "0" + fracS
	}
	return wholeS + "." + fracS
}

// ─── Subscriptions ────────────────────────────────────────────────────────

// TestFlowBookSubscriptionsCompleteSelection: returns the channel
// string built from market/venue/symbol.
func TestFlowBookSubscriptionsCompleteSelection(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	sel := dashboard.Selection{
		Market: "perpetuals",
		Venue:  "binance",
		Symbol: "BTCUSDT",
	}
	got := p.Subscriptions(sel)
	want := "book.perpetuals.binance.BTCUSDT"
	if len(got.Channels) != 1 || got.Channels[0] != want {
		t.Errorf("Subscriptions = %v, want [%q]", got.Channels, want)
	}
}

// TestFlowBookSubscriptionsIncompleteSelection: returns empty when
// any of market/venue/symbol is missing. The kernel won't try to
// subscribe; the panel renders the "no instrument selected"
// placeholder.
func TestFlowBookSubscriptionsIncompleteSelection(t *testing.T) {
	cases := []struct {
		name string
		sel  dashboard.Selection
	}{
		{"empty selection", dashboard.Selection{}},
		{"missing market", dashboard.Selection{Venue: "binance", Symbol: "BTCUSDT"}},
		{"missing venue", dashboard.Selection{Market: "perpetuals", Symbol: "BTCUSDT"}},
		{"missing symbol", dashboard.Selection{Market: "perpetuals", Venue: "binance"}},
		{"only currency", dashboard.Selection{Currency: "BTC"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewFlowBookPanel(dashboard.Selection{})
			got := p.Subscriptions(tc.sel)
			if len(got.Channels) != 0 {
				t.Errorf("incomplete selection produced channels: %v", got.Channels)
			}
		})
	}
}

// ─── SelectionChangedMsg ──────────────────────────────────────────────────

// TestFlowBookSelectionChangedClearsSnapshot: a snapshot for the
// previous instrument must not survive a drill-down to a different
// instrument. Without this, the user briefly sees BTC's last book
// under the ETH header.
func TestFlowBookSelectionChangedClearsSnapshot(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})

	// Set initial selection and seed a snapshot.
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))
	if p.snapshot == nil {
		t.Fatal("setup failed: expected snapshot to be set after matching event")
	}

	// Drill: change selection. Snapshot must clear.
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "deribit", Symbol: "ETH-PERPETUAL"},
	})
	if p.snapshot != nil {
		t.Errorf("snapshot survived selection change: %+v", p.snapshot)
	}
	if got := p.currentChannel(); got != "book.perpetuals.deribit.ETH-PERPETUAL" {
		t.Errorf("currentChannel = %q, want book.perpetuals.deribit.ETH-PERPETUAL", got)
	}
}

// ─── FeedTickMsg routing ─────────────────────────────────────────────────

// TestFlowBookFeedTickMatchingChannel: an event whose channel
// matches the current selection updates the snapshot.
func TestFlowBookFeedTickMatchingChannel(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})

	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))

	if p.snapshot == nil {
		t.Fatal("matching event should populate snapshot")
	}
	if len(p.snapshot.Asks) != 1 || p.snapshot.Asks[0].Price != 78500 {
		t.Errorf("asks = %+v, want [{78500, 1}]", p.snapshot.Asks)
	}
	if len(p.snapshot.Bids) != 1 || p.snapshot.Bids[0].Price != 78400 {
		t.Errorf("bids = %+v, want [{78400, 2}]", p.snapshot.Bids)
	}
}

// TestFlowBookFeedTickStaleEventDropped: an event whose channel
// doesn't match the current selection (in-flight from a previous
// subscription the gateway hasn't unsubscribed yet) is dropped.
// This is the "stale event filter" — without it, drilling
// BTC→ETH would briefly render BTC ticks under the ETH stats bar.
func TestFlowBookFeedTickStaleEventDropped(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})

	// Stale event: channel doesn't match the current selection.
	p.Update(makeBookEvent("book.perpetuals.deribit.BTC-PERPETUAL", 78500, 78400))

	if p.snapshot != nil {
		t.Errorf("stale event populated snapshot: %+v", p.snapshot)
	}
}

// TestFlowBookSubscriptionsSyncsSelection: a panel constructed with
// an empty Selection but whose kernel-passed sel populates
// market/venue/symbol must accept ticks for that channel without
// waiting for a SelectionChangedMsg. The router calls Subscriptions
// on init with the kernel's selection; the panel's Update filter
// must agree.
//
// Codex round-2 caught this: prior code subscribed correctly from
// `sel` but kept filtering against the empty `p.selection`, so
// every tick was dropped silently.
func TestFlowBookSubscriptionsSyncsSelection(t *testing.T) {
	// Constructor with empty selection — simulates Step 7 wiring
	// where FlowPanel may build the panel before knowing the
	// initial selection.
	p := NewFlowBookPanel(dashboard.Selection{})

	// Kernel's first Subscriptions call carries the populated
	// selection from root config.
	got := p.Subscriptions(dashboard.Selection{
		Market: "perpetuals",
		Venue:  "binance",
		Symbol: "BTCUSDT",
	})
	if len(got.Channels) != 1 || got.Channels[0] != "book.perpetuals.binance.BTCUSDT" {
		t.Fatalf("Subscriptions = %v, want [book.perpetuals.binance.BTCUSDT]", got.Channels)
	}

	// First matching tick (no SelectionChangedMsg yet) must populate
	// the snapshot. Pre-fix this dropped silently.
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))
	if p.snapshot == nil {
		t.Fatal("matching tick after Subscriptions was dropped; selection sync broken")
	}
	if p.snapshot.Asks[0].Price != 78500 {
		t.Errorf("asks[0].Price = %v, want 78500", p.snapshot.Asks[0].Price)
	}
}

// TestFlowBookSubscriptionsClearsSnapshotOnChannelChange: when the
// kernel's sel differs from the panel's current selection at the
// channel level, the snapshot is invalidated to match what
// SelectionChangedMsg would do. Otherwise a panel that briefly
// observes a different sel through Subscriptions would render
// stale data.
func TestFlowBookSubscriptionsClearsSnapshotOnChannelChange(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{
		Market: "perpetuals",
		Venue:  "binance",
		Symbol: "BTCUSDT",
	})
	// Seed a snapshot for the original selection.
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))
	if p.snapshot == nil {
		t.Fatal("setup failed: expected snapshot after seed event")
	}

	// Kernel's sel switches to a different channel via Subscriptions
	// (rather than via SelectionChangedMsg). Snapshot must clear.
	p.Subscriptions(dashboard.Selection{
		Market: "perpetuals",
		Venue:  "deribit",
		Symbol: "ETH-PERPETUAL",
	})
	if p.snapshot != nil {
		t.Errorf("snapshot survived Subscriptions-driven channel change: %+v", p.snapshot)
	}
}

// TestFlowBookSubscriptionsNoOpOnEqualChannel: Subscriptions called
// with a Selection whose channel matches the current one must NOT
// clear the snapshot. Otherwise repeated Subscriptions calls
// (kernel's refreshSubscriptions on tick churn) would erase the
// rendered book between frames.
func TestFlowBookSubscriptionsNoOpOnEqualChannel(t *testing.T) {
	sel := dashboard.Selection{
		Market: "perpetuals",
		Venue:  "binance",
		Symbol: "BTCUSDT",
	}
	p := NewFlowBookPanel(sel)
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))

	// Kernel re-asks for subs with the same selection. Snapshot must
	// survive.
	p.Subscriptions(sel)
	if p.snapshot == nil {
		t.Errorf("snapshot was cleared by no-op Subscriptions call")
	}
}

// TestFlowBookConstructorSelectionDrivesFiltering: a panel built
// with an initial Selection accepts FeedTickMsgs for that
// selection's channel WITHOUT needing a SelectionChangedMsg first.
// Codex's High-priority round-1 finding: the previous design
// stored channel on SelectionChangedMsg only, so events arriving
// at startup (before the kernel routed the first selection
// message) were dropped silently.
func TestFlowBookConstructorSelectionDrivesFiltering(t *testing.T) {
	initial := dashboard.Selection{
		Market: "perpetuals",
		Venue:  "binance",
		Symbol: "BTCUSDT",
	}
	p := NewFlowBookPanel(initial)

	// No SelectionChangedMsg yet. The next event should still land
	// in the snapshot because the constructor already installed
	// the selection.
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))

	if p.snapshot == nil {
		t.Fatal("event with constructor-installed selection was dropped; should have populated snapshot")
	}
	if p.snapshot.Asks[0].Price != 78500 {
		t.Errorf("snapshot asks[0].Price = %v, want 78500", p.snapshot.Asks[0].Price)
	}
}

// TestFlowBookFeedTickWithoutSelection: events arriving before any
// selection has been installed are dropped (no channel to match
// against).
func TestFlowBookFeedTickWithoutSelection(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))
	if p.snapshot != nil {
		t.Errorf("event without selection populated snapshot: %+v", p.snapshot)
	}
}

// TestFlowBookFeedTickMalformedPayload: a payload that fails to
// unmarshal does NOT clear the existing snapshot — better to keep
// stale data than blank the panel on a transient decode error.
func TestFlowBookFeedTickMalformedPayload(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))
	if p.snapshot == nil {
		t.Fatal("setup failed: expected snapshot")
	}

	// Malformed payload — unparseable JSON.
	p.Update(dashboard.FeedTickMsg{
		Event: wsclient.Event{
			Channel: "book.perpetuals.binance.BTCUSDT",
			Data:    json.RawMessage(`{not valid json`),
		},
	})

	if p.snapshot == nil {
		t.Errorf("malformed payload blanked the snapshot; should have been kept")
	}
}

// ─── View ────────────────────────────────────────────────────────────────

// TestFlowBookViewWaitingPlaceholder: with no snapshot, View
// renders a "waiting for book…" centred message.
func TestFlowBookViewWaitingPlaceholder(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})

	view := p.View(40, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "waiting for book") {
		t.Errorf("expected waiting placeholder, got:\n%s", view)
	}
}

// TestFlowBookViewNoSelectionPlaceholder: with no selection at all,
// View renders "no instrument selected" rather than "waiting" —
// helps the user distinguish "before drill" from "during drill".
func TestFlowBookViewNoSelectionPlaceholder(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	view := p.View(40, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "no instrument selected") {
		t.Errorf("expected no-selection placeholder, got:\n%s", view)
	}
}

// TestFlowBookViewTinyRendersCompactBook: very small panes render a
// compact bid/ask view rather than refusing with "(too small)".
func TestFlowBookViewTinyRendersCompactBook(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))

	view := p.View(10, 4, dashboard.PanelContext{}) // below 29x5 minimum
	if strings.Contains(view, "too small") {
		t.Errorf("unexpected too-small placeholder:\n%s", view)
	}
	if !strings.Contains(view, "78,400") {
		t.Errorf("expected compact bid price, got:\n%s", view)
	}
}

// TestFlowBookViewWidthBelowMinUsesCompactRows: widths in the
// 16-28 range use the compact bid/ask renderer and still obey the
// panel's width budget.
func TestFlowBookViewWidthBelowMinUsesCompactRows(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))

	for _, w := range []int{16, 20, 28} {
		view := p.View(w, 11, dashboard.PanelContext{})
		if strings.Contains(view, "too small") {
			t.Errorf("width %d: unexpected too-small placeholder:\n%s", w, view)
		}
		for i, line := range strings.Split(view, "\n") {
			if got := output.VisibleWidth(line); got != w {
				t.Fatalf("width %d line %d visible width = %d, want %d\n%s", w, i, got, w, view)
			}
		}
	}
	// width 29 is the floor and should render the ladder.
	view := p.View(29, 11, dashboard.PanelContext{})
	if strings.Contains(view, "too small") {
		t.Errorf("width 29 (floor) should render ladder, got placeholder:\n%s", view)
	}
}

// TestFlowBookViewRendersLadderShape: a populated snapshot renders
// asks above the spread separator and bids below.
//
// Renders at width 100 — the canonical ladder has seven columns
// (CUM BID / BID SZ / bid bar / PRICE / ask bar / ASK SZ /
// CUM ASK) and full width is required to show every column
// without the per-line width clamp truncating prices off the
// right edge. At narrow widths (< ~80 cells) the rightmost
// columns are clipped intentionally — that's the production
// behavior in tight pane allocations and not what this shape
// test is verifying.
func TestFlowBookViewRendersLadderShape(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))

	view := p.View(100, 11, dashboard.PanelContext{})
	lines := strings.Split(view, "\n")

	// Find the spread separator line.
	sepIdx := -1
	for i, line := range lines {
		// Separator contains the "──" glyph runs.
		if strings.Contains(line, "──") {
			sepIdx = i
			break
		}
	}
	if sepIdx == -1 {
		t.Fatalf("spread separator missing from view:\n%s", view)
	}

	// Asks above the separator should reference the ask price.
	asksBlock := strings.Join(lines[:sepIdx], "\n")
	if !strings.Contains(asksBlock, "78,500") && !strings.Contains(asksBlock, "78500") {
		t.Errorf("ask price 78500 missing above separator:\n%s", asksBlock)
	}

	// Bids below the separator should reference the bid price.
	bidsBlock := strings.Join(lines[sepIdx+1:], "\n")
	if !strings.Contains(bidsBlock, "78,400") && !strings.Contains(bidsBlock, "78400") {
		t.Errorf("bid price 78400 missing below separator:\n%s", bidsBlock)
	}
}

// ─── Capabilities ────────────────────────────────────────────────────────

// TestFlowBookCapabilitiesEmpty: the panel is passive in v0.10.0 —
// no key capabilities. Composite contract: declaring caps that
// the panel can't honour (because the composite uses
// activeChildNone) puts dead keys on the footer.
//
// As of v0.10.0 polish round, the book pane consumes
// depth/group/recenter — the parent composite (FlowPanel detail
// mode) routes detail-mode keys to it explicitly. So
// Capabilities is no longer zero; it advertises exactly the
// three actions applyKey responds to.
func TestFlowBookCapabilitiesDepthGroupRecenter(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	caps := p.Capabilities()
	if !caps.ListNav {
		t.Errorf("ListNav should be true (panel handles viewport scroll)")
	}
	if !caps.DepthCycle {
		t.Errorf("DepthCycle should be true (panel handles `d`)")
	}
	if !caps.Group {
		t.Errorf("Group should be true (panel handles `+/-`)")
	}
	if !caps.Recenter {
		t.Errorf("Recenter should be true (panel handles `c`)")
	}
	// Negative cases — keys the panel does NOT consume:
	if caps.Drill || caps.Back {
		t.Errorf("Drill/Back must remain false: %+v", caps)
	}
}

func TestFlowBookViewportKeysMoveAndRecenter(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.viewHeight = 24

	if !p.applyKey(keymap.ActDown) {
		t.Fatal("ActDown should be consumed")
	}
	if p.viewport.Offset >= 0 {
		t.Fatalf("ActDown should scroll toward bids, offset=%d", p.viewport.Offset)
	}
	if !p.applyKey(keymap.ActUp) {
		t.Fatal("ActUp should be consumed")
	}
	if p.viewport.Offset != 0 {
		t.Fatalf("ActUp should return to center, offset=%d", p.viewport.Offset)
	}
	p.applyKey(keymap.ActPageUp)
	if p.viewport.Offset <= 0 {
		t.Fatalf("PageUp should scroll toward asks, offset=%d", p.viewport.Offset)
	}
	p.applyKey(keymap.ActRecenter)
	if p.viewport.Offset != 0 {
		t.Fatalf("Recenter should reset offset, got %d", p.viewport.Offset)
	}
	p.applyKey(keymap.ActBottom)
	if p.viewport.Offset >= 0 {
		t.Fatalf("Bottom should snap toward bids, offset=%d", p.viewport.Offset)
	}
}

func TestFlowBookViewportResetsOnDepthGroupAndSelection(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.viewHeight = 24
	p.applyKey(keymap.ActPageDown)
	if p.viewport.Offset == 0 {
		t.Fatal("setup failed: expected non-zero offset")
	}
	p.applyKey(keymap.ActDepthCycle)
	if p.viewport.Offset != 0 {
		t.Fatalf("depth cycle should recenter viewport, got %d", p.viewport.Offset)
	}
	p.applyKey(keymap.ActPageDown)
	p.applyKey(keymap.ActGroupUp)
	if p.viewport.Offset != 0 {
		t.Fatalf("group change should recenter viewport, got %d", p.viewport.Offset)
	}
	p.applyKey(keymap.ActPageDown)
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "ETHUSDT"},
	})
	if p.viewport.Offset != 0 {
		t.Fatalf("selection change should recenter viewport, got %d", p.viewport.Offset)
	}
}

// TestFlowBookTitleEmpty: parent composites have no chrome; the
// panel itself returns "" for Title — instrument identity lives
// in the flow stats bar above.
func TestFlowBookTitleEmpty(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	if got := p.Title(); got != "" {
		t.Errorf("Title = %q, want empty", got)
	}
}

// TestFlowBookInitNoCmd: the panel is reactive only — no startup
// commands.
func TestFlowBookInitNoCmd(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	if got := p.Init(); got != nil {
		t.Errorf("Init returned non-nil cmd; panel should be reactive only")
	}
}

// TestFlowBookIgnoresOtherMessages: messages other than
// SelectionChangedMsg / FeedTickMsg don't touch state.
func TestFlowBookIgnoresOtherMessages(t *testing.T) {
	p := NewFlowBookPanel(dashboard.Selection{})
	p.Update(dashboard.SelectionChangedMsg{
		New: dashboard.Selection{Market: "perpetuals", Venue: "binance", Symbol: "BTCUSDT"},
	})
	p.Update(makeBookEvent("book.perpetuals.binance.BTCUSDT", 78500, 78400))
	beforeSnap := p.snapshot

	// A keyboard message would be routed by the kernel, but if it
	// arrived here it shouldn't mutate state.
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if p.snapshot != beforeSnap {
		t.Errorf("KeyMsg changed snapshot reference; panel should be passive")
	}
}
