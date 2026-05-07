package panels

// FlowPanel — the v0.10.0 flow dashboard's mode owner.
//
// One panel that wears two hats: a screener on first render, a
// detail composite (chart + book + tape + liquidations) after the
// user drills into a row. The mode is the single piece of state
// FlowPanel owns directly; everything else lives in the screener
// or the detail child panels.
//
// Why concentrate the mode here:
//   - The screener emits FlowDrillMsg on Enter; the detail mode
//     receives Esc to back out. Both transitions are local — they
//     never need the kernel to participate. Putting the switch
//     anywhere else (in the kernel, in the screener) would either
//     leak flow-mode semantics into shared infrastructure or
//     scatter the back-out path across two panels.
//   - The kernel routes keys to the focused panel. FlowPanel IS
//     the focused panel from the kernel's perspective; from inside
//     FlowPanel we re-route to either the screener or the detail
//     composite per mode.
//
// Detail composite layout (per the v0.10.0 visual polish pass):
//
//	┌───────────────────────┐ ┌───────────────────────┐
//	│ CHART                 │ │                       │
//	│ (top-left, ~60% h)    │ │ BOOK                  │
//	│                       │ │ (full right half)     │
//	├───────────┬───────────┤ │                       │
//	│ TAPE      │ LIQ       │ │                       │
//	│ (bottom)  │ (bottom)  │ │                       │
//	└───────────┴───────────┘ └───────────────────────┘
//
// Built from a full-height right BOOK pane plus a left column:
//   - left column: chart over (tape | liquidations)
//   - right half:  book ladder, full available height
//   - vertical:    chart gets ~60% of the left column; tape/liq
//     share the lower ~40%
//
// All four detail panels are passive — they react to
// SelectionChangedMsg and FeedTickMsg; they declare ListNav: false,
// Drill: false. That's why the composite uses activeChildNone for
// key routing: nothing inside the detail composite consumes keys.
// Esc is the one key that matters in detail mode, and FlowPanel
// handles it directly before the composite ever sees the message.
//
// Subscriptions semantics:
//
//   - Screener mode: declares ONLY the screener's overscan window
//     (cursor ± flowScreenerOverscanRows). The detail composite's
//     panels are not subscribed because we'd over-subscribe to
//     book/tape/liq channels for a venue the user hasn't drilled
//     into yet — those are higher-rate streams than the screener's
//     trades-only warming, so we keep them off until drill.
//
//   - Detail mode: declares the detail composite's union (book +
//     tape + liquidations + chart trades). The screener's overscan
//     drops to nothing; once the user is in detail mode, keeping
//     trades streams warm for ten unrelated venues is wasted
//     bandwidth.
//
// The kernel's FeedRouter compares the union across all panels to
// the previous set and issues subscribe / unsubscribe RPCs as
// needed. Mode flips therefore produce a clean swap rather than
// double-subscription.

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
)

// flowMode names the two states FlowPanel toggles between.
type flowMode int

const (
	flowModeScreener flowMode = iota
	flowModeDetail
)

// flowLargePrintMinUSD is the fixed notional threshold for the spot
// detail's LARGE PRINTS pane. The pane is opinionated by design — it
// shows only trades worth paying attention to on liquid spot pairs —
// and does NOT participate in the `F min size` cycle. Users who want
// a different threshold or no threshold use the regular TAPE pane,
// which starts at "all" and cycles via `F`.
//
// $100K is the right starting point: on `binance:BTCUSDT`, retail
// prints clear $10K constantly, so a $10K filter renders as a wall
// of small trades that look identical to the unfiltered tape. $100K
// pulls out the actual outliers — block trades, OTC flow, large
// market-takers — without going so high that quieter pairs go
// silent. Future polish (v0.12+) could adapt this to a per-pair
// percentile of recent notionals; v0.11.x keeps it fixed.
const flowLargePrintMinUSD = 100_000

type flowDetailPane int

const (
	flowPaneChart flowDetailPane = iota
	flowPaneBook
	flowPaneTape
	flowPaneLiquidations
	flowPaneCount
)

// FlowPanel implements dashboard.Panel.
type FlowPanel struct {
	mode flowMode

	// screener is the perp screener — always present, owns its own
	// REST refresh loop. Stays alive across mode flips so the
	// snapshot/cursor state survive a screener → detail → screener
	// round trip.
	screener *FlowScreenerPanel

	// detail is the composite that renders the four detail panes.
	// Always present — built once at construction; reused across
	// mode flips. Its child panels reset their state on
	// SelectionChangedMsg, so a re-drill into the same row gets a
	// fresh tape/chart/book/liquidations view.
	detail Panel

	chart Panel
	book  Panel
	tape  Panel
	liq   Panel

	detailFocus    flowDetailPane
	detailExpanded bool

	// detailSel is the selection currently installed on the detail
	// composite. Tracked here so we don't re-broadcast
	// SelectionChangedMsg to the children when the screener emits
	// the same selection twice (cursor jitter, snapshot refresh
	// re-emit).
	detailSel dashboard.Selection
}

// NewFlowPanel constructs the flow dashboard's top-level panel.
// The screener's currency + market + REST client are passed
// through; the detail panes are constructed empty (no selection)
// and receive their first selection via SelectionChangedMsg when
// the user drills.
func NewFlowPanel(screener *FlowScreenerPanel) *FlowPanel {
	// Wrap each detail panel in a titled card so the layout has
	// clear visual separation. The cards forward Subscriptions /
	// Update / Capabilities to the wrapped panel transparently;
	// only View paints the border + title around the inner output.
	chart := NewCardPanel(NewFlowChartPanel(dashboard.Selection{}, screener.client), "CHART")
	book := NewCardPanel(NewFlowBookPanel(dashboard.Selection{}), "BOOK")
	tape := NewCardPanel(NewFlowTapePanel(dashboard.Selection{}), "TAPE")
	var liq Panel
	if screener.market == "spot" {
		liq = NewCardPanel(NewFlowLargePrintsPanel(dashboard.Selection{}, flowLargePrintMinUSD), "LARGE PRINTS")
	} else {
		liq = NewCardPanel(NewFlowLiquidationsPanel(dashboard.Selection{}), "LIQUIDATIONS")
	}

	// Layout: BOOK takes the full right half so the canonical
	// seven-column ladder gets enough width to render without the
	// per-line clamp truncating columns. CHART takes the top of
	// the left half; TAPE + LIQ share the bottom of the left half
	// side-by-side (TAPE wider since it has more rows of value;
	// LIQ narrower since events are sparse).
	//
	// Visual layout:
	//
	//   ┌──────── 50% ────────┐ ┌──────── 50% ────────┐
	//   │ CHART     (60% h)   │ │                     │
	//   │                     │ │  BOOK               │
	//   │                     │ │  (full height,      │
	//   ├──────── 40% h ──────┤ │   50% width)        │
	//   │ TAPE 50% │ LIQ 50%  │ │                     │
	//   │          │          │ │                     │
	//   └──────────┴──────────┘ └─────────────────────┘
	//
	// Why this beats the previous 70/30 layout:
	//   - BOOK gets ~half the screen instead of 30%. The
	//     canonical ladder's seven columns need ~80 cells for a
	//     truthful render; 50% of a 140-cell terminal is 70
	//     cells (close enough — the clamp keeps it tidy at the
	//     margins). 30% of 140 was 42 cells and the clamp was
	//     eating CUM ASK / ASK SZ off the right edge.
	//   - TAPE and LIQ are side-by-side instead of stacked.
	//     LIQ is sparse on most pairs; giving it a tall thin
	//     strip wasted 15% of vertical space on a near-empty
	//     pane. Side-by-side, LIQ gets a column that's used
	//     proportionally to its data density.
	//   - CHART is unchanged in horizontal allocation but
	//     simpler in stack arrangement.
	//
	// Building blocks (inner-to-outer):
	//   tapeLiq = Split(tape, liq, 60%)         — bottom-left
	//   left    = FlexStack(chart, tapeLiq, 60%) — full left
	//                                              column
	//   detail  = Split(left, book, 50%)         — left + book
	//
	// Key routing: detail's activeChild=1 routes keys to book.
	// The left column is passive.
	tapeLiq := NewSplitPanel(tape, liq, 50, activeChildNone)
	left := NewFlexStackPanel(chart, tapeLiq, 60, activeChildNone)
	detail := NewSplitPanel(left, book, 50, 1)

	return &FlowPanel{
		mode:     flowModeScreener,
		screener: screener,
		detail:   detail,
		chart:    chart,
		book:     book,
		tape:     tape,
		liq:      liq,
		// BOOK is the highest-value pane to expand on narrow
		// terminals; start focus there so Enter immediately gives
		// the user the full ladder.
		detailFocus: flowPaneBook,
	}
}

// Init batches the screener's startup commands. The detail panes
// don't have startup work (they're passive — they wait for the
// first SelectionChangedMsg); calling their Init() is a defensive
// no-op consistent with the kernel's contract that every panel's
// Init runs at startup.
func (p *FlowPanel) Init() tea.Cmd {
	return tea.Batch(p.screener.Init(), p.detail.Init())
}

// Update routes messages by mode:
//
//   - Screener mode: Enter is intercepted by FlowPanel itself and
//     drills directly (the screener's own Enter handler emits
//     FlowDrillMsg via cmd, but cmd results escape to the kernel,
//     not back to FlowPanel — and the kernel has no
//     FlowDrillMsg case, so the drill would silently die). Other
//     keys forward to the screener.
//   - Detail mode: Esc flips back to screener; other keys are
//     dropped (detail panes are passive).
//   - Non-key messages (FeedTickMsg, SelectionChangedMsg,
//     WindowSizeMsg, etc.): broadcast to BOTH the screener and
//     the detail composite, regardless of mode. The screener
//     needs ticks even in detail mode so its background row state
//     stays current. The detail composite receives the broadcast
//     in screener mode too, but its panes' subscription set is
//     empty until drill (FlowPanel.Subscriptions is mode-gated)
//     so any FeedTickMsg that arrives in screener mode is for a
//     channel none of the detail panes track — the panes ignore
//     it. The broadcast is mostly a uniformity contract:
//     WindowSizeMsg has to reach the detail composite in either
//     mode for layout math, and treating every non-key message
//     identically keeps Update simple.
//
// Note: Bubble Tea cmds escape upward to the root model — they
// never come back to the panel that returned them. Any
// FlowPanel ↔ child handshake that needs to round-trip MUST be
// done synchronously inside this Update, not via a returned cmd.
func (p *FlowPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	switch m := msg.(type) {
	case FlowDrillMsg:
		// Direct FlowDrillMsg delivery (from a test that calls
		// FlowPanel.Update(FlowDrillMsg{}) or any caller that
		// translates an external trigger into a drill). In the
		// normal kernel path, Enter is intercepted in the KeyMsg
		// branch below — see drill() — so this case rarely fires
		// at runtime. Kept for symmetry and so library callers can
		// drive the drill imperatively if they want to.
		return p, p.drill(m.Selection)

	case tea.KeyMsg:
		key := keymap.ClassifyKey(m.String())

		// Esc in detail mode → back out to screener. We classify
		// through keymap to keep the vocabulary consistent with
		// every other surface ("esc backs out").
		if p.mode == flowModeDetail && key == keymap.ActEsc {
			if p.detailExpanded {
				p.detailExpanded = false
				return p, nil
			}
			p.mode = flowModeScreener
			// Surface a SelectionChangedMsg so the kernel re-runs
			// refreshSubscriptions; FlowPanel.Subscriptions now
			// returns the screener's overscan only, and the
			// FeedRouter unsubscribes the detail-only channels on
			// the next refresh.
			old := p.detailSel
			cur := p.screener.currentSelection()
			return p, makeSelectionChangedCmd(old, cur)
		}

		// Mode-routed key dispatch.
		switch p.mode {
		case flowModeScreener:
			// Intercept Enter HERE, not in the screener. The
			// screener's own Enter handler emits FlowDrillMsg via
			// cmd, but cmds in Bubble Tea travel up to the root
			// model — they never come back down to the panel that
			// returned them. The kernel has no FlowDrillMsg case
			// and no fall-through to the focused panel for
			// non-classified messages, so the drill would silently
			// vanish. Doing it synchronously here avoids that
			// class of bug entirely.
			//
			// Caveat: we read p.screener.currentSelection()
			// synchronously here, BEFORE forwarding the key. The
			// screener's Enter handler builds its drill cmd from
			// the same source, so the two paths agree on which row
			// is being drilled.
			if key == keymap.ActEnter {
				return p, p.drill(p.screener.currentSelection())
			}

			updated, cmd := p.screener.Update(msg)
			// Screener returns *FlowScreenerPanel via the Panel
			// interface; the type assertion keeps our typed pointer.
			if sp, ok := updated.(*FlowScreenerPanel); ok {
				p.screener = sp
			}
			return p, cmd
		case flowModeDetail:
			if p.handleDetailFocusKey(key) {
				return p, nil
			}
			if key == keymap.ActTimeframeCycle {
				updated, cmd := p.chart.Update(msg)
				p.chart = updated
				return p, cmd
			}
			if key == keymap.ActTapeFilter && p.detailFocus == flowPaneTape {
				updated, cmd := p.tape.Update(msg)
				p.tape = updated
				return p, cmd
			}
			if p.detailExpanded {
				updated, cmd := p.focusedDetailPane().Update(msg)
				p.setFocusedDetailPane(updated)
				return p, cmd
			}
			// Detail composite has activeChild → top → book. Forward
			// the key down so the book pane's depth/group/recenter
			// handlers fire. Esc was already intercepted above
			// (mode flip back to screener); other unbound keys
			// reach the book's applyKey, which no-ops on actions
			// it doesn't recognise.
			updated, cmd := p.detail.Update(msg)
			p.detail = updated
			return p, cmd
		}

	case flowScreenerSnapshotMsg:
		// Snapshot install is the ONE screener Update path that
		// can emit a SelectionChangedMsg cmd (via the cursor
		// identity-clamp on row vanish — see flow_screener.go's
		// snapshot handler). In detail mode, surfacing that cmd
		// would let the screener's background re-clamp pull the
		// rug out from under the drilled detail panes: the kernel
		// would broadcast the SelectionChangedMsg, FlowPanel would
		// forward it to the detail composite, every detail pane
		// would reset its ring/cache, and the user's drilled view
		// would silently swap to whatever row the screener clamped
		// onto.
		//
		// Suppression is scoped to THIS message type only. Other
		// non-key broadcasts (FeedTickMsg, WindowSizeMsg,
		// flowScreenerRefreshMsg) must pass their cmds through —
		// flowScreenerRefreshMsg in particular re-arms the
		// screener's REST refresh loop via fetchCmd + tickCmd, and
		// dropping that would freeze the background screener until
		// the user backs out. The fall-through default branch
		// handles those.
		updatedScreener, screenerCmd := p.screener.Update(msg)
		if sp, ok := updatedScreener.(*FlowScreenerPanel); ok {
			p.screener = sp
		}
		updatedDetail, detailCmd := p.detail.Update(msg)
		p.detail = updatedDetail
		if p.mode == flowModeDetail {
			// Drop screenerCmd specifically because it may carry a
			// rogue SelectionChangedMsg. Detail panes don't react
			// to flowScreenerSnapshotMsg anyway, so detailCmd is
			// effectively always nil here — but we pass it through
			// for uniformity in case that changes.
			return p, detailCmd
		}
		return p, tea.Batch(screenerCmd, detailCmd)

	default:
		// Non-key, non-drill, non-snapshot message. Broadcast to
		// both. The screener's row state must keep updating in
		// detail mode; the detail composite must receive
		// WindowSizeMsg in either mode for layout. FeedTickMsg in
		// screener mode reaches the detail composite but its panes
		// have no matching channel (subscriptions are mode-gated)
		// so they no-op. flowScreenerRefreshMsg in detail mode
		// re-arms the screener's REST refresh — its cmd MUST pass
		// through or the screener freezes until the user backs
		// out. Symmetric broadcast keeps Update simple.
		updatedScreener, screenerCmd := p.screener.Update(msg)
		if sp, ok := updatedScreener.(*FlowScreenerPanel); ok {
			p.screener = sp
		}
		updatedDetail, detailCmd := p.detail.Update(msg)
		p.detail = updatedDetail
		return p, tea.Batch(screenerCmd, detailCmd)
	}
	return p, nil
}

func (p *FlowPanel) handleDetailFocusKey(action keymap.Action) bool {
	switch action {
	case keymap.ActCycleFocus:
		p.detailFocus = (p.detailFocus + 1) % flowPaneCount
		return true
	case keymap.ActReverseFocus:
		p.detailFocus = (p.detailFocus - 1 + flowPaneCount) % flowPaneCount
		return true
	case keymap.ActJumpPane1:
		p.detailFocus = flowPaneChart
		p.detailExpanded = true
		return true
	case keymap.ActJumpPane2:
		p.detailFocus = flowPaneBook
		p.detailExpanded = true
		return true
	case keymap.ActJumpPane3:
		p.detailFocus = flowPaneTape
		p.detailExpanded = true
		return true
	case keymap.ActJumpPane4:
		p.detailFocus = flowPaneLiquidations
		p.detailExpanded = true
		return true
	case keymap.ActEnter:
		p.detailExpanded = !p.detailExpanded
		return true
	}
	return false
}

func (p *FlowPanel) focusedDetailPane() Panel {
	switch p.detailFocus {
	case flowPaneChart:
		return p.chart
	case flowPaneBook:
		return p.book
	case flowPaneTape:
		return p.tape
	case flowPaneLiquidations:
		return p.liq
	default:
		return p.book
	}
}

func (p *FlowPanel) setFocusedDetailPane(panel Panel) {
	switch p.detailFocus {
	case flowPaneChart:
		p.chart = panel
	case flowPaneBook:
		p.book = panel
	case flowPaneTape:
		p.tape = panel
	case flowPaneLiquidations:
		p.liq = panel
	}
}

// selectionDrillable reports whether a Selection has the fields
// the detail composite needs to subscribe — Venue, Symbol, and
// Market. Currency alone isn't enough; the detail panes' channel
// builders (book.<market>.<venue>.<symbol> et al.) return empty
// strings when those fields are missing, which would land the
// user on a blank detail pane with no live data.
//
// Used as the gate in drill() so both the Enter key path and the
// imperative FlowDrillMsg path are protected from drilling into a
// non-drillable row (e.g. Enter pressed while the screener is
// still loading and currentSelection() returns just
// {Currency, Market}).
func selectionDrillable(sel dashboard.Selection) bool {
	return sel.Venue != "" && sel.Symbol != "" && sel.Market != ""
}

// drill performs the screener → detail mode flip. Called from both
// the FlowDrillMsg case (imperative drill) and the Enter
// interception in screener-mode key handling. Returns the cmd that
// must escape to the kernel: a SelectionChangedMsg so the
// FeedRouter re-runs refreshSubscriptions and picks up the detail
// composite's channels.
//
// Returns nil and stays in screener mode if the selection isn't
// drillable (no Venue / Symbol / Market — typically because the
// screener is still loading or has zero rows). The caller doesn't
// need to pre-check; this guard is the single chokepoint for both
// drill paths so the bug class can't sneak back in.
//
// Synchronous on the detail composite: we install the selection
// directly via Update so detail children's selection state and
// p.detailSel stay in lockstep. If we returned a cmd carrying the
// SelectionChangedMsg for the children too, that cmd would escape
// to the kernel, which would broadcast it back down — same
// problem as the Enter-via-cmd bug. Local install + cmd-to-kernel
// is the only correct shape.
func (p *FlowPanel) drill(sel dashboard.Selection) tea.Cmd {
	if !selectionDrillable(sel) {
		// Not drillable — stay in screener mode and emit nothing.
		// The user's Enter press is effectively a no-op until the
		// screener has rows; this matches the screener's own
		// "no rows → keys are no-ops" contract from
		// FlowScreenerPanel.handleKey.
		return nil
	}
	p.mode = flowModeDetail
	old := p.detailSel
	p.detailSel = sel
	// Local broadcast to the detail composite — synchronous, the
	// Update return travels up to FlowPanel's caller (the kernel)
	// only via the cmd we return below.
	updated, _ := p.detail.Update(dashboard.SelectionChangedMsg{Old: old, New: sel})
	p.detail = updated
	// Surface the SelectionChangedMsg to the kernel so the
	// FeedRouter sees it and re-subscribes. The kernel will also
	// re-broadcast it back down to every panel — including back to
	// FlowPanel itself, which forwards it to the detail composite
	// AGAIN. That double-install is harmless: detail children
	// re-clear their (already empty) caches and re-call
	// Subscriptions with the same selection; the kernel dedupes.
	return makeSelectionChangedCmd(old, sel)
}

// View renders the active mode's panel into the full pane area.
// Composites have no chrome of their own — the kernel's header
// already names the dashboard.
//
// No min-size guard: the composite/card layer normalises every
// pane block to its allotted width×height (clipping rows and
// padding/truncating lines), so panes can't break the grid at
// any terminal size. Production CLIs degrade rather than refuse.
func (p *FlowPanel) View(width, height int, ctx dashboard.PanelContext) string {
	switch p.mode {
	case flowModeDetail:
		if p.detailExpanded {
			return p.focusedDetailPane().View(width, height, ctx)
		}
		return p.detail.View(width, height, ctx)
	default:
		return p.screener.View(width, height, ctx)
	}
}

// Subscriptions returns the channels the active mode needs.
// Mode-gated rather than always-union because keeping book + tape
// + liquidations subscribed for the screener mode would burn
// bandwidth on streams the user can't see.
//
// The screener's selection-overscan stays in screener mode; the
// detail composite's union takes over in detail mode. Crossing
// modes triggers the kernel's FeedRouter to compute the diff and
// issue add/remove RPCs.
func (p *FlowPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	switch p.mode {
	case flowModeDetail:
		return p.detail.Subscriptions(sel)
	default:
		return p.screener.Subscriptions(sel)
	}
}

// Title — empty. The kernel header carries the dashboard's name;
// FlowPanel doesn't add chrome.
func (p *FlowPanel) Title() string { return "" }

// Capabilities — mode-gated. Screener mode advertises ListNav +
// Drill (the screener's keys). Detail overview unions every visible
// pane, because every pane is on screen. Expanded detail advertises
// the focused pane only, plus chart timeframe because `t` is routed
// globally in detail mode even when CHART is not focused.
func (p *FlowPanel) Capabilities() keymap.Capabilities {
	switch p.mode {
	case flowModeDetail:
		base := keymap.Capabilities{Back: true, MultiPane: true}.Union(p.chart.Capabilities())
		if p.detailExpanded {
			return base.Union(p.focusedDetailPane().Capabilities())
		}
		return base.Union(p.detail.Capabilities())
	default:
		return p.screener.Capabilities()
	}
}

// Compile-time interface satisfaction.
var _ Panel = (*FlowPanel)(nil)
