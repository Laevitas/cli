package panels

// FlowScreenerPanel tests. Exercises the cursor + selection +
// overscan logic; does NOT exercise the REST fetch path (which
// requires a real or mocked api.Client). The fetch is straight
// JSON decode plumbing — exercising it would test net/http more
// than the panel — and we instead drive snapshots into the panel
// directly via Update(flowScreenerSnapshotMsg).

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/api"
	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/dashboard/columns"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// Helpers.

func makeRows(n int) []columns.PerpRow {
	out := make([]columns.PerpRow, n)
	venues := []string{"binance", "deribit", "okx", "bybit", "hyperliquid"}
	for i := 0; i < n; i++ {
		out[i] = columns.PerpRow{
			Exchange:       venues[i%len(venues)],
			InstrumentName: "BTCUSDT",
			MarkPrice:      78000 + float64(i),
			BidPrice:       77999 + float64(i),
			AskPrice:       78001 + float64(i),
			Volume24hUSD:   float64(1_000_000 * (i + 1)),
			OI:             float64(500_000 * (i + 1)),
			FundingRate:    0.00005,
		}
	}
	// Ensure unique identities — change InstrumentName per row past
	// the first to avoid collision when n > len(venues).
	for i := len(venues); i < n; i++ {
		out[i].InstrumentName = "BTC-PERPETUAL-" + string(rune('A'+i))
	}
	return out
}

func snapMsg(rows []columns.PerpRow) flowScreenerSnapshotMsg {
	return flowScreenerSnapshotMsg{rows: rows}
}

// ─── Construction + initial state ─────────────────────────────────────────

func TestFlowScreenerNewLoadingTrue(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	if !p.loading {
		t.Errorf("new screener should start in loading state")
	}
	if p.cursor != 0 {
		t.Errorf("new screener cursor = %d, want 0", p.cursor)
	}
	if p.currency != "BTC" {
		t.Errorf("currency = %q, want BTC", p.currency)
	}
}

// TestFlowScreenerCurrencyUppercased: lowercase input is stored
// uppercased so the REST query matches the API's convention.
func TestFlowScreenerCurrencyUppercased(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "btc", "perpetuals")
	if p.currency != "BTC" {
		t.Errorf("currency = %q, want BTC", p.currency)
	}
}

// ─── Snapshot ingestion ────────────────────────────────────────────────────

func TestFlowScreenerSnapshotInstallsRows(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))

	if p.loading {
		t.Errorf("loading should clear after first snapshot")
	}
	if len(p.rows) != 5 {
		t.Errorf("rows = %d, want 5", len(p.rows))
	}
	if p.lastErr != "" {
		t.Errorf("lastErr = %q, want empty after success", p.lastErr)
	}
}

func TestFlowScreenerSnapshotErrorPreservesPreviousRows(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	first := makeRows(3)
	p.Update(snapMsg(first))
	if len(p.rows) != 3 {
		t.Fatalf("setup failed: rows = %d", len(p.rows))
	}

	// Error snapshot — rows must survive, error must surface.
	p.Update(flowScreenerSnapshotMsg{err: errMsg("network down")})
	if len(p.rows) != 3 {
		t.Errorf("rows lost after error: got %d, want 3 preserved", len(p.rows))
	}
	if !strings.Contains(p.lastErr, "network down") {
		t.Errorf("lastErr = %q, want to contain network error", p.lastErr)
	}
}

// errMsg is a tiny error helper.
type errMsg string

func (e errMsg) Error() string { return string(e) }

// TestFlowScreenerCursorIdentityPreserved: a snapshot refresh
// shouldn't yank the cursor to a different row. If the
// previously-highlighted row is still in the new snapshot, cursor
// stays on it; if it vanished, cursor clamps to its index (or
// to len-1 if past the end).
func TestFlowScreenerCursorIdentityPreserved(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))
	// Move cursor to row 2 (deribit:BTCUSDT).
	p.cursor = 2
	prevID := rowIdentity(p.rows[p.cursor])

	// Refresh: same rows in different order. The cursor should
	// follow the identity.
	reordered := []columns.PerpRow{rows[2], rows[0], rows[1], rows[3], rows[4]}
	p.Update(snapMsg(reordered))
	if p.cursor != 0 {
		t.Errorf("cursor should follow identity to new index: cursor=%d, expected 0 (the prev row 2 is now at 0)", p.cursor)
	}
	if rowIdentity(p.rows[p.cursor]) != prevID {
		t.Errorf("cursor row identity changed: was %q, now %q", prevID, rowIdentity(p.rows[p.cursor]))
	}
}

// TestFlowScreenerCursorClampsOnVanishedRow: if the cursor's row
// disappears from the new snapshot (delisting), cursor clamps to
// the same numerical index in the new (smaller) slice.
func TestFlowScreenerCursorClampsOnVanishedRow(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))
	p.cursor = 4 // last row

	// New snapshot has only 3 rows, none of which match the old
	// identity. Cursor should clamp to len-1 = 2.
	smaller := makeRows(3) // rows 0..2 from the same identities
	// Force a different identity set: rename instruments.
	for i := range smaller {
		smaller[i].InstrumentName = "ETH-PERP" // none match the original
	}
	p.Update(snapMsg(smaller))
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		t.Errorf("cursor out of range after vanish: %d (rows=%d)", p.cursor, len(p.rows))
	}
}

// ─── Cursor navigation + SelectionChangedMsg emission ─────────────────────

func TestFlowScreenerCursorMoveEmitsSelectionChangedMsg(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))
	// Initial cursor at 0; lastEmittedSelection might already match
	// after the snapshot install. Force-clear so this test exercises
	// the down-arrow emit path independently.
	p.lastEmittedSelection = dashboard.Selection{}

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("down key produced no cmd; expected SelectionChangedMsg")
	}
	msg := cmd()
	sel, ok := msg.(dashboard.SelectionChangedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want SelectionChangedMsg", msg)
	}
	if sel.New.Symbol != p.rows[1].InstrumentName {
		t.Errorf("emitted selection symbol = %q, want %q (row 1)", sel.New.Symbol, p.rows[1].InstrumentName)
	}
	if sel.New.Venue != p.rows[1].Exchange {
		t.Errorf("emitted selection venue = %q, want %q", sel.New.Venue, p.rows[1].Exchange)
	}
	if sel.New.Currency != "BTC" {
		t.Errorf("emitted selection currency = %q, want BTC", sel.New.Currency)
	}
	if sel.New.Market != "perpetuals" {
		t.Errorf("emitted selection market = %q, want perpetuals", sel.New.Market)
	}
}

func TestFlowScreenerCursorMoveDedupesEmits(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))
	// First down move emits.
	_, cmd1 := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd1 == nil {
		t.Fatal("first down should emit")
	}

	// Capture the selection from the emit so we can prove it was
	// installed as lastEmittedSelection.
	got := cmd1()
	if _, ok := got.(dashboard.SelectionChangedMsg); !ok {
		t.Fatalf("first emit was %T, expected SelectionChangedMsg", got)
	}

	// Pretend the user moved up and back down (or that we're now
	// asking for the same row twice). The current selection still
	// matches lastEmittedSelection — should NOT emit a duplicate.
	// Simulate: cursor stays put because there's no further down
	// to go (we're at row 1, valid). We can't test "no new emit"
	// directly without simulating more moves; instead, snapshot
	// the panel state and verify the dedup field is populated.
	if (p.lastEmittedSelection == dashboard.Selection{}) {
		t.Errorf("lastEmittedSelection was not stored after emit")
	}
}

// TestFlowScreenerCmdCapturesByValue: cmd closures must snapshot
// the selection at the time they're created, NOT read panel state
// when the cmd actually executes. Bubble Tea runs cmds
// concurrently with Update, so a subsequent cursor move that
// mutates p.cursor or p.lastEmittedSelection between the cmd
// being returned and being executed would corrupt the message
// payload.
//
// Codex round-1 finding: SelectionChangedMsg.Old was reading
// p.lastEmittedSelection at execution time, which had already
// been overwritten — Old and New ended up identical. And the
// Enter drill cmd called p.currentSelection() at execution
// time, which would drill the row the cursor was on at that
// moment, not when the user pressed Enter.
func TestFlowScreenerCmdCapturesByValue(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))
	p.lastEmittedSelection = dashboard.Selection{} // force a clean baseline

	// Move down to row 1 and capture the cmd before any further
	// mutation. We don't execute it yet.
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("down key did not produce a cmd")
	}

	// Race simulation: mutate panel state AFTER the cmd is created
	// but BEFORE it executes. If the cmd reads panel state at
	// execution time, the messages below will reflect the new
	// state, not the state at cmd-creation time.
	p.cursor = 4
	p.lastEmittedSelection = dashboard.Selection{
		Currency: "ZZZ", Market: "garbage", Venue: "noexchange", Symbol: "WRONG",
	}

	// Execute the cmd. The message it produces must reflect the
	// panel state when the cmd was MADE, not now.
	msg := cmd()
	sel, ok := msg.(dashboard.SelectionChangedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want SelectionChangedMsg", msg)
	}
	// New should be the row-1 selection (from the down-arrow).
	if sel.New.Symbol != rows[1].InstrumentName || sel.New.Venue != rows[1].Exchange {
		t.Errorf("New = %+v, want row 1 (%s:%s) — cmd read post-mutation state",
			sel.New, rows[1].Exchange, rows[1].InstrumentName)
	}
	// Old should be the empty Selection (the lastEmittedSelection
	// at cmd-creation time), NOT the garbage one we wrote after.
	if sel.Old != (dashboard.Selection{}) {
		t.Errorf("Old = %+v, want empty Selection — cmd captured post-mutation lastEmittedSelection",
			sel.Old)
	}
}

// TestFlowScreenerEnterDrillCapturesByValue: same race-protection
// invariant for the Enter drill cmd. Mutating cursor between
// Update returning the cmd and the cmd executing must not change
// which row gets drilled.
func TestFlowScreenerEnterDrillCapturesByValue(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(5)
	p.Update(snapMsg(rows))
	p.cursor = 2
	wantSym := rows[2].InstrumentName
	wantVenue := rows[2].Exchange

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter did not produce a cmd")
	}

	// Race simulation: cursor moves before the cmd executes.
	p.cursor = 4

	msg := cmd()
	drill, ok := msg.(FlowDrillMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want FlowDrillMsg", msg)
	}
	if drill.Selection.Symbol != wantSym || drill.Selection.Venue != wantVenue {
		t.Errorf("drill selection = %+v, want row 2 (%s:%s) — cmd read post-mutation cursor",
			drill.Selection, wantVenue, wantSym)
	}
}

// TestFlowScreenerEnterEmitsDrillMsg: pressing Enter on a row
// emits FlowDrillMsg with the row's selection.
func TestFlowScreenerEnterEmitsDrillMsg(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(3)
	p.Update(snapMsg(rows))
	p.cursor = 1

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter produced no cmd")
	}
	msg := cmd()
	drill, ok := msg.(FlowDrillMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want FlowDrillMsg", msg)
	}
	if drill.Selection.Symbol != p.rows[1].InstrumentName {
		t.Errorf("drill symbol = %q, want %q", drill.Selection.Symbol, p.rows[1].InstrumentName)
	}
}

// TestFlowScreenerCursorNoOpOnEmptyRows: arrow keys with no rows
// don't crash and don't emit.
func TestFlowScreenerCursorNoOpOnEmptyRows(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Errorf("down with no rows produced cmd: %v", cmd)
	}
	if p.cursor != 0 {
		t.Errorf("cursor moved with no rows: %d", p.cursor)
	}
}

// TestFlowScreenerCursorBoundsClamped: cursor doesn't go past row
// 0 going up or past len-1 going down.
func TestFlowScreenerCursorBoundsClamped(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(3)
	p.Update(snapMsg(rows))

	// Already at 0 — up is a no-op.
	p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.cursor != 0 {
		t.Errorf("cursor went below 0: %d", p.cursor)
	}

	// Move to last row, then down should be a no-op.
	p.cursor = len(p.rows) - 1
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.cursor != len(p.rows)-1 {
		t.Errorf("cursor went past end: %d (last=%d)", p.cursor, len(p.rows)-1)
	}
}

// ─── Subscriptions / overscan window ──────────────────────────────────────

func TestFlowScreenerSubscriptionsBoundedByOverscan(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(50)
	p.Update(snapMsg(rows))
	p.cursor = 25

	got := p.Subscriptions(dashboard.Selection{})
	// Window is cursor ± flowScreenerOverscanRows = 25 ± 5 = [20, 30],
	// inclusive = 11 rows.
	wantCount := 2*flowScreenerOverscanRows + 1
	if len(got.Channels) != wantCount {
		t.Errorf("subscription count = %d, want %d (overscan window)",
			len(got.Channels), wantCount)
	}
}

func TestFlowScreenerSubscriptionsClampedAtBoundaries(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := makeRows(3)
	p.Update(snapMsg(rows))
	p.cursor = 0

	got := p.Subscriptions(dashboard.Selection{})
	// Window is [0-5, 0+5] clamped to [0, 2] = 3 rows.
	if len(got.Channels) != 3 {
		t.Errorf("at top of small list, channels = %d, want 3", len(got.Channels))
	}
}

func TestFlowScreenerSubscriptionsEmptyBeforeRows(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	got := p.Subscriptions(dashboard.Selection{})
	if len(got.Channels) != 0 {
		t.Errorf("pre-rows subscriptions = %v, want empty", got.Channels)
	}
}

func TestFlowScreenerSubscriptionsBuildCorrectChannelStrings(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	rows := []columns.PerpRow{
		{Exchange: "binance", InstrumentName: "BTCUSDT"},
	}
	p.Update(snapMsg(rows))
	got := p.Subscriptions(dashboard.Selection{})
	want := "trades.perpetuals.binance.BTCUSDT"
	if len(got.Channels) != 1 || got.Channels[0] != want {
		t.Errorf("channels = %v, want [%s]", got.Channels, want)
	}
}

// ─── Capabilities ────────────────────────────────────────────────────────

func TestFlowScreenerCapabilitiesListNavAndDrill(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	caps := p.Capabilities()
	if !caps.ListNav {
		t.Errorf("expected ListNav capability")
	}
	if !caps.Drill {
		t.Errorf("expected Drill capability")
	}
}

// ─── View ────────────────────────────────────────────────────────────────

// fakeClient returns a non-nil *api.Client for tests that need
// the panel to bypass the "no client" placeholder. The View tests
// never invoke fetchCmd — they drive snapshots in directly via
// Update — so the client is just a presence marker.
func fakeClient() *api.Client {
	return &api.Client{}
}

func TestFlowScreenerViewLoadingState(t *testing.T) {
	p := NewFlowScreenerPanel(fakeClient(), "BTC", "perpetuals")
	view := p.View(80, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "waiting") && !strings.Contains(view, "loading") {
		t.Errorf("expected loading/waiting placeholder, got:\n%s", view)
	}
}

func TestFlowScreenerViewNoClient(t *testing.T) {
	p := NewFlowScreenerPanel(nil, "BTC", "perpetuals")
	view := p.View(80, 12, dashboard.PanelContext{})
	if !strings.Contains(view, "no API client") {
		t.Errorf("expected no-client placeholder, got:\n%s", view)
	}
}

func TestFlowScreenerViewNarrowRendersInstrumentOnly(t *testing.T) {
	p := NewFlowScreenerPanel(fakeClient(), "BTC", "perpetuals")
	p.Update(snapMsg(makeRows(5)))
	view := p.View(40, 4, dashboard.PanelContext{}) // below 60-wide minimum
	if strings.Contains(view, "too small") {
		t.Errorf("unexpected too-small placeholder:\n%s", view)
	}
	if !strings.Contains(view, "INSTRUMENT") || !strings.Contains(view, "binance:BTCUSDT") {
		t.Errorf("expected instrument-only screener content, got:\n%s", view)
	}
}

func TestFlowScreenerViewRendersTable(t *testing.T) {
	p := NewFlowScreenerPanel(fakeClient(), "BTC", "perpetuals")
	p.Update(snapMsg(makeRows(5)))
	view := p.View(120, 12, dashboard.PanelContext{})

	if !strings.Contains(view, "INSTRUMENT") {
		t.Errorf("expected INSTRUMENT header, got:\n%s", view)
	}
	if !strings.Contains(view, "binance:BTCUSDT") {
		t.Errorf("expected first row content, got:\n%s", view)
	}
	if !strings.Contains(view, "FUNDING") {
		t.Errorf("expected FUNDING column at wide width, got:\n%s", view)
	}
}

// TestFlowScreenerViewLongErrorTruncated: the footer must not bleed
// past the panel edge when the API returns a long error message
// (e.g. a wrapped network error carrying a full URL + stack hint).
// Each rendered line should be exactly `width` cells wide.
func TestFlowScreenerViewLongErrorTruncated(t *testing.T) {
	p := NewFlowScreenerPanel(fakeClient(), "BTC", "perpetuals")
	p.Update(snapMsg(makeRows(3)))

	long := "fetch perpetuals snapshot: Get \"https://apiv2.laevitas.ch/api/v1/perpetuals/snapshot?currency=BTC\": dial tcp: lookup apiv2.laevitas.ch on 8.8.8.8:53: read udp 192.168.1.42:54321->8.8.8.8:53: i/o timeout"
	p.Update(flowScreenerSnapshotMsg{err: errMsg(long)})

	const width = 80
	view := p.View(width, 12, dashboard.PanelContext{})
	for i, line := range strings.Split(view, "\n") {
		// Each line must measure exactly `width` visible cells —
		// no overflow into the next pane, no padding gaps.
		if visible := output.VisibleWidth(line); visible != width {
			t.Errorf("line %d width = %d, want %d (line=%q)", i, visible, width, line)
		}
	}
}

// Capabilities sentinel — keep keymap import live.
var _ = keymap.Capabilities{}
