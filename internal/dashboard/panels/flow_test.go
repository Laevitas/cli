package panels

// FlowPanel tests. Exercises the mode-owner contract: drill flips
// the mode, Esc flips it back, key routing follows the mode,
// non-key messages broadcast to both children, subscriptions and
// capabilities are mode-gated.
//
// We use the real screener + detail composite (not mocks) because
// FlowPanel's wiring contract IS its observable behaviour — a mock
// child wouldn't catch the kinds of bugs that this layer is
// responsible for (e.g. forgetting to install the selection on
// detail-children, broadcasting the wrong message type).

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
)

// newFlowFixture returns a FlowPanel + its screener already
// populated with N rows so the tests don't have to repeat the
// snapshot install step.
func newFlowFixture(t *testing.T, rows int) (*FlowPanel, *FlowScreenerPanel) {
	t.Helper()
	screener := newTestFlowScreenerPanel(fakeClient(), "BTC", "perpetuals")
	screener.Update(snapMsg(makeRows(rows)))
	return NewFlowPanel(screener), screener
}

// ─── Construction + initial mode ─────────────────────────────────────────

func TestFlowPanelStartsInScreenerMode(t *testing.T) {
	p, _ := newFlowFixture(t, 5)
	if p.mode != flowModeScreener {
		t.Errorf("mode = %d, want flowModeScreener", p.mode)
	}
}

// ─── Mode transitions ────────────────────────────────────────────────────

// TestFlowPanelDrillFlipsToDetail: a FlowDrillMsg from the
// screener flips the mode and surfaces a SelectionChangedMsg the
// kernel can broadcast.
func TestFlowPanelDrillFlipsToDetail(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 2
	want := screener.currentSelection()

	_, cmd := p.Update(FlowDrillMsg{Selection: want})
	if p.mode != flowModeDetail {
		t.Fatalf("after drill, mode = %d, want flowModeDetail", p.mode)
	}
	if p.detailSel != want {
		t.Errorf("detailSel = %+v, want %+v", p.detailSel, want)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd carrying SelectionChangedMsg")
	}

	// The cmd is a tea.Batch; we walk its messages to find the
	// SelectionChangedMsg. Bubble Tea's tea.Batch is opaque so we
	// invoke the cmd and inspect the result — for tea.Batch it
	// returns a tea.BatchMsg.
	msg := cmd()
	if !batchContainsSelection(msg, want) {
		t.Errorf("cmd did not surface SelectionChangedMsg{New: %+v}", want)
	}
}

// batchContainsSelection walks a tea.Msg (which may be a single
// message or a tea.BatchMsg) and reports whether any of the
// resulting messages carries SelectionChangedMsg{New: want}.
func batchContainsSelection(msg tea.Msg, want dashboard.Selection) bool {
	switch m := msg.(type) {
	case dashboard.SelectionChangedMsg:
		return m.New == want
	case tea.BatchMsg:
		// BatchMsg is a slice of tea.Cmd. Run each and recurse.
		for _, c := range m {
			if c == nil {
				continue
			}
			if batchContainsSelection(c(), want) {
				return true
			}
		}
	}
	return false
}

// TestFlowPanelEscFlipsBackToScreener: Esc in detail mode flips
// back. Keys other than Esc don't.
func TestFlowPanelEscFlipsBackToScreener(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 2
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})
	if p.mode != flowModeDetail {
		t.Fatalf("setup: expected detail mode")
	}

	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.mode != flowModeScreener {
		t.Errorf("after Esc, mode = %d, want flowModeScreener", p.mode)
	}
}

func TestFlowPanelEscCollapsesExpandedDetailFirst(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if !p.detailExpanded || p.detailFocus != flowPaneBook {
		t.Fatalf("setup: expected expanded book, expanded=%v focus=%d", p.detailExpanded, p.detailFocus)
	}

	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.mode != flowModeDetail {
		t.Fatalf("first Esc should stay in detail mode, got %d", p.mode)
	}
	if p.detailExpanded {
		t.Fatalf("first Esc should collapse expanded pane")
	}

	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.mode != flowModeScreener {
		t.Fatalf("second Esc should return to screener, got %d", p.mode)
	}
}

// TestFlowPanelEscInScreenerModeIsNoOp: Esc has no special meaning
// in screener mode (the screener may decide to respond, but
// FlowPanel itself doesn't flip the mode somewhere weird).
func TestFlowPanelEscInScreenerModeNoModeFlip(t *testing.T) {
	p, _ := newFlowFixture(t, 5)
	p.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p.mode != flowModeScreener {
		t.Errorf("Esc in screener should not flip mode; got mode=%d", p.mode)
	}
}

// TestFlowPanelNonEscKeyInDetailModeDropped: in detail mode, keys
// other than Esc are dropped (detail composite uses
// activeChildNone — no child consumes keys). The mode must not
// flip.
func TestFlowPanelNonEscKeyInDetailModeDropped(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 0
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})

	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.mode != flowModeDetail {
		t.Errorf("non-Esc key in detail mode should not flip mode; got %d", p.mode)
	}
}

func TestFlowPanelDetailPaneFocusAndExpandKeys(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})

	if p.detailFocus != flowPaneBook {
		t.Fatalf("default detail focus = %d, want book", p.detailFocus)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyTab})
	if p.detailFocus != flowPaneTape {
		t.Fatalf("tab focus = %d, want tape", p.detailFocus)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if p.detailFocus != flowPaneBook {
		t.Fatalf("shift+tab focus = %d, want book", p.detailFocus)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	if p.detailFocus != flowPaneLiquidations || !p.detailExpanded {
		t.Fatalf("4 should jump+expand liq, focus=%d expanded=%v", p.detailFocus, p.detailExpanded)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.detailExpanded {
		t.Fatalf("enter should collapse expanded pane")
	}
}

func TestFlowPanelExpandedDetailRendersFocusedPane(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})

	view := p.View(120, 20, dashboard.PanelContext{})
	if !strings.Contains(view, "BOOK") {
		t.Fatalf("expanded book view missing BOOK title:\n%s", view)
	}
	if strings.Contains(view, "CHART") || strings.Contains(view, "TAPE") || strings.Contains(view, "LIQUIDATIONS") {
		t.Fatalf("expanded book view should hide other panes:\n%s", view)
	}
}

// ─── Key routing per mode ────────────────────────────────────────────────

// TestFlowPanelScreenerKeyRoutedToScreener: arrow keys in screener
// mode reach the screener's cursor.
func TestFlowPanelScreenerKeyRoutedToScreener(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	before := screener.cursor

	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if screener.cursor != before+1 {
		t.Errorf("screener cursor = %d, want %d (key routed correctly)",
			screener.cursor, before+1)
	}
}

// TestFlowPanelDetailModeKeysDoNotMoveScreenerCursor: an arrow key
// in detail mode must not reach the screener (the screener
// background mode should freeze its cursor while detail is up).
func TestFlowPanelDetailModeKeysDoNotMoveScreenerCursor(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 1
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})

	before := screener.cursor
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if screener.cursor != before {
		t.Errorf("detail-mode key should not move screener cursor; was %d, now %d",
			before, screener.cursor)
	}
}

// ─── Broadcast contract ──────────────────────────────────────────────────

// TestFlowPanelNonKeyMessageBroadcasts: a non-key message
// (FeedTickMsg, WindowSizeMsg) reaches both screener and detail
// composite. Verified indirectly by feeding a SelectionChangedMsg
// and checking that the screener's lastEmittedSelection updates
// (the screener Update path doesn't touch lastEmittedSelection
// directly on SelectionChangedMsg, but it does broadcast the
// message through; we instead use the detail-pane's reaction as
// the witness — the FlowBookPanel's selection field updates).
func TestFlowPanelNonKeyMessageBroadcastsToBoth(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 0

	// Send a refresh-tick — both screener and detail receive it.
	// The screener kicks off a fetch (we don't care about the
	// result here); the detail panes see flowScreenerRefreshMsg
	// and ignore it, but the Update path must not panic. The
	// observable witness: the cmd is non-nil because the
	// screener returns its fetchCmd batched.
	_, cmd := p.Update(flowScreenerRefreshMsg{})
	if cmd == nil {
		t.Errorf("expected non-nil cmd from screener's refresh handler")
	}
}

// ─── Subscriptions: mode-gated ───────────────────────────────────────────

// TestFlowPanelScreenerModeSubscriptionsFromScreener: in screener
// mode, FlowPanel.Subscriptions returns only the screener's
// overscan window (no detail-pane channels).
func TestFlowPanelScreenerModeSubscriptionsFromScreener(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 2

	got := p.Subscriptions(dashboard.Selection{})
	want := screener.Subscriptions(dashboard.Selection{})
	if len(got.Channels) != len(want.Channels) {
		t.Errorf("screener-mode subs count = %d, want %d (screener only)",
			len(got.Channels), len(want.Channels))
	}
	// Every channel should be a trades channel (no book/liquidations).
	for _, ch := range got.Channels {
		if !strings.HasPrefix(ch, "trades.") {
			t.Errorf("screener-mode channel %q has wrong prefix; expected trades.*", ch)
		}
	}
}

// TestFlowPanelDetailModeSubscriptionsFromDetail: after drill, the
// subscription set covers the detail composite (book + tape +
// liquidations + chart trades).
func TestFlowPanelDetailModeSubscriptionsFromDetail(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 0
	sel := screener.currentSelection()
	p.Update(FlowDrillMsg{Selection: sel})

	got := p.Subscriptions(sel)
	if len(got.Channels) == 0 {
		t.Fatalf("detail-mode subscriptions empty; expected book/tape/liq/trades channels")
	}

	// Look for at least one channel of each detail family.
	wantPrefixes := []string{"book.", "trades.", "liquidations."}
	for _, prefix := range wantPrefixes {
		found := false
		for _, ch := range got.Channels {
			if strings.HasPrefix(ch, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("detail-mode missing %q channel in: %v", prefix, got.Channels)
		}
	}
}

// ─── Capabilities: mode-gated ────────────────────────────────────────────

func TestFlowPanelCapabilitiesScreenerMode(t *testing.T) {
	p, _ := newFlowFixture(t, 5)
	caps := p.Capabilities()
	if !caps.ListNav || !caps.Drill {
		t.Errorf("screener-mode caps = %+v, want ListNav+Drill", caps)
	}
	if caps.Back {
		t.Errorf("screener-mode caps.Back = true; should be false (nothing to back out of)")
	}
}

func TestFlowPanelCapabilitiesDetailMode(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})

	caps := p.Capabilities()
	if !caps.Back {
		t.Errorf("detail-mode caps.Back = false; want true (Esc should advertise)")
	}
	if !caps.ListNav {
		t.Errorf("detail-mode caps.ListNav = false; want true for expanded book scroll")
	}
	if caps.Drill {
		t.Errorf("detail-mode advertised Drill = %+v; should not (no further drill)", caps)
	}
	if !caps.MultiPane {
		t.Errorf("detail-mode caps.MultiPane = false; want true for focus/expand keys")
	}
	if !caps.ChartTimeframe {
		t.Errorf("detail-mode caps.ChartTimeframe = false; want t advertised even when chart is not focused")
	}
}

func TestFlowPanelExpandedLargePrintsDoesNotAdvertiseTapeFilter(t *testing.T) {
	screener := newTestFlowScreenerPanel(fakeClient(), "BTC", "spot")
	screener.Update(snapMsg(makeRows(2)))
	p := NewFlowPanel(screener)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})

	overviewCaps := p.Capabilities()
	if !overviewCaps.TapeFilter {
		t.Fatalf("spot detail overview caps missing TapeFilter for regular TAPE pane: %+v", overviewCaps)
	}

	p.detailFocus = flowPaneLiquidations
	p.detailExpanded = true
	caps := p.Capabilities()
	if caps.TapeFilter {
		t.Fatalf("expanded LARGE PRINTS advertised TapeFilter: %+v", caps)
	}
	if !caps.ChartTimeframe {
		t.Fatalf("expanded LARGE PRINTS should still advertise global chart timeframe: %+v", caps)
	}
}

func TestFlowPanelExpandedTapeAdvertisesTapeFilter(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})
	p.detailFocus = flowPaneTape
	p.detailExpanded = true

	if caps := p.Capabilities(); !caps.TapeFilter {
		t.Fatalf("expanded TAPE caps missing TapeFilter: %+v", caps)
	}
}

func TestFlowPanelOverviewTapeFilterRoutesToTape(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	sel := screener.currentSelection()
	p.Update(FlowDrillMsg{Selection: sel})
	if p.detailFocus != flowPaneBook || p.detailExpanded {
		t.Fatalf("setup focus=%d expanded=%v, want overview with book focus", p.detailFocus, p.detailExpanded)
	}

	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'F'}})

	card, ok := p.tape.(*CardPanel)
	if !ok {
		t.Fatalf("tape panel = %T, want *CardPanel", p.tape)
	}
	tape, ok := card.inner.(*FlowTapePanel)
	if !ok {
		t.Fatalf("tape card inner = %T, want *FlowTapePanel", card.inner)
	}
	if tape.minUSD != 1_000 {
		t.Fatalf("overview F minUSD = %v, want 1000", tape.minUSD)
	}
	if p.detailFocus != flowPaneBook || p.detailExpanded {
		t.Fatalf("overview F changed focus/expand: focus=%d expanded=%v", p.detailFocus, p.detailExpanded)
	}

	p.Update(makeTradeEvent(tradesChannelForSelection(sel), 100, 20, "buy"))
	view := p.View(200, 24, dashboard.PanelContext{})
	if !strings.Contains(view, "min") || !strings.Contains(view, "$1K") {
		t.Fatalf("overview tape view missing active filter cue:\n%s", view)
	}
}

func TestFlowPanelTimeframeKeyRoutesToChartWithoutChartFocus(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})
	if p.detailFocus != flowPaneBook {
		t.Fatalf("setup: detail focus = %d, want book", p.detailFocus)
	}

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Fatal("timeframe key returned nil cmd; want chart reseed command")
	}

	card, ok := p.chart.(*CardPanel)
	if !ok {
		t.Fatalf("chart panel = %T, want *CardPanel", p.chart)
	}
	chart, ok := card.inner.(*FlowChartPanel)
	if !ok {
		t.Fatalf("chart card inner = %T, want *FlowChartPanel", card.inner)
	}
	if got := chart.chartTimeframe(); got != 5*time.Minute {
		t.Fatalf("chart timeframe = %s, want 5m", got)
	}
	if p.detailFocus != flowPaneBook {
		t.Fatalf("timeframe key changed focus to %d; want book unchanged", p.detailFocus)
	}
}

// ─── View ────────────────────────────────────────────────────────────────

// TestFlowPanelViewRendersScreenerInScreenerMode: the View is
// just the screener's view in screener mode. Surface check —
// we look for the INSTRUMENT header which only the screener
// emits.
func TestFlowPanelViewRendersScreenerInScreenerMode(t *testing.T) {
	p, _ := newFlowFixture(t, 5)
	view := p.View(120, 20, dashboard.PanelContext{})
	if !strings.Contains(view, "INSTRUMENT") {
		t.Errorf("screener-mode view missing INSTRUMENT header:\n%s", view)
	}
}

// TestFlowPanelViewRendersDetailInDetailMode: after drill, the
// view changes. The detail composite renders four child views;
// here we just verify the screener's INSTRUMENT header is
// no longer present (the screener is hidden) and SOME content
// renders without panicking.
func TestFlowPanelViewRendersDetailInDetailMode(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})

	view := p.View(120, 20, dashboard.PanelContext{})
	if strings.Contains(view, "INSTRUMENT") {
		t.Errorf("detail-mode view still shows screener header:\n%s", view)
	}
	if view == "" {
		t.Errorf("detail-mode view is empty; expected detail composite output")
	}
}

// ─── Init ────────────────────────────────────────────────────────────────

// TestFlowPanelInitBatchesScreenerCommands: Init returns a non-nil
// cmd carrying the screener's fetch + tick. Detail panes' Init
// returns nil today; the batch should still contain the
// screener's work.
func TestFlowPanelInitNonNil(t *testing.T) {
	p, _ := newFlowFixture(t, 5)
	if cmd := p.Init(); cmd == nil {
		t.Errorf("Init returned nil; expected screener fetch + tick batched")
	}
}

// ─── Regression: Enter via the kernel key path ───────────────────────────

// TestFlowPanelEnterDrillsViaKernelKeyPath is the regression test
// for the Codex-found bug where Enter handled by the screener
// emitted FlowDrillMsg via cmd, but cmds in Bubble Tea escape
// upward to the root model — they never come back down to
// FlowPanel. The kernel has no FlowDrillMsg case, so the drill
// would silently die.
//
// FlowPanel must intercept Enter directly in screener mode and
// perform the drill synchronously. The earlier
// TestFlowPanelDrillFlipsToDetail bypassed this by calling
// FlowPanel.Update(FlowDrillMsg{}) directly — that path works,
// but it's not the path the kernel uses.
func TestFlowPanelEnterDrillsViaKernelKeyPath(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 2
	want := screener.currentSelection()

	// Simulate the kernel's key path: FlowPanel.Update receives
	// the KeyMsg directly. NO FlowDrillMsg is sent — that's the
	// whole point of the bug.
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if p.mode != flowModeDetail {
		t.Fatalf("after Enter, mode = %d, want flowModeDetail (drill must happen synchronously)", p.mode)
	}
	if p.detailSel != want {
		t.Errorf("detailSel = %+v, want %+v (Enter must install selection)", p.detailSel, want)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd carrying SelectionChangedMsg for the kernel")
	}
	if !batchContainsSelection(cmd(), want) {
		t.Errorf("Enter cmd did not surface SelectionChangedMsg{New: %+v}", want)
	}
}

// ─── Regression: detail-mode broadcast suppression ───────────────────────

// TestFlowPanelDetailModeSuppressesScreenerSelectionCmd is the
// regression test for the Codex-found bug where a background
// snapshot refresh in detail mode could clamp the screener cursor
// onto a different row identity, making the screener emit a
// SelectionChangedMsg via cmd. That cmd would escape to the
// kernel, which would broadcast it back down — and FlowPanel
// would forward it to the detail composite, silently switching
// the user's drilled view to a different row.
//
// FlowPanel must drop the screener's cmd while in detail mode.
// The screener's row state still mutates correctly (rows are
// installed via Update during broadcast); only the
// surface-it-to-the-kernel cmd is suppressed.
func TestFlowPanelDetailModeSuppressesScreenerSelectionCmd(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	// Drill into row 0 (binance:BTCUSDT — the first row from
	// makeRows).
	screener.cursor = 0
	drilled := screener.currentSelection()
	p.Update(FlowDrillMsg{Selection: drilled})
	if p.mode != flowModeDetail {
		t.Fatalf("setup: expected detail mode")
	}

	// Now simulate a background snapshot that vanishes the cursor
	// row. The screener's snapshot handler would clamp the cursor
	// onto a different row identity and emit a
	// SelectionChangedMsg via cmd. Build a snapshot with
	// completely different identities so findRowByIdentity
	// can't match.
	smaller := makeRows(3)
	for i := range smaller {
		smaller[i].InstrumentName = "ETH-PERP"
		smaller[i].Exchange = "kraken"
	}

	// Send the snapshot through FlowPanel (the kernel broadcasts
	// non-key messages via FlowPanel.Update with the screener +
	// detail composite both seeing the message).
	_, cmd := p.Update(snapMsg(smaller))

	// The screener's row state must have updated (background
	// freshness preserved).
	if screener.rows[0].InstrumentName != "ETH-PERP" {
		t.Errorf("screener rows did not update during detail-mode broadcast; got %q",
			screener.rows[0].InstrumentName)
	}
	// detailSel must be unchanged — the user is still looking at
	// the originally-drilled row.
	if p.detailSel != drilled {
		t.Errorf("detailSel changed during detail-mode broadcast: was %+v, now %+v",
			drilled, p.detailSel)
	}
	// Mode must still be detail.
	if p.mode != flowModeDetail {
		t.Errorf("mode flipped during detail-mode broadcast; was detail, now %d", p.mode)
	}
	// The cmd FlowPanel returns must NOT carry a
	// SelectionChangedMsg with a new selection different from
	// detailSel — that's the bug. (cmd may be nil, may carry a
	// detail-pane cmd, but no rogue selection-change.)
	if cmd != nil {
		msg := cmd()
		if changed, ok := msg.(dashboard.SelectionChangedMsg); ok {
			if changed.New != drilled {
				t.Errorf("rogue SelectionChangedMsg surfaced in detail mode: %+v (want suppressed)", changed)
			}
		}
		// If cmd is a tea.BatchMsg, walk it the same way.
		if !batchContainsNoStrayChange(msg, drilled) {
			t.Errorf("detail-mode cmd surfaced a stray SelectionChangedMsg")
		}
	}
}

// ─── Regression: Enter on empty/loading screener is a no-op ──────────────

// TestFlowPanelEnterOnEmptyScreenerStaysInScreener is the
// regression test for the Codex-found bug where Enter, intercepted
// in FlowPanel before the screener's len(rows)==0 guard, would
// flip the mode to detail with a non-drillable selection
// ({Currency, Market} only — no Venue, no Symbol). The detail
// composite would then render with empty channel strings and
// the user would land on a blank pane.
//
// FlowPanel.drill() must guard against non-drillable selections
// itself (single chokepoint for both Enter and FlowDrillMsg
// paths) and stay in screener mode when the user presses Enter
// before the first snapshot installs rows.
func TestFlowPanelEnterOnEmptyScreenerStaysInScreener(t *testing.T) {
	// Construct a screener with NO rows installed (the loading
	// state). Wrap it in a FlowPanel.
	screener := newTestFlowScreenerPanel(fakeClient(), "BTC", "perpetuals")
	p := NewFlowPanel(screener)

	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if p.mode != flowModeScreener {
		t.Errorf("mode = %d, want flowModeScreener (Enter on empty must not flip)", p.mode)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd from Enter on empty screener; got non-nil %T", cmd())
	}
	// detailSel must remain zero — no spurious install.
	if (p.detailSel != dashboard.Selection{}) {
		t.Errorf("detailSel mutated on no-op drill: %+v", p.detailSel)
	}
}

// TestFlowPanelDrillRejectsNonDrillableSelection: same protection,
// driven through the FlowDrillMsg path. A library caller passing
// a {Currency, Market}-only selection must not land us in detail
// mode.
func TestFlowPanelDrillRejectsNonDrillableSelection(t *testing.T) {
	p, _ := newFlowFixture(t, 5)

	_, cmd := p.Update(FlowDrillMsg{Selection: dashboard.Selection{
		Currency: "BTC",
		Market:   "perpetuals",
		// Venue and Symbol intentionally empty.
	}})

	if p.mode != flowModeScreener {
		t.Errorf("non-drillable FlowDrillMsg flipped mode to %d; want screener", p.mode)
	}
	if cmd != nil {
		t.Errorf("non-drillable FlowDrillMsg returned non-nil cmd: %T", cmd())
	}
}

// ─── Regression: refresh-tick survives detail mode ───────────────────────

// TestFlowPanelDetailModePreservesScreenerRefreshCmd is the
// regression test for the Codex-found bug where the previous
// detail-mode suppression (which dropped EVERY screener cmd)
// also killed the screener's refresh loop. flowScreenerRefreshMsg
// returns tea.Batch(fetchCmd, tickCmd); without that cmd
// re-arming, the next refresh tick never fires and the background
// screener freezes the moment the user drills.
//
// The fix scopes suppression to flowScreenerSnapshotMsg only
// (the one Update path that emits SelectionChangedMsg cmds).
// Refresh-tick cmds must pass through unchanged in detail mode.
func TestFlowPanelDetailModePreservesScreenerRefreshCmd(t *testing.T) {
	p, screener := newFlowFixture(t, 5)
	screener.cursor = 0
	p.Update(FlowDrillMsg{Selection: screener.currentSelection()})
	if p.mode != flowModeDetail {
		t.Fatalf("setup: expected detail mode")
	}

	// Drive a refresh tick through FlowPanel. The screener's
	// handler returns tea.Batch(fetchCmd, tickCmd) — a non-nil
	// cmd that must survive detail-mode broadcast.
	_, cmd := p.Update(flowScreenerRefreshMsg{})

	if cmd == nil {
		t.Fatal("flowScreenerRefreshMsg in detail mode returned nil cmd; refresh loop would freeze")
	}
	// Sanity: invoke the cmd; running the screener's fetch+tick
	// shouldn't blow up on a fakeClient (the fetch will fail at
	// the HTTP layer and produce a flowScreenerSnapshotMsg with
	// err set — that's fine, we're checking the cmd is alive).
	_ = cmd()
}

// batchContainsNoStrayChange returns true iff the message contains
// no SelectionChangedMsg whose New != allowed. Used to assert that
// detail-mode broadcasts don't surface rogue selection-changes.
func batchContainsNoStrayChange(msg tea.Msg, allowed dashboard.Selection) bool {
	switch m := msg.(type) {
	case dashboard.SelectionChangedMsg:
		return m.New == allowed
	case tea.BatchMsg:
		for _, c := range m {
			if c == nil {
				continue
			}
			if !batchContainsNoStrayChange(c(), allowed) {
				return false
			}
		}
	}
	return true
}
