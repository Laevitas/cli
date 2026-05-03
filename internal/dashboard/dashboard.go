// Package dashboard is the kernel for `laevitas dash` — multi-pane TUI
// dashboards composed of independent Panel implementations sharing a
// selection state and a single WebSocket feed.
//
// The architecture follows leg100/pug's pattern (single root model
// owns state, dumb children render slices of it) and Charm's
// realtime-example pattern (a goroutine writes to a chan tea.Msg, a
// re-arming tea.Cmd pumps that channel into the model).
//
// Three message classes flow through the root:
//
//   - tea.KeyMsg       → routed to the focused panel only (so j/k
//                        doesn't navigate every panel at once).
//   - tea.WindowSizeMsg → broadcast to every panel after the root
//                        recomputes its size budget.
//   - data messages    → broadcast to every panel (FeedTickMsg from
//                        the WS pump, SelectionChangedMsg when the
//                        active symbol/expiry/etc. changes).
//
// Hidden panes still receive data messages so re-focus is instant —
// no warm-up gap when the user Tabs to a panel they haven't viewed
// before. Only key events are focus-gated.
package dashboard

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
	"github.com/laevitas/cli/internal/wsclient"
)

// ─── public types ──────────────────────────────────────────────────────────

// Selection is the dashboard's shared "what are we looking at right
// now" state. Panels read from it during their View() and react to
// SelectionChangedMsg in their Update() if they want to re-subscribe
// or refresh.
//
// Fields are deliberately optional — a book dashboard only needs
// Symbol; a chain dashboard needs Symbol + Expiry; a vol dashboard
// might use Currency. Empty fields mean "not applicable to this
// dashboard."
type Selection struct {
	Symbol   string // e.g. "BTCUSDT"
	Currency string // e.g. "BTC"
	Expiry   string // e.g. "26JUN26"
	Strike   float64
	Venue    string // optional venue lock
	// Market is the canonical product family (perpetuals, futures,
	// options, spot, predictions). Required for panels that build WS
	// channel strings from the selection — `book.<market>.<venue>.<symbol>`
	// can't be assembled without it. Added in v0.9.3 for the
	// upcoming flow dashboard's screener→detail drill-down; existing
	// dashboards that don't read Market are unaffected.
	Market string
}

// SelectionChangedMsg is fanned out to every panel whenever the root
// updates its Selection. Panels handle it in their Update() — most
// will issue a re-subscribe Cmd or invalidate cached state.
type SelectionChangedMsg struct {
	Old Selection
	New Selection
}

// FeedTickMsg carries one wsclient event from the gateway through to
// every panel. The root re-arms the wait Cmd after each tick so the
// channel keeps draining.
type FeedTickMsg struct {
	Event wsclient.Event
}

// FeedSpec is the set of WebSocket channels a panel wants to be
// subscribed to. The root collects every panel's spec on
// initialisation and on every SelectionChangedMsg, deduplicates,
// and updates the live subscription set.
type FeedSpec struct {
	Channels []string
}

// FeedState describes the current connection/subscription health.
// Panels read this from PanelContext to decide whether to render
// "loading…" placeholders, the live data, or an error message.
//
// Lifecycle:
//
//	FeedDialing      first dial in flight; nothing has connected yet
//	FeedSubscribed   dialed + subscribed but no events arrived yet
//	FeedHealthy      events flowing normally
//	FeedReconnecting connection dropped, retrying with backoff
//	FeedFatal        unrecoverable (auth fail, conn cap, etc.)
type FeedState int

const (
	FeedDialing FeedState = iota
	FeedSubscribed
	FeedHealthy
	FeedReconnecting
	FeedFatal
)

// String returns the human-readable label rendered in the
// connection-state pill. Kept here so every renderer that surfaces
// FeedState uses identical wording.
func (f FeedState) String() string {
	switch f {
	case FeedDialing:
		return "connecting"
	case FeedSubscribed:
		return "subscribed"
	case FeedHealthy:
		return "live"
	case FeedReconnecting:
		return "reconnecting"
	case FeedFatal:
		return "disconnected"
	}
	return "unknown"
}

// PanelContext is the per-render handoff from kernel to panel. The
// kernel builds it fresh each frame and passes it to View(). Panels
// pull what they need by field; new fields can be added without
// breaking existing implementations (panels ignore fields they
// don't read).
//
// This is the "dumb child, smart root" pattern from leg100/pug —
// panels never get a reference to the kernel, only to the slice of
// state they care about. That keeps panels testable in isolation
// (build a PanelContext with fake values, call View) and prevents
// the kernel from growing tendrils into panel internals.
type PanelContext struct {
	// SpinnerFrame is the current animation glyph. Panels render it
	// next to "loading…" labels or per-row waiting indicators.
	SpinnerFrame string

	// FeedState reflects the WS gateway health. Panels showing
	// per-pair waiting indicators check this AND their own data
	// freshness — a healthy feed with no data for a specific pair
	// just means that pair hasn't fired yet.
	FeedState FeedState

	// Focused is true when this panel is the active one (received
	// the last keystroke). Renderers can use this to bold the
	// border, highlight the cursor, or surface focus-only hints.
	Focused bool

	// LastError carries the most recent soft error text from the
	// feed router or any panel-internal source. Empty when
	// everything is fine. Panels typically don't render this — the
	// kernel surfaces it in the footer — but it's available for
	// panels that want to inline it.
	LastError string
}

// Panel is the interface every dashboard view implements. A panel
// owns its own state and renders into a width × height region the
// root assigns. It declares what feed channels it needs (so the
// root can subscribe / unsubscribe); it can react to selection
// changes by issuing tea.Cmd that re-renders or re-subscribes.
//
// Update returns a (Panel, tea.Cmd) so panel state can change in
// response to a message. Returning the receiver unchanged is normal;
// Bubble Tea is happy with no-op updates.
type Panel interface {
	// Init returns any startup commands (typically nil).
	Init() tea.Cmd

	// Update handles one message. Returns the (possibly mutated)
	// panel and any follow-up command. Handlers should be cheap;
	// long work goes into a tea.Cmd.
	Update(msg tea.Msg) (Panel, tea.Cmd)

	// View renders the panel into width × height cells. The string
	// MUST NOT exceed those bounds — the layout engine doesn't clip.
	// Use lipgloss.NewStyle().Width(w).Height(h).MaxWidth(w) to
	// enforce. ctx carries kernel state every panel might need
	// (spinner frame, feed health, focus); pull what you need.
	View(width, height int, ctx PanelContext) string

	// Subscriptions declares the channels this panel wants. Called
	// at startup and after every SelectionChangedMsg. Returning an
	// empty FeedSpec means "no subscriptions right now."
	Subscriptions(sel Selection) FeedSpec

	// Title returns a short header label shown in the panel border.
	// Used by the layout chrome only — panels can return "" to skip
	// the border.
	Title() string

	// Capabilities declares which keymap features the panel honors
	// (list nav, depth tier, drill-down, etc.). The kernel ORs the
	// active panel's capabilities with its own layout-derived flags
	// (e.g. MultiPane when more than one slot is populated) and
	// passes the result to keymap.FooterHints / RenderHelpOverlay.
	// That's how the footer stops advertising keys that have no
	// effect on the active surface.
	Capabilities() keymap.Capabilities
}

// ─── pane positions & layout ───────────────────────────────────────────────

// PaneSlot identifies one of the fixed positions in the dashboard's
// layout. The kernel ships with a small enum because every dashboard
// in v0.8.3 uses one of three layouts; layout flexibility comes
// later (see roadmap memory). Each layout interprets the slots
// differently:
//
//   - LayoutSingle:  PaneMain only.
//   - LayoutSplit:   PaneMain | PaneSide  (left/right).
//   - LayoutTriad:   PaneMain | PaneSide  on the right; PaneStrip across the bottom.
//
// Future layouts (2x2, 1+3, etc.) will add slots but never remove
// these — backwards compatibility for any panel implementation.
type PaneSlot int

const (
	PaneMain PaneSlot = iota
	PaneSide
	PaneStrip
)

// LayoutKind picks how the root composes its panels.
type LayoutKind int

const (
	LayoutSingle LayoutKind = iota
	LayoutSplit
	LayoutTriad
)

// ─── root model ────────────────────────────────────────────────────────────

// Root is the top-level Bubble Tea model. It owns:
//   - selection state shared across panels
//   - the panel registry keyed by PaneSlot
//   - which slot has focus
//   - the WS feed router (one connection, fans events to every panel)
//   - terminal dimensions
//
// Root.Update implements the three-tier dispatch (global keys / size
// broadcast / data broadcast) and re-arms the feed pump on every
// FeedTickMsg.
type Root struct {
	cfg       Config
	panels    map[PaneSlot]Panel
	focused   PaneSlot
	selection Selection

	width, height int

	feed      *FeedRouter
	feedState FeedState
	lastErr   string

	// spinner is the single source of animation for every "loading"
	// indicator across the dashboard — header pill, panel-level
	// placeholders, per-row waiting cells. Owning it on Root means
	// one tick path drives every visible spinner; panels pull the
	// current frame via PanelContext at View() time and never tick
	// their own.
	spinner spinner.Model

	// helpOpen toggles the keybinding overlay. Same idiom as the
	// single-stream renderer so users get the same `?` everywhere.
	helpOpen bool

	// quitting flag — set when we're shutting down so the feed
	// goroutine and the model agree on lifetime.
	quitting bool
}

// Config bundles everything Root needs at construction time.
//
// Title is shown in the dashboard's chrome (e.g. "book — BTCUSDT").
// Panels is the slot-keyed map of children. Initial selection comes
// from cobra-flag parsing; panels read it in their first Init or in
// their Subscriptions() method.
//
// APIKey + GatewayURL are the WS connection parameters. The router
// lazily dials on the first Subscriptions() call and reconnects on
// drop with the existing wsclient backoff machinery.
type Config struct {
	Title      string
	Layout     LayoutKind
	Panels     map[PaneSlot]Panel
	Selection  Selection
	APIKey     string
	GatewayURL string
}

// NewRoot wires up the dashboard. Returns a tea.Model ready to pass
// to tea.NewProgram. Doesn't start the feed yet — that happens in
// Init() when Bubble Tea calls it.
func NewRoot(cfg Config) *Root {
	if cfg.Panels == nil {
		cfg.Panels = make(map[PaneSlot]Panel)
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(brandGreenHex))
	r := &Root{
		cfg:       cfg,
		panels:    cfg.Panels,
		focused:   PaneMain,
		selection: cfg.Selection,
		width:     100,
		height:    30,
		feed:      newFeedRouter(cfg.APIKey, cfg.GatewayURL),
		feedState: FeedDialing,
		spinner:   sp,
	}
	return r
}

// brandGreenHex is the lipgloss-friendly hex for the spinner's
// default style. lipgloss takes a hex string, not the ANSI escape
// we use elsewhere — so we keep the raw hex co-located with where
// it's consumed instead of re-deriving it from output.BrandGreen.
const brandGreenHex = "#46be52"

// ─── tea.Model implementation ──────────────────────────────────────────────

func (r *Root) Init() tea.Cmd {
	// IMPORTANT: refreshSubscriptions() must run BEFORE feed.start()
	// is appended to cmds. start() returns a closure that reads
	// f.pending; if pending is empty when the closure executes,
	// wsclient.Dial gets no channels and the dashboard sits forever
	// on "subscribed · waiting…" with nothing to receive.
	//
	// Order matters even though Cmds are deferred — the closure
	// captures f by pointer and reads f.pending lazily, but we
	// previously called refreshSubscriptions AFTER constructing the
	// cmds slice. That ordering created a race where start() could
	// fire before refreshSubscriptions had populated pending.
	r.refreshSubscriptions()

	cmds := []tea.Cmd{
		r.feed.start(),
		r.spinner.Tick,
	}
	for _, p := range r.panels {
		if c := p.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if dashDebug {
		fmt.Fprintf(os.Stderr, "[dash] Init: pending channels = %v\n", r.feed.pending)
	}
	return tea.Batch(cmds...)
}

// dashDebug enables stderr trace logs from the dashboard kernel.
// Off by default; set LAEVITAS_DASH_DEBUG=1 to turn on. Logs are
// written to stderr which the alt-screen doesn't paint over —
// they appear after the program exits.
var dashDebug = os.Getenv("LAEVITAS_DASH_DEBUG") == "1"

// maskKey returns a key string safe to print in debug logs:
// "abcd…wxyz" for normal-length keys, "" for empty. Avoids
// committing api keys to a screenshot when LAEVITAS_DASH_DEBUG
// is on during a live debugging session.
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "…" + k[len(k)-4:]
}

func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		// Route every key through the shared keymap vocabulary, then
		// switch on the resulting Action — same dispatch shape every
		// TUI surface uses, so adding a global key means editing
		// internal/keymap.ClassifyKey and updating one switch case
		// here.
		//
		// Action-not-recognised falls through to the focused panel,
		// which is also free to ignore the key. That's how
		// panel-specific keys like +/- (depth tier) work without
		// the kernel knowing about them — kernel sees ActDepthUp,
		// has no case for it, hands the raw KeyMsg to the panel.
		switch keymap.ClassifyKey(m.String()) {
		case keymap.ActQuit:
			r.quitting = true
			return r, tea.Sequence(r.feed.stop(), tea.Quit)
		case keymap.ActHelp:
			r.helpOpen = !r.helpOpen
			return r, nil
		case keymap.ActEsc:
			if r.helpOpen {
				r.helpOpen = false
				return r, nil
			}
			// Otherwise fall through to focused panel — Esc may have
			// panel-specific meaning (back out of a drill-down).
			return r, r.dispatchToFocused(msg)
		case keymap.ActCycleFocus:
			if r.activeCapabilities().MultiPane {
				r.cycleFocus(+1)
			}
			return r, nil
		case keymap.ActReverseFocus:
			if r.activeCapabilities().MultiPane {
				r.cycleFocus(-1)
			}
			return r, nil
		case keymap.ActJumpPane1:
			if _, ok := r.panels[PaneMain]; ok {
				r.focused = PaneMain
			}
			return r, nil
		case keymap.ActJumpPane2:
			if _, ok := r.panels[PaneSide]; ok {
				r.focused = PaneSide
			}
			return r, nil
		case keymap.ActJumpPane3:
			if _, ok := r.panels[PaneStrip]; ok {
				r.focused = PaneStrip
			}
			return r, nil
		}
		// Anything else (panel-specific actions like ActDepthUp, or
		// ActNone keys we don't recognise) routes to the focused
		// panel for it to decide.
		return r, r.dispatchToFocused(msg)

	case tea.MouseMsg:
		// Wheel = scroll. Always. That's what every TUI does and
		// what every user expects. Earlier dev mapped wheel to
		// depth-tier cycle, which conflated two concepts (scroll
		// vs zoom) into one keystroke — exactly the kind of
		// overloading we just refactored out of the keymap.
		//
		// ListNav surfaces get arrow-key dispatch on wheel; the
		// panel decides what scroll means in its context (book
		// ladder: viewport offset; future screener: cursor row).
		// Surfaces without ListNav simply ignore the wheel.
		//
		// Click events stay un-consumed so the terminal keeps
		// native click-drag-to-select for copy-paste.
		caps := r.activeCapabilities()
		if !caps.ListNav {
			return r, nil
		}
		switch keymap.ClassifyMouse(m.Button) {
		case keymap.ActWheelUp:
			return r, r.dispatchToFocused(tea.KeyMsg(tea.Key{Type: tea.KeyUp}))
		case keymap.ActWheelDown:
			return r, r.dispatchToFocused(tea.KeyMsg(tea.Key{Type: tea.KeyDown}))
		}
		return r, nil

	case tea.WindowSizeMsg:
		r.width, r.height = m.Width, m.Height
		return r, r.broadcast(msg)

	case SelectionChangedMsg:
		// Mutate r.selection BEFORE broadcasting so panels reading
		// PanelContext or calling Subscriptions(sel) see the new state.
		// The msg's Old/New fields are the authoritative pair; we
		// install msg.New unconditionally rather than computing a diff
		// here so senders don't have to coordinate with us about which
		// fields changed.
		//
		// Without this write, the broadcast/refreshSubscriptions pair
		// downstream would still be working off the previous selection
		// — drill-down across panels (e.g. screener → detail) would
		// silently fail to propagate. The kernel's contract is "send
		// this message and the selection updates"; that contract had
		// been documented but not implemented.
		r.selection = m.New
		cmd := r.broadcast(msg)
		r.refreshSubscriptions()
		return r, cmd

	case FeedTickMsg:
		// First event from the gateway means the connection is fully
		// alive. Promote the feed state from Subscribed/Reconnecting
		// to Healthy. Fan to every panel — hidden panels receive
		// ticks too so they're warm when re-focused.
		if dashDebug && r.feedState != FeedHealthy {
			fmt.Fprintf(os.Stderr, "[dash] first FeedTickMsg received: channel=%s\n", m.Event.Channel)
		}
		r.feedState = FeedHealthy
		return r, tea.Batch(r.broadcast(msg), r.feed.next())

	case feedErrorMsg:
		// Soft error from the feed goroutine. Record the message text
		// for PanelContext.LastError; the error itself doesn't change
		// feed state (a transient decode failure is recoverable). The
		// FeedRouter signals real connection problems via feedStateMsg.
		if m.err != nil {
			r.lastErr = m.err.Error()
		}
		return r, r.feed.next()

	case feedStateMsg:
		// FeedRouter announces a phase change (dialing → subscribed →
		// healthy → reconnecting → fatal). The header pill and any
		// per-panel "loading" placeholders read this directly.
		//
		// CRITICAL: when the transition leaves us in a connected
		// state (Subscribed / Healthy / Reconnecting) we must hand
		// back next() so the pump goroutine's events get pulled
		// into the Bubble Tea loop. start()'s very first return
		// is feedStateMsg{Subscribed} — without this, the pump
		// fills with FeedTickMsgs that never reach Update().
		//
		// Fatal is the one terminal state; nothing more will arrive,
		// so don't re-arm.
		r.feedState = m.state
		if m.err != nil {
			r.lastErr = m.err.Error()
		}
		if m.state == FeedFatal {
			return r, nil
		}
		return r, r.feed.next()

	case spinner.TickMsg:
		// Spinner advances one frame and re-arms its own tick. This
		// is the single source of animation across the dashboard;
		// panels never tick their own spinners — they read the
		// current frame from PanelContext at View() time.
		var cmd tea.Cmd
		r.spinner, cmd = r.spinner.Update(msg)
		return r, cmd
	}

	return r, nil
}

// panelContext builds the per-render handoff struct passed to every
// panel's View(). Built fresh each frame from kernel state so panels
// always see a consistent snapshot.
//
// focusedSlot is the slot the rendered panel occupies — passed in
// by the caller so we can set ctx.Focused correctly per panel
// without coupling this method to the layout logic.
func (r *Root) panelContext(slot PaneSlot) PanelContext {
	return PanelContext{
		SpinnerFrame: r.spinner.View(),
		FeedState:    r.feedState,
		Focused:      slot == r.focused,
		LastError:    r.lastErr,
	}
}

func (r *Root) View() string {
	if r.quitting {
		return ""
	}
	if r.helpOpen {
		return r.renderHelp()
	}
	return r.renderLayout()
}

// ─── routing helpers ───────────────────────────────────────────────────────

// dispatchToFocused sends a message to the focused panel only. Used
// for key events that should only affect the active pane (j/k, Enter,
// etc.).
func (r *Root) dispatchToFocused(msg tea.Msg) tea.Cmd {
	p, ok := r.panels[r.focused]
	if !ok {
		return nil
	}
	updated, cmd := p.Update(msg)
	r.panels[r.focused] = updated
	return cmd
}

// broadcast sends a message to every panel and batches their commands.
// Used for resize and data-fan-out — anything every panel needs to
// see regardless of focus.
func (r *Root) broadcast(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(r.panels))
	for slot, p := range r.panels {
		updated, cmd := p.Update(msg)
		r.panels[slot] = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// refreshSubscriptions collects every panel's current Subscriptions()
// list, deduplicates, and asks the feed router to align its live
// subscription set. Called on Init and after every selection change.
func (r *Root) refreshSubscriptions() {
	seen := make(map[string]struct{})
	for _, p := range r.panels {
		spec := p.Subscriptions(r.selection)
		for _, ch := range spec.Channels {
			seen[ch] = struct{}{}
		}
	}
	channels := make([]string, 0, len(seen))
	for ch := range seen {
		channels = append(channels, ch)
	}
	r.feed.subscribe(channels)
}

// cycleFocus rotates focus to the next/previous panel in slot-enum
// order (1 = main, 2 = side, 3 = strip). Skips empty slots so Tab
// only lands on actual panels.
func (r *Root) cycleFocus(direction int) {
	order := []PaneSlot{PaneMain, PaneSide, PaneStrip}
	idx := 0
	for i, s := range order {
		if s == r.focused {
			idx = i
			break
		}
	}
	for i := 0; i < len(order); i++ {
		idx = (idx + direction + len(order)) % len(order)
		if _, ok := r.panels[order[idx]]; ok {
			r.focused = order[idx]
			return
		}
	}
}

// ─── layout rendering ──────────────────────────────────────────────────────

const headerHeight = 1
const footerHeight = 1

// renderLayout composes the panel views per the configured
// LayoutKind. Each layout computes its own size budget per slot;
// panels render into width × height cells and the layout joins them
// with lipgloss.
//
// Sizes are computed defensively: if a slot is unpopulated we don't
// allocate space to it. That keeps `LayoutTriad` graceful when only
// PaneMain is provided (renders identically to LayoutSingle).
func (r *Root) renderLayout() string {
	// Title may be empty — that's the panel-prints-its-own-header
	// opt-out used by the book panel (it emits a shared
	// ladder.HeaderLine identical to the legacy ws book ladder, so
	// the kernel header would be a duplicate). Other panels keep
	// the kernel header for consistent dashboard chrome.
	skipHeader := r.cfg.Title == ""
	hdrH := headerHeight
	if skipHeader {
		hdrH = 0
	}

	innerW := r.width
	innerH := r.height - hdrH - footerHeight
	if innerH < 4 {
		innerH = 4
	}

	footer := r.renderFooter()

	var body string
	switch r.cfg.Layout {
	case LayoutSingle:
		body = r.renderSingle(innerW, innerH)
	case LayoutSplit:
		body = r.renderSplit(innerW, innerH)
	case LayoutTriad:
		body = r.renderTriad(innerW, innerH)
	}

	if skipHeader {
		return body + "\n" + footer
	}
	return r.renderHeader() + "\n" + body + "\n" + footer
}

func (r *Root) renderSingle(w, h int) string {
	p, ok := r.panels[PaneMain]
	if !ok {
		return placeholder(w, h, "(no main panel configured)")
	}
	return p.View(w, h, r.panelContext(PaneMain))
}

func (r *Root) renderSplit(w, h int) string {
	main, hasMain := r.panels[PaneMain]
	side, hasSide := r.panels[PaneSide]
	if !hasSide {
		return r.renderSingle(w, h)
	}
	mainW := w * 60 / 100
	sideW := w - mainW - 1 // 1-cell gutter
	if mainW < 20 || sideW < 20 {
		// Terminal too narrow for split — collapse to main only and
		// let the user widen.
		return main.View(w, h, r.panelContext(PaneMain))
	}
	mv := main.View(mainW, h, r.panelContext(PaneMain))
	sv := side.View(sideW, h, r.panelContext(PaneSide))
	if !hasMain {
		mv = placeholder(mainW, h, "(no main panel)")
	}
	gutter := strings.Repeat(" ", 1)
	return lipgloss.JoinHorizontal(lipgloss.Top, mv, gutter, sv)
}

func (r *Root) renderTriad(w, h int) string {
	main, hasMain := r.panels[PaneMain]
	side, hasSide := r.panels[PaneSide]
	strip, hasStrip := r.panels[PaneStrip]
	if !hasStrip {
		return r.renderSplit(w, h)
	}
	stripH := h * 25 / 100
	if stripH < 4 {
		stripH = 4
	}
	upperH := h - stripH - 1
	if upperH < 4 {
		upperH = 4
	}
	var upper string
	if hasSide {
		mainW := w * 60 / 100
		sideW := w - mainW - 1
		mv := placeholder(mainW, upperH, "(no main panel)")
		if hasMain {
			mv = main.View(mainW, upperH, r.panelContext(PaneMain))
		}
		sv := side.View(sideW, upperH, r.panelContext(PaneSide))
		upper = lipgloss.JoinHorizontal(lipgloss.Top, mv, " ", sv)
	} else if hasMain {
		upper = main.View(w, upperH, r.panelContext(PaneMain))
	} else {
		upper = placeholder(w, upperH, "(no upper panels)")
	}
	stripV := strip.View(w, stripH, r.panelContext(PaneStrip))
	return lipgloss.JoinVertical(lipgloss.Left, upper, "", stripV)
}

func placeholder(w, h int, label string) string {
	style := lipgloss.NewStyle().
		Width(w).Height(h).
		Align(lipgloss.Center, lipgloss.Center).
		Foreground(lipgloss.Color("#475057"))
	return style.Render(label)
}

// renderHeader is a single-line dashboard header showing title +
// active selection summary. Brand-styled to match the rest of the
// CLI's TUI surfaces.
func (r *Root) renderHeader() string {
	bold := output.Bold
	green := output.BrandGreen
	grey := output.BrandGreyMid
	reset := output.Reset

	title := r.cfg.Title
	if title == "" {
		title = "dashboard"
	}
	sel := summariseSelection(r.selection)
	if sel != "" {
		sel = grey + " · " + sel + reset
	}
	return bold + green + "▲ " + title + reset + sel + r.renderFeedPill()
}

// renderFeedPill returns a small connection-state badge appended to
// the header. Hidden when the feed is healthy (the dashboard speaks
// for itself); shown for any other state with the spinner glyph
// when relevant. Three visual treatments:
//
//	dialing       grey  ⠼ connecting…
//	subscribed    grey  ⠼ subscribed · waiting for first event…
//	healthy       (no pill — the live data IS the signal)
//	reconnecting  yellow ⠼ reconnecting…
//	fatal         red   ✗ disconnected
//
// Single source for "what's the connection doing" — both the header
// and panel-internal "loading" placeholders read FeedState, so the
// dashboard never disagrees with itself.
func (r *Root) renderFeedPill() string {
	grey := output.BrandGreyMid
	yellow := output.Yellow
	red := output.Red
	reset := output.Reset
	frame := r.spinner.View()

	switch r.feedState {
	case FeedHealthy:
		return ""
	case FeedDialing:
		return "   " + grey + frame + " connecting…" + reset
	case FeedSubscribed:
		return "   " + grey + frame + " subscribed · waiting…" + reset
	case FeedReconnecting:
		return "   " + yellow + frame + " reconnecting…" + reset
	case FeedFatal:
		msg := "disconnected"
		if r.lastErr != "" {
			msg = "disconnected · " + r.lastErr
		}
		return "   " + red + "✗ " + msg + reset
	}
	return ""
}

// renderFooter delegates to keymap.FooterHints with the active
// surface's effective capabilities. "Effective" = the active panel's
// declared capabilities OR'd with kernel-derived flags (MultiPane
// when more than one slot is populated). That's why a single-pane
// book dashboard's footer stops advertising tab/1/2/3 — the
// MultiPane flag is false on this surface, so keymap.FooterHints
// hides those hints automatically.
func (r *Root) renderFooter() string {
	return output.BrandGreyMid + keymap.FooterHints(r.activeCapabilities()) + output.Reset
}

// activeCapabilities builds the Capabilities value the keymap
// module uses to decide which footer hints + help-overlay sections
// to show. The active panel declares its own (depth tier, list nav,
// drill, etc.); the kernel adds the layout-level flags (MultiPane,
// always-on Pause + Help).
//
// Single source of truth: every keymap-aware kernel render
// (footer, help overlay, key dispatch) derives from this method
// rather than recomputing per-call.
func (r *Root) activeCapabilities() keymap.Capabilities {
	caps := keymap.Capabilities{Pause: true, Help: true}
	if len(r.panels) > 1 {
		caps.MultiPane = true
	}
	if p, ok := r.panels[r.focused]; ok {
		pc := p.Capabilities()
		caps.ListNav = caps.ListNav || pc.ListNav
		caps.Drill = caps.Drill || pc.Drill
		caps.Back = caps.Back || pc.Back
		caps.Group = caps.Group || pc.Group
		caps.DepthCycle = caps.DepthCycle || pc.DepthCycle
		caps.Recenter = caps.Recenter || pc.Recenter
		caps.LadderMode = caps.LadderMode || pc.LadderMode
		caps.VenueToggle = caps.VenueToggle || pc.VenueToggle
		// Pause/Help/MultiPane stay kernel-controlled — panels can't
		// suppress them. (A panel that doesn't support pause just
		// no-ops the action.)
	}
	return caps
}

// renderHelp delegates to keymap.RenderHelpOverlay so the dashboard
// help shows exactly the same sections the footer advertises —
// derived from the same activeCapabilities() value. No more
// duplicated binding tables; adding a key in keymap.go propagates
// here automatically.
func (r *Root) renderHelp() string {
	bold, green, grey, light, reset := output.HelpStyleStrings()
	style := keymap.HelpStyle{Bold: bold, Green: green, Grey: grey, LightGrey: light, Reset: reset}
	return keymap.RenderHelpOverlay(r.cfg.Title, r.activeCapabilities(), style, r.width)
}

func summariseSelection(s Selection) string {
	parts := []string{}
	if s.Symbol != "" {
		parts = append(parts, s.Symbol)
	}
	if s.Currency != "" && s.Symbol == "" {
		parts = append(parts, s.Currency)
	}
	if s.Expiry != "" {
		parts = append(parts, s.Expiry)
	}
	if s.Strike > 0 {
		parts = append(parts, fmt.Sprintf("%g", s.Strike))
	}
	if s.Venue != "" {
		parts = append(parts, s.Venue)
	}
	return strings.Join(parts, " ")
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// ─── feed router ───────────────────────────────────────────────────────────

// feedErrorMsg is delivered when the feed goroutine encounters a
// soft error (reconnect, decode failure). The root acks it and
// re-arms the next-tick wait so events keep flowing.
type feedErrorMsg struct{ err error }

// feedStateMsg announces a connection-state phase change from the
// FeedRouter to the Root. This is how the header's connection pill
// transitions from "connecting" → "subscribed" → "live" without the
// kernel having to poll the router.
//
// err is set when transitioning to FeedFatal (carries the
// underlying reason); otherwise nil.
type feedStateMsg struct {
	state FeedState
	err   error
}

// FeedRouter owns the wsclient connection and bridges its events
// into the Bubble Tea message loop. It runs the WS pump in a
// goroutine; the model pulls events one at a time via re-arming
// tea.Cmd (the realtime-example pattern from the Charm docs).
//
// Lifecycle:
//   - newFeedRouter: store config, no connection yet.
//   - start(): returns a Cmd that dials the WS and emits the first
//     wait-for-tick. Called from Root.Init().
//   - subscribe(channels): align the live subscription set to the
//     given list. Idempotent.
//   - next(): the re-arming Cmd called after every FeedTickMsg.
//   - stop(): cancel context + close client. Returned as part of
//     the Quit cmd sequence.
type FeedRouter struct {
	apiKey     string
	gatewayURL string

	ctx    context.Context
	cancel context.CancelFunc

	cli  *wsclient.Client
	pump chan tea.Msg

	// mu guards every read and write of cli, current, and pending —
	// Root.Update goroutine and start()'s tea.Cmd goroutine race on
	// all three during the dial window AND across the post-dial
	// reconciliation. Specifically:
	//   - pending: written by subscribe() pre-dial, read by start()
	//     to seed wsclient.Dial AND to capture latest-desired-set
	//     for post-dial reconciliation.
	//   - cli: written once by start() after dial succeeds; read by
	//     subscribe()/diffAndInstallLocked and by stop().
	//   - current: written by start() during post-dial install AND
	//     by every diffAndInstallLocked call; read by both. Without
	//     coverage here, a subscribe() could observe (cli != nil
	//     AND current empty) during the post-dial install window.
	//
	// Held across wsclient RPC calls only during start()'s post-dial
	// reconciliation (startup-only, no concurrent traffic blocked in
	// practice). Regular subscribe() releases the lock before firing
	// RPCs — see diffAndInstallLocked + subscribe.
	mu      sync.Mutex
	pending []string

	// current holds the channel set the wsclient layer is currently
	// subscribed to from FeedRouter's perspective. Treated as desired
	// state, not confirmed wire state — wsclient's own state machine
	// reconciles toward it asynchronously via subscribe/unsubscribe
	// RPCs. All reads and writes go through mu.
	current map[string]struct{}
}

func newFeedRouter(apiKey, gatewayURL string) *FeedRouter {
	if gatewayURL == "" {
		gatewayURL = wsclient.NativeURL
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &FeedRouter{
		apiKey:     apiKey,
		gatewayURL: gatewayURL,
		ctx:        ctx,
		cancel:     cancel,
		pump:       make(chan tea.Msg, 256),
		current:    make(map[string]struct{}),
	}
}

// start dials the WS and launches the pump goroutine. Returns the
// Cmd that produces the first message — either a feedStateMsg
// (dial succeeded; transition to Subscribed) or feedErrorMsg (dial
// failed; transition to Fatal).
//
// The initial channel set comes from f.pending — Root.Init calls
// refreshSubscriptions synchronously *before* this Cmd executes,
// so any panel's Subscriptions() result is already stashed by
// then. Passing the channels into wsclient.Dial means the gateway
// receives the subscribe request as part of the upgrade handshake;
// without this, the dashboard would dial successfully but never
// subscribe to anything, which is exactly the "stuck on waiting"
// bug we just hit on the first BookPanel run.
//
// The kernel's Update() handles either result; subsequent messages
// are pulled by the re-arming next() Cmd.
func (f *FeedRouter) start() tea.Cmd {
	return func() tea.Msg {
		// Snapshot pending under lock — without this, subscribe() can
		// race with our read here. We dial against this snapshot, then
		// reconcile any further pending arrivals after dial completes
		// so the latest-desired set wins even if subscribe() raced us.
		f.mu.Lock()
		dialChannels := append([]string(nil), f.pending...)
		f.mu.Unlock()

		if dashDebug {
			fmt.Fprintf(os.Stderr, "[dash] FeedRouter.start: dialing with channels=%v apiKey=%s url=%s\n",
				dialChannels, maskKey(f.apiKey), f.gatewayURL)
		}
		cli, err := wsclient.Dial(f.ctx, wsclient.Config{
			URL:      f.gatewayURL,
			APIKey:   f.apiKey,
			Channels: dialChannels,
		})
		if err != nil {
			// Dial failure is fatal — no events will ever arrive.
			// Surface as a state transition so the header reflects
			// "disconnected" rather than a bare error toast.
			return feedStateMsg{state: FeedFatal, err: err}
		}

		// Post-dial install + reconciliation must happen under one
		// critical section, INCLUDING the wsclient RPC calls for
		// reconciliation. Two distinct races would otherwise occur:
		//
		//  1) Visibility race: between f.cli=cli and the f.current
		//     population, an Update-goroutine subscribe() call could
		//     observe cli != nil with empty f.current and compute the
		//     wrong diff baseline.
		//
		//  2) Stale-pending race: if we unlock after capturing
		//     latestPending and a concurrent subscribe(want=A)
		//     races our reconciliation, one of two bad outcomes lands
		//     depending on ordering:
		//       (a) reconciliation overwrites f.current=A back to
		//           latestPending, losing the user's newer intent.
		//       (b) RPC ordering at the wsclient level interleaves
		//           start()'s startupAdd/Remove with subscribe()'s,
		//           briefly leaving channels missing.
		//
		// Holding the FeedRouter mutex across the wsclient RPC calls
		// here is acceptable because this is startup-only — there's no
		// concurrent traffic to block in practice. The wsclient layer
		// never calls back into FeedRouter, so no deadlock risk.
		f.mu.Lock()
		f.cli = cli
		for _, ch := range dialChannels {
			f.current[ch] = struct{}{}
		}
		latestPending := append([]string(nil), f.pending...)
		f.pending = nil
		if !channelSetsEqual(latestPending, dialChannels) {
			wantSet := make(map[string]struct{}, len(latestPending))
			for _, ch := range latestPending {
				wantSet[ch] = struct{}{}
			}
			var add, remove []string
			for ch := range wantSet {
				if _, has := f.current[ch]; !has {
					add = append(add, ch)
				}
			}
			for ch := range f.current {
				if _, keep := wantSet[ch]; !keep {
					remove = append(remove, ch)
				}
			}
			f.current = wantSet
			// Fire the wsclient RPCs WHILE STILL HOLDING the lock —
			// any concurrent Update-goroutine subscribe() blocks until
			// we're done so it sees the post-reconciliation state and
			// doesn't get stomped.
			if len(add) > 0 {
				_ = cli.Subscribe(add...)
			}
			if len(remove) > 0 {
				_ = cli.Unsubscribe(remove...)
			}
		}
		f.mu.Unlock()

		// Forward events into our pump channel.
		go func() {
			for ev := range cli.Events() {
				select {
				case f.pump <- FeedTickMsg{Event: ev}:
				case <-f.ctx.Done():
					return
				}
			}
		}()
		// Forward soft errors too.
		go func() {
			for e := range cli.Errs() {
				select {
				case f.pump <- feedErrorMsg{err: e}:
				case <-f.ctx.Done():
					return
				}
			}
		}()

		// Dial succeeded; we're subscribed but no events have
		// arrived yet. The first FeedTickMsg will promote us to
		// Healthy in the kernel's handler.
		return feedStateMsg{state: FeedSubscribed}
	}
}

// next is the re-arming Cmd handed back from the model after each
// FeedTickMsg. Pulls one message off the pump and returns it.
func (f *FeedRouter) next() tea.Cmd {
	return func() tea.Msg {
		return f.waitOnce()
	}
}

func (f *FeedRouter) waitOnce() tea.Msg {
	select {
	case msg := <-f.pump:
		return msg
	case <-f.ctx.Done():
		return nil
	}
}

// subscribe aligns the live subscription set to exactly `want`.
// Channels in `want` that are not currently subscribed are added;
// channels currently subscribed that are not in `want` are removed.
// Idempotent — calling with the same set twice in a row sends no
// RPCs the second time.
//
// Pre-dial: subscribe stashes the desired set into f.pending instead
// of returning silently. start()'s tea.Cmd reads pending when it
// dials, passing the full channel list to wsclient.Dial as part of
// the upgrade handshake — so the dashboard receives events from the
// very first tick rather than dialing then waiting indefinitely.
//
// Drift-free contract: the desired set is exactly `want`, no more no
// less. Earlier versions only added channels (the unsubscribe path
// was deferred); panels that rotated their subscription set as the
// user navigated would accumulate forever, eventually tripping the
// gateway's 200-subs-per-connection cap.
// channelSetsEqual reports whether two channel-name slices represent
// the same set (order-independent, duplicates collapsed). Used by
// start() to decide whether the latest pending differs from what we
// dialed with, gating the post-dial reconciliation pass.
//
// Always dedupes both inputs into sets and compares — earlier same-
// length fast path was wrong for cases like ["a","b"] vs ["a","a"]
// (would have returned true). Production callers don't currently
// produce duplicates, but the helper's contract claims it handles
// them, so it does.
func channelSetsEqual(a, b []string) bool {
	aSet := make(map[string]struct{}, len(a))
	for _, x := range a {
		aSet[x] = struct{}{}
	}
	bSet := make(map[string]struct{}, len(b))
	for _, x := range b {
		bSet[x] = struct{}{}
	}
	if len(aSet) != len(bSet) {
		return false
	}
	for x := range aSet {
		if _, ok := bSet[x]; !ok {
			return false
		}
	}
	return true
}

func (f *FeedRouter) subscribe(want []string) {
	// All access to f.cli, f.current, and f.pending goes through the
	// mutex — start() can race with Update-goroutine subscribe() calls
	// up to and including the post-dial reconciliation, where f.current
	// is being populated by start() concurrently with subscribe()
	// reading it.
	//
	// We compute the diff and install the new f.current under the lock,
	// then release the lock before issuing the actual Subscribe /
	// Unsubscribe RPCs to wsclient — those are network ops and shouldn't
	// hold the FeedRouter mutex while they run.
	add, remove, cli := f.diffAndInstallLocked(want)
	if cli == nil {
		// Pre-dial: diffAndInstallLocked already stashed `want` into
		// f.pending; nothing else to do.
		return
	}
	if len(add) > 0 {
		_ = cli.Subscribe(add...)
	}
	if len(remove) > 0 {
		_ = cli.Unsubscribe(remove...)
	}
}

// diffAndInstallLocked atomically reads f.cli, computes add/remove
// vs f.current, and installs the new f.current. Pre-dial it stashes
// the desired set into f.pending and returns nil cli + nil deltas
// (caller is a no-op). Returns the cli ref so the caller can issue
// the actual RPCs after releasing the lock.
func (f *FeedRouter) diffAndInstallLocked(want []string) (add, remove []string, cli *wsclient.Client) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cli == nil {
		// Pre-dial. Replace pending — Root.Init may call
		// refreshSubscriptions multiple times before dial completes; we
		// want the latest set, not an accumulation.
		f.pending = append(f.pending[:0], want...)
		return nil, nil, nil
	}

	wantSet := make(map[string]struct{}, len(want))
	for _, ch := range want {
		wantSet[ch] = struct{}{}
	}
	for ch := range wantSet {
		if _, has := f.current[ch]; !has {
			add = append(add, ch)
		}
	}
	for ch := range f.current {
		if _, keep := wantSet[ch]; !keep {
			remove = append(remove, ch)
		}
	}
	f.current = wantSet
	return add, remove, f.cli
}

// stop cancels the feed context and closes the WS. Returned as a
// Cmd in the Quit sequence so the goroutine drains cleanly before
// the program exits.
//
// Reads f.cli under the mutex because start()'s tea.Cmd may still
// be writing it concurrently (user hits 'q' during the dial window).
// Copy out the cli ref under the lock, release, then close — Close
// is a long-ish op (writes a close frame) that shouldn't be holding
// the FeedRouter mutex.
func (f *FeedRouter) stop() tea.Cmd {
	return func() tea.Msg {
		f.cancel()
		f.mu.Lock()
		cli := f.cli
		f.mu.Unlock()
		if cli != nil {
			_ = cli.Close()
		}
		return nil
	}
}
