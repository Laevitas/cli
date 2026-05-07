// Package wsrender — local shims to the shared keymap module.
//
// Until v0.8.3 this file held the canonical TUI keymap. As the
// dashboard kernel needed the same vocabulary, the truth moved to
// internal/keymap so every surface routes through one source. The
// shims below preserve the old per-surface API (footerHints("tape"))
// so existing wsrender call sites don't all need editing in the
// same commit; new code (dashboard panels, future renderers) calls
// keymap.* directly with explicit Capabilities.
//
// Each shim translates a surface name into the Capabilities value
// the underlying surface really supports, then delegates to the
// shared module. Adding a new surface here = one switch case;
// adding a new key globally = one edit in internal/keymap.
package wsrender

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/keymap"
)

// keyAction is the local alias for the shared Action enum, kept so
// older switch statements in book.go / wsrender.go still compile
// without touching every case label. The underlying type is the
// same — alias, not a distinct type — so values pass through.
type keyAction = keymap.Action

const (
	actNone     = keymap.ActNone
	actQuit     = keymap.ActQuit
	actPause    = keymap.ActPause
	actHelp     = keymap.ActHelp
	actEsc      = keymap.ActEsc
	actUp       = keymap.ActUp
	actDown     = keymap.ActDown
	actPageUp   = keymap.ActPageUp
	actPageDown = keymap.ActPageDown
	actTop      = keymap.ActTop
	actBottom   = keymap.ActBottom
	actEnter    = keymap.ActEnter
	// actDepthUp / actDepthDown are kept as aliases to ActGroupUp /
	// ActGroupDown so the existing rolling-tape book ladder keeps
	// its current `+/-` behaviour without renaming every call site.
	// In the new vocabulary `+/-` widens/narrows price grouping —
	// the legacy ladder treated it as "depth tier," but the keys
	// dispatch identically and existing user muscle memory carries
	// over.
	actDepthUp      = keymap.ActGroupUp
	actDepthDown    = keymap.ActGroupDown
	actWheelUp      = keymap.ActWheelUp
	actWheelDown    = keymap.ActWheelDown
	actTapeFilter   = keymap.ActTapeFilter
	actCycleFocus   = keymap.ActCycleFocus
	actReverseFocus = keymap.ActReverseFocus
	actJumpPane1    = keymap.ActJumpPane1
	actJumpPane2    = keymap.ActJumpPane2
	actJumpPane3    = keymap.ActJumpPane3
)

func classifyKey(s string) keyAction            { return keymap.ClassifyKey(s) }
func classifyMouse(b tea.MouseButton) keyAction { return keymap.ClassifyMouse(b) }

// surfaceCapabilities maps the legacy surface-name strings used by
// wsrender ("tape", "scan", "ladder", "ladder-back") to the
// Capabilities the shared module expects. Keeps this file the
// single place that knows about the legacy names.
func surfaceCapabilities(surface string) keymap.Capabilities {
	switch surface {
	case "scan":
		return keymap.Capabilities{
			ListNav: true, Drill: true,
			Pause: true, Help: true,
		}
	case "ladder":
		// Single-venue ladder uses the unified vocabulary in v0.8.3:
		// `+/-` is price grouping (Group), `d` cycles depth tier
		// (DepthCycle), `c` recentres on spread (Recenter), and
		// j/k/PgUp/PgDn/g/G drive the viewport (ListNav). Same
		// Capabilities the dashboard book panel declares — both
		// surfaces share the helpers in internal/ladder, so they
		// must share the footer/help vocabulary too.
		return keymap.Capabilities{
			Group: true, DepthCycle: true, Recenter: true,
			ListNav: true, Pause: true, Help: true,
		}
	case "ladder-back":
		return keymap.Capabilities{
			Group: true, DepthCycle: true, Recenter: true,
			ListNav: true, Back: true, Pause: true, Help: true,
		}
	case "tape":
		return keymap.Capabilities{
			TapeFilter: true, Pause: true, Help: true,
		}
	case "stream":
		return keymap.Capabilities{
			Pause: true, Help: true,
		}
	}
	return keymap.Capabilities{Help: true}
}

func footerHints(surface string) string {
	return keymap.FooterHints(surfaceCapabilities(surface))
}
