package output

import (
	"os"

	"golang.org/x/term"
)

// Brand colors for TTY output.
const (
	Cyan   = "\033[36m"
	Green  = "\033[32m"
	Red    = "\033[31m"
	Yellow = "\033[33m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Reset  = "\033[0m"
)

// Semantic aliases.
const (
	ColorSuccess = Green
	ColorError   = Red
	ColorWarn    = Yellow
	ColorAccent  = Cyan
	ColorMuted   = Dim
)

// Colorize wraps text in ANSI color codes. Returns plain text if noColor is true.
func Colorize(text, color string, noColor bool) string {
	if noColor {
		return text
	}
	return color + text + Reset
}

// IsTTY returns true if stdout is an interactive terminal.
func IsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
