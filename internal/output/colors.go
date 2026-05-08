package output

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// Generic ANSI helpers. The visible colours are selected at startup:
// truecolor when the terminal advertises it, otherwise bright 16-colour
// SGRs that older clients such as Termius handle reliably.
const (
	Cyan  = "\033[36m"
	Bold  = "\033[1m"
	Dim   = "\033[2m"
	Reset = "\033[0m"
)

var activePalette = detectColorPalette()

var (
	Green  = activePalette.Green
	Red    = activePalette.Red
	Yellow = activePalette.Yellow
)

// Laevitas brand palette. Truecolor-capable terminals get the exact
// branding colours; conservative terminals get 16-colour fallbacks that
// preserve readability even when 24-bit SGR is unsupported or misparsed.
//
// From Branding Guidelines (Apr 2021):
//
//	Primary green   #46be52   "Laevitas green" — accents, positive deltas, brand glyph
//	Dark navy       #1a2127   header backgrounds, deep contrast
//	Mid grey        #475057   secondary text, dim labels, metadata
//	Light grey      #ececec   separators, faint borders
var (
	BrandGreen     = activePalette.BrandGreen
	BrandNavy      = activePalette.BrandNavy
	BrandGreyMid   = activePalette.BrandGreyMid
	BrandGreyLight = activePalette.BrandGreyLight

	BrandGreenBg = activePalette.BrandGreenBg
	BrandNavyBg  = activePalette.BrandNavyBg
)

// Semantic aliases — prefer these in new code so the palette can swap centrally.
var (
	ColorSuccess  = BrandGreen
	ColorError    = Red // No brand red exists; using terminal red.
	ColorWarn     = Yellow
	ColorAccent   = BrandGreen   // Brand-coloured accents replace generic cyan.
	ColorMuted    = BrandGreyMid // Dim text uses brand mid-grey, not generic dim.
	ColorHeaderBg = BrandNavyBg
)

type colorPalette struct {
	Green          string
	Red            string
	Yellow         string
	BrandGreen     string
	BrandNavy      string
	BrandGreyMid   string
	BrandGreyLight string
	BrandGreenBg   string
	BrandNavyBg    string
}

func detectColorPalette() colorPalette {
	return detectColorPaletteWithEnv(os.Getenv)
}

func detectColorPaletteWithEnv(getenv func(string) string) colorPalette {
	if supportsTrueColorEnv(getenv) {
		return colorPalette{
			Green:          "\033[38;2;70;190;82m",
			Red:            "\033[91m",
			Yellow:         "\033[33m",
			BrandGreen:     "\033[38;2;70;190;82m",   // #46be52
			BrandNavy:      "\033[38;2;26;33;39m",    // #1a2127
			BrandGreyMid:   "\033[38;2;71;80;87m",    // #475057
			BrandGreyLight: "\033[38;2;236;236;236m", // #ececec
			BrandGreenBg:   "\033[48;2;70;190;82m",
			BrandNavyBg:    "\033[48;2;26;33;39m",
		}
	}
	return colorPalette{
		Green:          "\033[92m",
		Red:            "\033[91m",
		Yellow:         "\033[93m",
		BrandGreen:     "\033[92m",
		BrandNavy:      "\033[34m",
		BrandGreyMid:   "\033[90m",
		BrandGreyLight: "\033[97m",
		BrandGreenBg:   "\033[42m",
		BrandNavyBg:    "\033[44m",
	}
}

func supportsTrueColorEnv(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("LAEVITAS_COLOR"))) {
	case "truecolor", "24bit", "24-bit":
		return true
	case "ansi", "16", "16color", "16-color", "never":
		return false
	}

	colorterm := strings.ToLower(getenv("COLORTERM"))
	if strings.Contains(colorterm, "truecolor") || strings.Contains(colorterm, "24bit") {
		return true
	}
	term := strings.ToLower(getenv("TERM"))
	if strings.Contains(term, "truecolor") || strings.Contains(term, "24bit") {
		return true
	}
	switch strings.ToLower(getenv("TERM_PROGRAM")) {
	case "iterm.app", "vscode", "wezterm", "kitty":
		return true
	}
	return getenv("WT_SESSION") != ""
}

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
