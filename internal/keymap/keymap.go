// Package keymap is the single source of truth for every TUI
// keybinding in the CLI — rolling tape, book scan, book ladder,
// dashboard kernel, dashboard panels.
//
// Two principles, both load-bearing:
//
//  1. ONE vocabulary. q always quits. p always pauses. j/k always
//     moves the cursor. Same key, same action, regardless of which
//     surface the user is on. A user (or an agent) learns the keys
//     once and they generalise everywhere.
//
//  2. Footer + help overlay adapt to the SURFACE'S CAPABILITIES,
//     not to a hardcoded surface name. A single-pane dashboard
//     hides multi-pane keys from the footer because they have
//     nothing to do — but the keys themselves still exist in the
//     vocabulary and would work the moment a multi-pane layout
//     starts using them. Footer "lying" was the bug we fixed by
//     extracting this — the dashboard used to advertise tab/1/2/3
//     on a single-pane book view where they did nothing visible.
//
// Adding a new key:
//  1. Append a new Action constant.
//  2. Add the key string(s) to classifyKey.
//  3. Add a Binding entry under the section it belongs to.
//  4. (If the key is conditional) extend Capabilities to gate it.
//
// Every renderer / kernel / panel automatically picks it up.
package keymap

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Action is the logical "what is the user trying to do" produced by
// classifyKey / classifyMouse. Renderers switch on Action rather than
// raw key strings so the vocabulary stays declarative.
type Action int

const (
	ActNone Action = iota
	ActQuit
	ActPause
	ActHelp
	ActEsc
	ActUp
	ActDown
	ActPageUp
	ActPageDown
	ActTop
	ActBottom
	ActEnter

	// Price-grouping actions — `+` widens, `-` narrows. Used by the
	// book ladder to bucket adjacent ticks (e.g. show every $0.10
	// vs every $1 vs every $10). Renamed from ActDepthUp/Down in
	// v0.8.3 because the previous name conflated three orthogonal
	// concepts (stats depth, viewport scroll, price grouping). Now
	// each user intent gets its own action.
	ActGroupUp
	ActGroupDown

	// Stats-depth cycle — `d` rotates through pre-computed wire
	// tiers (10 → 20 → 50 → 100). Affects which liquidity/imbalance
	// numbers the strip + CONSOLIDATED block read from each
	// venue's snapshot; does NOT change the rendered ladder row
	// count.
	ActDepthCycle

	// Venue toggle — `v` opens an inline venue picker that lets
	// the user hide / show specific venues in the aggregated
	// ladder + venue strip. State persists across renders until
	// the user toggles back.
	ActVenueToggle

	// Recenter — `c` snaps the viewport back to the spread
	// separator. Useful muscle-memory key on book ladders after
	// scrolling deep into one side; without it the user would
	// have to count rows back to the spread by hand.
	ActRecenter

	// Chart timeframe cycle — `t` rotates chart panes through
	// 1m → 5m → 15m → 1h. Panels derive coarser candles from a
	// canonical 1m stream so live updates stay consistent.
	ActTimeframeCycle

	// Search — `/` opens an in-surface filter prompt. Currently
	// used by flow screeners to filter instrument tables.
	ActSearch

	// Ladder-mode toggle — `m` cycles between aggregated and split
	// presentations of the same multi-venue book. Aggregated merges
	// every venue's depth into one centre-price ladder coloured by
	// venue contribution; split shows one narrow per-venue column
	// side-by-side so the user can compare individual venue
	// liquidity at a glance. Same data, different lens.
	ActLadderMode

	ActWheelUp
	ActWheelDown

	// Multi-pane / focus actions. Defined here so the vocabulary is
	// shared across every renderer; only surfaces that opt into
	// MultiPane in their Capabilities will show them in the footer.
	ActCycleFocus
	ActReverseFocus
	ActJumpPane1
	ActJumpPane2
	ActJumpPane3
	ActJumpPane4
)

// ClassifyKey maps a Bubble Tea key string to an Action. Returns
// ActNone when the key isn't in the vocabulary; callers should
// ignore it (don't fall through to surface-specific behaviour —
// add a key to the vocabulary instead).
func ClassifyKey(key string) Action {
	switch key {
	case "q", "Q", "ctrl+c":
		return ActQuit
	case "p", "P":
		return ActPause
	case "?", "h", "H":
		return ActHelp
	case "esc":
		return ActEsc
	case "up", "k":
		return ActUp
	case "down", "j":
		return ActDown
	case "pgup", "b":
		return ActPageUp
	case "pgdown", "f":
		return ActPageDown
	case "home", "g":
		return ActTop
	case "end", "G":
		return ActBottom
	case "enter":
		return ActEnter
	case "+", "=":
		return ActGroupUp
	case "-", "_":
		return ActGroupDown
	case "d":
		return ActDepthCycle
	case "v":
		return ActVenueToggle
	case "c":
		return ActRecenter
	case "t", "T":
		return ActTimeframeCycle
	case "/":
		return ActSearch
	case "m":
		return ActLadderMode
	case "tab":
		return ActCycleFocus
	case "shift+tab":
		return ActReverseFocus
	case "1":
		return ActJumpPane1
	case "2":
		return ActJumpPane2
	case "3":
		return ActJumpPane3
	case "4":
		return ActJumpPane4
	}
	return ActNone
}

// ClassifyMouse maps a Bubble Tea mouse button to a wheel Action,
// or ActNone for click events we deliberately don't consume (so
// the terminal keeps native click-drag-to-select for copy-paste).
func ClassifyMouse(btn tea.MouseButton) Action {
	switch btn {
	case tea.MouseButtonWheelUp:
		return ActWheelUp
	case tea.MouseButtonWheelDown:
		return ActWheelDown
	}
	return ActNone
}

// ─── capabilities ──────────────────────────────────────────────────────────

// Capabilities describes what a TUI surface supports. The footer and
// help overlay use this to decide which keys to advertise — keys not
// gated by a capability flag (q, p, ?, esc) are always shown because
// they work everywhere.
//
// Conventions:
//   - ListNav: surface has a cursor / scrollable rows (book scan,
//     book ladder, future screener); enables ↑↓/jk/PgUp/PgDn/g/G +
//     wheel scroll.
//   - Drill: surface lets Enter open a detail view (scan → ladder).
//   - Back: surface lets Esc back out (drill-down ladder → scan);
//     paired with Drill on the parent surface.
//   - Group: surface respects +/− to widen/narrow price grouping.
//     Used by the book ladder to bucket adjacent ticks.
//   - DepthCycle: surface respects `d` to cycle book stats depth
//     (10 → 20 → 50 → 100). Affects strip/CONSOLIDATED math; doesn't
//     change the rendered ladder row count.
//   - VenueToggle: surface respects `v` to hide/show specific
//     venues. Used by the book ladder + strip.
//   - MultiPane: surface has more than one pane; enables tab/1/2/3.
//   - Pause: surface supports pause/resume; almost always true, but
//     panels with no continuous state (e.g. a static settings view)
//     can opt out so `p` becomes a no-op there too.
type Capabilities struct {
	ListNav bool
	Drill   bool
	Back    bool

	// Group — `+/-` widens / narrows price grouping. Useful on
	// surfaces that aggregate multiple venues' books (the dashboard
	// book panel) where overlapping prices collapse into one
	// bucket per `+` press. Mutually exclusive with DepthTier in
	// practice; both gate `+/-` so a surface picks one footer
	// label.
	Group bool

	// DepthTier — `+/-` cycles the stats depth tier (10 / 20 / 50 / 100).
	// Used by the legacy single-venue ladder where price grouping
	// is marginal value (the wire payload caps at 100 levels) but
	// tier-cycling has been shipped behaviour since v0.8.0. Footer
	// reads `+/- depth` instead of `+/- group`.
	DepthTier bool

	// DepthCycle — `d` cycles the stats depth tier independently
	// of `+/-`. Used by the dashboard book panel so the user gets
	// three orthogonal `+/-` (group), `d` (depth), arrow-key
	// (scroll) controls.
	DepthCycle bool

	VenueToggle bool

	// Recenter — `c` snaps the viewport back to the spread
	// separator. Used by both book surfaces (legacy ladder +
	// dashboard panel) so the muscle memory carries.
	Recenter bool

	// ChartTimeframe — `t` cycles chart timeframe views.
	ChartTimeframe bool

	// Search — `/` opens a table/list filter prompt.
	Search bool

	// LadderMode — `m` toggles aggregated ↔ split presentation of
	// a multi-venue book. Only the dashboard book panel sets this
	// today; the legacy single-venue ladder has nothing to split.
	LadderMode bool

	MultiPane bool
	Pause     bool
	Help      bool // when false, ? is a no-op (rare)
}

// FullCapabilities is the everything-on preset for surfaces that
// support every feature (a multi-pane dashboard with a list panel
// drilling into a detail panel).
func FullCapabilities() Capabilities {
	return Capabilities{
		ListNav: true, Drill: true, Back: true,
		Group: true, DepthCycle: true, VenueToggle: true,
		Recenter: true, ChartTimeframe: true, Search: true, LadderMode: true,
		MultiPane: true, Pause: true, Help: true,
	}
}

// Union combines two Capabilities into one with each field set if
// either input had it set. Used by composite panels that need to
// declare the union of their children's capabilities so the kernel's
// footer hints reflect everything any child can do.
//
// Capabilities is a plain struct of bools rather than a bitfield, so
// composites can't just `|` two values together. This helper is the
// equivalent. New fields added to Capabilities should be added here
// in the same commit; missing one drops that capability when
// composing.
func (c Capabilities) Union(other Capabilities) Capabilities {
	return Capabilities{
		ListNav:        c.ListNav || other.ListNav,
		Drill:          c.Drill || other.Drill,
		Back:           c.Back || other.Back,
		Group:          c.Group || other.Group,
		DepthTier:      c.DepthTier || other.DepthTier,
		DepthCycle:     c.DepthCycle || other.DepthCycle,
		VenueToggle:    c.VenueToggle || other.VenueToggle,
		Recenter:       c.Recenter || other.Recenter,
		ChartTimeframe: c.ChartTimeframe || other.ChartTimeframe,
		Search:         c.Search || other.Search,
		LadderMode:     c.LadderMode || other.LadderMode,
		MultiPane:      c.MultiPane || other.MultiPane,
		Pause:          c.Pause || other.Pause,
		Help:           c.Help || other.Help,
	}
}

// ─── footer hints ──────────────────────────────────────────────────────────

// FooterHints returns the brand-grey one-line hint shown at the
// bottom of a surface. The output adapts to the surface's
// Capabilities — keys that have no effect aren't advertised, so
// users see "tab focus" only on multi-pane dashboards (where it
// actually does something), and "+/- depth" only on book surfaces
// that respect the depth tier.
//
// Order matters: keys are listed in the order users typically reach
// for them — navigation first, then surface-specific actions, then
// the always-available pause/help/quit trio at the end.
func FooterHints(c Capabilities) string {
	parts := []string{}

	if c.ListNav {
		// Verb is "scroll" not "select" because the book ladder
		// scrolls its viewport while the book scan moves a row
		// cursor; both share the keys, so a neutral verb covers
		// both. Surfaces that strongly want "select" wording can
		// override the footer themselves later.
		parts = append(parts, "↑↓/jk scroll")
		parts = append(parts, "pgup/pgdn page")
		parts = append(parts, "g/G top/end")
	}
	if c.Drill {
		parts = append(parts, "enter drill")
	}
	if c.Back {
		parts = append(parts, "esc back")
	}
	// Group and DepthTier are mutually exclusive labels for `+/-`.
	// Group wins when both are set so the dashboard's richer
	// vocabulary takes precedence.
	if c.Group {
		parts = append(parts, "+/- group")
	} else if c.DepthTier {
		parts = append(parts, "+/- depth")
	}
	if c.DepthCycle {
		parts = append(parts, "d depth")
	}
	if c.Recenter {
		parts = append(parts, "c recenter")
	}
	if c.ChartTimeframe {
		parts = append(parts, "t timeframe")
	}
	if c.Search {
		parts = append(parts, "/ search")
	}
	if c.LadderMode {
		parts = append(parts, "m mode")
	}
	if c.VenueToggle {
		parts = append(parts, "v venues")
	}
	if c.MultiPane {
		parts = append(parts, "tab focus")
		parts = append(parts, "1/2/3/4 jump")
		parts = append(parts, "enter expand")
	}
	if c.Pause {
		parts = append(parts, "p pause")
	}
	if c.Help {
		parts = append(parts, "? help")
	}
	parts = append(parts, "q quit")

	return joinHints(parts)
}

// joinHints concatenates with the standard 3-space separator that
// reads as a column gutter. Pulled out so it's trivial to swap if
// we ever decide e.g. " · " is cleaner.
func joinHints(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "   "
		}
		out += p
	}
	return out
}

// ─── help overlay binding tables ───────────────────────────────────────────

// Binding pairs a display string for the keys with a short
// description for the help overlay. Exported so renderers can build
// their own overlay layout if they want; the canonical renderer
// lives in internal/output (see book_format.go's renderHelpOverlay).
type Binding struct {
	Keys string
	Desc string
}

// Section is a labelled group of bindings — "Always", "Navigation",
// "Ladder", "Multi-pane" — so the help overlay can group related
// keys visually instead of one undifferentiated list.
type Section struct {
	Title    string
	Bindings []Binding
}

// commonBindings is the always-available set. Shown first in every
// help overlay regardless of capabilities.
var commonBindings = []Binding{
	{"q  Q  ctrl+c", "quit"},
	{"p  P", "pause / resume"},
	{"?  h  H", "toggle this help"},
	{"esc", "close help / back out"},
	{"wheel ↑ / ↓", "scroll (lists) or pause (tape)"},
}

var listBindings = []Binding{
	{"↑  k", "select previous"},
	{"↓  j", "select next"},
	{"pgup  b", "page up"},
	{"pgdn  f", "page down"},
	{"home  g", "jump to top"},
	{"end   G", "jump to bottom"},
	{"enter", "drill into selected"},
}

var groupBindings = []Binding{
	{"+  =", "widen price grouping (zoom out)"},
	{"-  _", "narrow price grouping (zoom in)"},
}

// depthTierBindings is the legacy single-venue ladder's `+/-`
// vocabulary, kept distinct from groupBindings so surfaces that
// declare DepthTier (instead of Group) get a help-overlay section
// labelled "Depth tier" and showing the right semantic. The keys
// are the same as Group's; the description differs.
var depthTierBindings = []Binding{
	{"+  =", "deeper stats tier (10 → 20 → 50 → 100)"},
	{"-  _", "shallower stats tier"},
}

var depthBindings = []Binding{
	{"d", "cycle stats depth (10 → 20 → 50 → 100)"},
}

var venueBindings = []Binding{
	{"v", "toggle venue visibility"},
}

var recenterBindings = []Binding{
	{"c", "recenter viewport on the spread"},
}

var chartTimeframeBindings = []Binding{
	{"t", "cycle chart timeframe (1m → 5m → 15m → 1h)"},
}

var searchBindings = []Binding{
	{"/", "filter table"},
	{"type", "update filter"},
	{"backspace", "delete filter character"},
	{"enter / esc", "close filter prompt"},
}

var ladderModeBindings = []Binding{
	{"m", "toggle aggregated ↔ split ladder"},
}

var multiPaneBindings = []Binding{
	{"tab", "focus next pane"},
	{"shift+tab", "focus previous pane"},
	{"1 / 2 / 3 / 4", "jump to pane 1 / 2 / 3 / 4"},
	{"enter", "expand / collapse focused pane"},
}

// SectionsFor returns the help-overlay sections relevant to a
// surface's capabilities. "Always" first; then per-capability
// groups, only when the capability is on.
func SectionsFor(c Capabilities) []Section {
	out := []Section{{Title: "Always", Bindings: commonBindings}}
	if c.ListNav {
		out = append(out, Section{Title: "Navigation", Bindings: listBindings})
	}
	if c.Group {
		out = append(out, Section{Title: "Price grouping", Bindings: groupBindings})
	} else if c.DepthTier {
		out = append(out, Section{Title: "Depth tier", Bindings: depthTierBindings})
	}
	if c.DepthCycle {
		out = append(out, Section{Title: "Stats depth", Bindings: depthBindings})
	}
	if c.Recenter {
		out = append(out, Section{Title: "Recenter", Bindings: recenterBindings})
	}
	if c.ChartTimeframe {
		out = append(out, Section{Title: "Chart", Bindings: chartTimeframeBindings})
	}
	if c.Search {
		out = append(out, Section{Title: "Search", Bindings: searchBindings})
	}
	if c.LadderMode {
		out = append(out, Section{Title: "Ladder mode", Bindings: ladderModeBindings})
	}
	if c.VenueToggle {
		out = append(out, Section{Title: "Venues", Bindings: venueBindings})
	}
	if c.MultiPane {
		out = append(out, Section{Title: "Multi-pane", Bindings: multiPaneBindings})
	}
	return out
}

// ─── help overlay renderer ─────────────────────────────────────────────────
//
// One canonical overlay renderer used by every TUI surface. Lives
// here (not in internal/output) because the section structure is
// owned by this package; the styling helpers (Bold/BrandGreen/etc.)
// are imported from the output package via the small interface
// HelpStyle. That dependency direction keeps the keymap package
// terminal-agnostic — a future text-only or HTML help renderer
// would just supply a different HelpStyle implementation.

// HelpStyle is the minimal styling surface RenderHelpOverlay needs
// to colour its output. Implementations live in internal/output
// (see output.HelpStyle()) so the keymap package doesn't import
// any rendering library directly.
type HelpStyle struct {
	Bold      string
	Green     string
	Grey      string
	LightGrey string
	Reset     string
}

// RenderHelpOverlay produces the canonical keybinding reference for
// a surface. Title is the human-readable view name shown in the
// banner ("rolling tape", "book scan", "book ladder", "dashboard");
// caps drives which sections appear; style is plain ANSI escapes
// (output.BrandGreen, output.BrandGreyMid, etc.) so we don't import
// the output package here.
//
// Width is reserved for future right-alignment of long descriptions;
// today it's unused and the layout flows naturally.
func RenderHelpOverlay(title string, caps Capabilities, style HelpStyle, width int) string {
	out := style.Bold + style.Green + "▲  laevitas — " + title + " keybindings" + style.Reset + "\n\n"

	for _, s := range SectionsFor(caps) {
		out += style.Bold + style.LightGrey + s.Title + style.Reset + "\n"
		for _, b := range s.Bindings {
			out += "  " + style.Green + padRight(b.Keys, 18) + style.Reset
			out += style.Grey + b.Desc + style.Reset + "\n"
		}
		out += "\n"
	}

	out += style.Grey + "Press ? or esc to return." + style.Reset + "\n"
	out += style.Grey + "Tip: hold Shift while dragging to copy text from this view (Alt in VS Code)." + style.Reset
	return out
}

// padRight pads s with ASCII spaces so its rune count reaches width.
// Naive — assumes no ANSI escapes inside the binding strings (which
// is true; bindings are plain ASCII).
func padRight(s string, width int) string {
	for len([]rune(s)) < width {
		s += " "
	}
	return s
}
