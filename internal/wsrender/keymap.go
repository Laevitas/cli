// Package wsrender — shared keybinding vocabulary for every TUI surface.
//
// Every interactive view (rolling tape, book scan, book ladder, anything
// added later) routes through this single source of truth so users and
// agents only have to learn one keymap. Adding a new binding means
// editing this file and nothing else; the help overlay, footer hints,
// and per-model dispatch all read from these constants.
//
// Design intent: match k9s + less + vim conventions where they overlap
// (j/k/g/G + arrows/Home/End + PgUp/PgDn + Enter/Esc + q + ?). The set
// is deliberately small — every key earns its place by being instantly
// recognisable to anyone who has used a Unix-style TUI before.
package wsrender

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// keyAction is a logical action — what the user is trying to do — keyed
// off whatever key string they pressed. The dispatcher returns one of
// these so each model can handle the action without re-implementing the
// "is q a quit key?" logic.
type keyAction int

const (
	actNone keyAction = iota
	actQuit
	actPause
	actHelp
	actEsc
	actUp
	actDown
	actPageUp
	actPageDown
	actTop
	actBottom
	actEnter
	actDepthUp
	actDepthDown
	actWheelUp
	actWheelDown
)

// classifyKey maps a Bubble Tea key string to a logical action. Returns
// actNone when the key isn't part of our vocabulary (callers should
// ignore it). The same string-to-action table is used by every TUI
// surface — adding a new binding here propagates everywhere.
func classifyKey(key string) keyAction {
	switch key {
	case "q", "Q", "ctrl+c":
		return actQuit
	case "p", "P":
		return actPause
	case "?", "h", "H":
		return actHelp
	case "esc":
		return actEsc
	case "up", "k":
		return actUp
	case "down", "j":
		return actDown
	case "pgup", "b":
		return actPageUp
	case "pgdown", "f":
		return actPageDown
	case "home", "g":
		return actTop
	case "end", "G":
		return actBottom
	case "enter":
		return actEnter
	case "+", "=":
		return actDepthUp
	case "-", "_":
		return actDepthDown
	}
	return actNone
}

// classifyMouse maps a Bubble Tea mouse button to a logical wheel
// action, or actNone for click events we deliberately don't capture
// (preserves the terminal's native click-drag-to-select for copy-paste).
func classifyMouse(btn tea.MouseButton) keyAction {
	switch btn {
	case tea.MouseButtonWheelUp:
		return actWheelUp
	case tea.MouseButtonWheelDown:
		return actWheelDown
	}
	return actNone
}

// ─── footer hint generation ────────────────────────────────────────────────

// footerHints returns the brand-grey one-line hint shown at the bottom of
// each surface. The set varies by surface but the wording and key order
// are derived from the same vocabulary so a hint never disagrees with
// the help overlay.
//
// surface is one of: "tape", "scan", "ladder", "ladder-back". The
// "ladder-back" variant adds `esc back` for ladder views entered via
// drill-down (so the user knows how to return to scan).
func footerHints(surface string) string {
	switch surface {
	case "scan":
		return "↑↓/jk select   pgup/pgdn page   g/G top/end   enter ladder   p pause   ? help   q quit"
	case "ladder":
		return "+/- depth   p pause   ? help   q quit"
	case "ladder-back":
		return "+/- depth   p pause   esc back   ? help   q quit"
	case "tape":
		return "p pause   ? help   q quit"
	}
	return "? help   q quit"
}

// ─── help overlay binding tables ───────────────────────────────────────────

// keyBinding pairs a display string for the keys with a short
// description for the help overlay.
type keyBinding struct {
	keys, desc string
}

// commonBindings lists the keys that work on every TUI surface.
// renderHelpOverlay always shows these first.
var commonBindings = []keyBinding{
	{"q  Q  ctrl+c", "quit"},
	{"p  P", "pause / resume"},
	{"?  h  H", "toggle this help"},
	{"esc", "close help / back out"},
	{"wheel ↑ / ↓", "scroll (lists) or pause (tape)"},
}

// listBindings adds list-navigation keys, used in scan views.
var listBindings = []keyBinding{
	{"↑  k", "select previous"},
	{"↓  j", "select next"},
	{"pgup  b", "page up"},
	{"pgdn  f", "page down"},
	{"home  g", "jump to top"},
	{"end   G", "jump to bottom"},
	{"enter", "drill into selected"},
}

// ladderBindings adds depth-tier keys, used in book ladder view.
var ladderBindings = []keyBinding{
	{"+  =", "deeper tier (10 → 20 → 50)"},
	{"-  _", "shallower tier"},
}

// bindingsForSurface returns the help-overlay sections relevant to a
// given surface, in display order.
func bindingsForSurface(surface string) []struct {
	title    string
	bindings []keyBinding
} {
	type section = struct {
		title    string
		bindings []keyBinding
	}
	out := []section{{title: "Always", bindings: commonBindings}}
	switch {
	case strings.Contains(surface, "scan"):
		out = append(out, section{title: "Navigation", bindings: listBindings})
	case strings.Contains(surface, "ladder"):
		out = append(out, section{title: "Ladder", bindings: ladderBindings})
	}
	return out
}
