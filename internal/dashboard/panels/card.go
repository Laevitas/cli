package panels

// CardPanel — a transparent decorator that draws a titled, bordered
// box around any wrapped Panel.
//
// Why a decorator and not chrome inside each panel:
//   - Each detail panel's View renders its data into width × height.
//     If the panel painted its own border, the panel's data renderer
//     would have to subtract 2 from width and height in every code
//     path, every time, AND emit the border ANSI in matching colours,
//     AND the visual style would drift between panels. Worst class
//     of duplication.
//   - The composite layer already knows the panel's region. Wrapping
//     a panel here means the wrapped panel's View receives the
//     interior dimensions (width-2, height-2) and the card's draw
//     code paints the frame around the result.
//   - Card titles are a per-instance label, not a per-panel-type
//     constant. The same FlowBookPanel might be wrapped as
//     "BOOK · binance" in one composite and "ASK SIDE" in another.
//     Decoration keeps the label out of the panel itself.
//
// Forwarding contract:
//   - Init / Update / Subscriptions / Capabilities / Title pass
//     through to the wrapped panel unchanged. The card's own Title()
//     returns "" because the kernel header is for the dashboard, not
//     for individual cards — the title we draw is INTERNAL chrome
//     painted by View().
//   - The card never consumes keys or mutates state. It is a pure
//     render decorator.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// cardMinWidth / cardMinHeight: anything smaller and the border eats
// every visible cell. Below these we render the wrapped panel's
// View at full size with no chrome — graceful degradation rather
// than a panel that renders only a frame.
const (
	cardMinWidth  = 10
	cardMinHeight = 4
)

// CardPanel wraps a panel with a titled border.
type CardPanel struct {
	inner Panel
	title string
}

// CardSubtitler is an optional interface implemented by panels
// that want their CardPanel wrapper to render a dynamic subtitle
// next to the static title — typically venue/instrument identity
// derived from the panel's selection.
//
// The subtitle changes per-render (selection drift, drill events)
// so it can't be baked into NewCardPanel's title arg. The
// CardPanel checks for this interface at render time and splices
// the result into the top border, after the static title.
//
// Returning "" suppresses the subtitle for that frame —
// equivalent to "no instrument selected." Same for nil receiver.
//
// Why an optional interface rather than a Panel-method addition:
// every existing panel would have to implement it. The flow-detail
// panes are the only callers today; future panels opt in by
// implementing the method.
type CardSubtitler interface {
	CardSubtitle() string
}

// NewCardPanel wraps inner with a titled bordered card. The title
// is rendered in the top border line; pass "" to draw an unlabeled
// border (rare — usually you want a label).
//
// If `inner` implements CardSubtitler, the card renders
// `<title> · <subtitle>` in the border, with subtitle pulled per
// frame so it tracks the inner panel's current selection.
func NewCardPanel(inner Panel, title string) *CardPanel {
	return &CardPanel{inner: inner, title: title}
}

func (c *CardPanel) Init() tea.Cmd { return c.inner.Init() }

// Update forwards every message; CardPanel has no state of its own.
// The wrapped panel's returned Panel is reinstalled so any state
// mutation in the inner panel persists.
func (c *CardPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	updated, cmd := c.inner.Update(msg)
	c.inner = updated
	return c, cmd
}

// View draws the titled border and the inner panel's content
// inside it. The inner panel receives (width-2, height-2) so its
// own renderer doesn't need to know about the chrome.
//
// Border style: brand-grey single-line frame with the title
// embedded in the top edge. Lipgloss handles the corner glyphs and
// horizontal-fill so we don't repeat box-drawing logic per pane.
func (c *CardPanel) View(width, height int, ctx dashboard.PanelContext) string {
	if width < cardMinWidth || height < cardMinHeight {
		// Too small for chrome — render the inner panel raw. Better
		// to show one cramped pane than a frame with no content.
		return normalizePanelBlock(c.inner.View(width, height, ctx), width, height)
	}

	innerW := width - 2
	innerH := height - 2
	body := normalizePanelBlock(c.inner.View(innerW, innerH, ctx), innerW, innerH)

	// Brand-grey border so cards visually separate without
	// competing with the live data inside (BUY/SELL greens, OI
	// numbers, etc.). Lipgloss takes a hex; the rest of the
	// codebase keeps brand colours as ANSI escapes for printf
	// paths, so we re-derive the hex here. Same value the kernel
	// uses for its spinner.
	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(brandGreyHex)).
		Border(lipgloss.NormalBorder()).
		Width(innerW).
		Height(innerH)

	framed := borderStyle.Render(body)

	// Resolve the embedded title. Static title from construction
	// + optional dynamic subtitle from the inner panel via the
	// CardSubtitler interface. The two are joined with " · " in
	// the top border so identity is always one glance away
	// (e.g. "BOOK · binance:BTCUSDT" instead of just "BOOK").
	label := c.title
	if sub, ok := c.inner.(CardSubtitler); ok {
		if extra := sub.CardSubtitle(); extra != "" {
			if label == "" {
				label = extra
			} else {
				label = label + " · " + extra
			}
		}
	}

	// Embed the label in the top border line. Lipgloss doesn't
	// support border-with-title natively, so we overlay manually:
	// find the first line of the framed output (the top border),
	// splice " label " into it after the corner glyph.
	if label != "" {
		framed = embedTitle(framed, label, width)
	}
	return normalizePanelBlock(framed, width, height)
}

// embedTitle replaces the second through (2 + visible-width-of-
// title)-th cells of the first line with " title " in brand-green.
// The corner glyph stays at column 0; the title sits one cell in.
//
// We work on the FIRST LINE only — lipgloss border puts the top
// edge there. If the line is shorter than the embed point (which
// can happen on tiny widths after our cardMinWidth gate, e.g.
// width=10 leaves only 8 cells of border) we just return framed
// unchanged.
func embedTitle(framed, title string, width int) string {
	lines := strings.SplitN(framed, "\n", 2)
	if len(lines) < 2 {
		return framed
	}
	top := lines[0]
	rest := lines[1]

	// Render the title in brand-green so it pops against the grey
	// border without being shouty. Surround with a single space on
	// each side so the border glyphs visually bracket the label
	// rather than touching it.
	label := " " + title + " "
	if output.VisibleWidth(label) >= width-2 {
		// Title would consume the whole top edge; truncate so at
		// least the right corner survives.
		label = output.TruncateAnsi(label, width-2)
	}
	styledLabel := output.BrandGreen + label + output.Reset

	// Splice: keep the leading corner glyph (1 cell), insert the
	// label, then resume with the border glyph stream after
	// (1 + visible width of label) cells. This works because
	// lipgloss's NormalBorder uses single-cell box-drawing chars
	// that don't carry their own ANSI state.
	labelVisible := output.VisibleWidth(label)

	// Walk top one rune at a time, skipping ANSI SGR sequences.
	// Emit: rune 0 (corner), styledLabel, then runes from
	// 1+labelVisible onward.
	var b strings.Builder
	visible := 0
	i := 0
	for i < len(top) {
		// Pass-through ANSI SGR.
		if top[i] == 0x1b && i+1 < len(top) && top[i+1] == '[' {
			j := i + 2
			for j < len(top) && top[j] != 'm' {
				j++
			}
			if j < len(top) {
				b.WriteString(top[i : j+1])
				i = j + 1
				continue
			}
		}
		// Capture rune width.
		r, size := decodeRune(top[i:])
		if visible == 0 {
			// Emit corner.
			b.WriteRune(r)
			i += size
			visible++
			// Then emit the styled label.
			b.WriteString(styledLabel)
			visible += labelVisible
			continue
		}
		if visible >= 1+labelVisible {
			// Past the label region — emit the rest verbatim.
			b.WriteString(top[i:])
			break
		}
		// Inside the label region: skip the underlying border glyphs.
		i += size
		visible++
	}
	return b.String() + "\n" + rest
}

// decodeRune is a tiny UTF-8 decoder so we don't pull in
// unicode/utf8 just for the title splice. Returns (rune, byteSize).
// Callers guarantee the input has at least one byte.
func decodeRune(s string) (rune, int) {
	if s[0] < 0x80 {
		return rune(s[0]), 1
	}
	// Multi-byte UTF-8. Use range over a single-byte cut.
	for _, r := range s {
		// Range yields the first decoded rune.
		// Compute its UTF-8 size.
		switch {
		case r < 0x80:
			return r, 1
		case r < 0x800:
			return r, 2
		case r < 0x10000:
			return r, 3
		default:
			return r, 4
		}
	}
	return 0, 1
}

// brandGreyHex matches the ANSI BrandGreyMid used elsewhere. Kept
// here rather than imported from output because output exposes the
// ANSI escape, not the hex — and lipgloss styles take hex.
const brandGreyHex = "#475057"

// Subscriptions, Title, Capabilities — pure pass-through.
func (c *CardPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	return c.inner.Subscriptions(sel)
}

// Title returns "" — cards don't use the kernel's header chrome.
// The internal title is painted by View().
func (c *CardPanel) Title() string { return "" }

func (c *CardPanel) Capabilities() keymap.Capabilities {
	return c.inner.Capabilities()
}

// Compile-time interface satisfaction.
var _ Panel = (*CardPanel)(nil)
