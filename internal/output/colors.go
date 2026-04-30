package output

import (
	"os"

	"golang.org/x/term"
)

// Generic ANSI helpers (8-color terminal palette).
const (
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Red    = "\033[31m"
	Yellow = "\033[33m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Reset  = "\033[0m"
)

// Laevitas brand palette, 24-bit truecolor. From Branding Guidelines (Apr 2021):
//
//	Primary green   #46be52   "Laevitas green" — accents, positive deltas, brand glyph
//	Dark navy       #1a2127   header backgrounds, deep contrast
//	Mid grey        #475057   secondary text, dim labels, metadata
//	Light grey      #ececec   separators, faint borders
//
// Modern terminals (iTerm2, Windows Terminal, VS Code, Alacritty, Kitty) honour
// these directly. Older terminals fall back to the nearest 256-colour entry —
// still readable, just less brand-faithful.
const (
	BrandGreen     = "\033[38;2;70;190;82m"     // #46be52
	BrandNavy      = "\033[38;2;26;33;39m"      // #1a2127
	BrandGreyMid   = "\033[38;2;71;80;87m"      // #475057
	BrandGreyLight = "\033[38;2;236;236;236m"   // #ececec

	BrandGreenBg = "\033[48;2;70;190;82m"  // green background (rare; for badges)
	BrandNavyBg  = "\033[48;2;26;33;39m"   // dark navy background (header rows)
)

// Semantic aliases — prefer these in new code so the palette can swap centrally.
const (
	ColorSuccess  = BrandGreen
	ColorError    = Red          // No brand red exists; using terminal red.
	ColorWarn     = Yellow
	ColorAccent   = BrandGreen   // Brand-coloured accents replace generic cyan.
	ColorMuted    = BrandGreyMid // Dim text uses brand mid-grey, not generic dim.
	ColorHeaderBg = BrandNavyBg
)

// Colorize wraps text in ANSI color codes. Returns plain text if noColor is true.
func Colorize(text, color string, noColor bool) string {
	if noColor {
		return text
	}
	return color + text + Reset
}

// HelpStyleStrings returns the ANSI escape codes the keymap package
// needs to render its help overlay. Keeps the keymap package free
// of any output / lipgloss imports — the dependency points one
// direction (consumers depend on output, not the other way around).
//
// Returned as a tuple of named strings rather than a struct so the
// keymap package can declare its own struct shape without importing
// our types. Order matches keymap.HelpStyle field order.
func HelpStyleStrings() (bold, green, grey, lightGrey, reset string) {
	return Bold, BrandGreen, BrandGreyMid, BrandGreyLight, Reset
}

// IsTTY returns true if stdout is an interactive terminal.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
