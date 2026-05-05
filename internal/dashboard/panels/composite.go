package panels

// Composite layout panels for the v0.10.0 flow dashboard.
//
// Both composites are deliberately dumb: they lay out their two
// children, forward Bubble Tea messages, union subscriptions and
// capabilities, and route keys to ONE designated active child
// chosen at construction time (no dynamic focus state inside the
// composite — that's a kernel concern). They exist only because the
// existing kernel layouts (Single / Split / Triad) don't fit the
// flow detail view's three-row arrangement (stats + chart-and-book
// row + tape-and-liquidations row), and we'd rather not extend the
// kernel for one consumer.
//
// Composite scope:
//   - Owns layout proportions only.
//   - Forwards Init / Update / View / Subscriptions / Capabilities.
//   - Returns "" for Title — composites have no chrome of their own.
//   - Sends a key (tea.KeyMsg) only to the active child; everything
//     else (size, feed ticks, selection changes, mouse) goes to both.
//
// Composite NON-scope:
//   - No internal focus state machine. activeChild is fixed at New.
//   - No help text aggregation. Capabilities are unioned for the
//     kernel's footer hints; rich help layering belongs in the
//     panels themselves.
//   - No nested kernel. If a child needs more children, build
//     another composite — composites compose.
//
// Capabilities ↔ key-routing contract:
//
//	Capabilities() unions all children's declared caps so the
//	kernel's footer can advertise every key any child responds to.
//	Update() routes tea.KeyMsg only to the active child (or
//	nowhere when activeChild is None). These two facts together
//	create a contract callers MUST respect:
//
//	  - A child whose capability is unioned through a composite
//	    with activeChildNone must NOT rely on that key reaching it.
//	    Either:
//	      (a) the child does not declare the capability, OR
//	      (b) the parent panel (e.g. FlowPanel) handles the key
//	          itself before constructing the composite.
//
//	Violation symptom: footer hints show a key (e.g. `d depth`),
//	but pressing it does nothing. Users would correctly call this
//	a UX bug.
//
//	For v0.10.0 the flow detail composite uses activeChildNone
//	(detail panes are passive) and global keys (q, esc) are
//	handled by the kernel; no detail child should declare
//	capabilities like DepthCycle through this composite.

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// activeChildNone is the activeChild sentinel meaning "neither child
// receives key messages." Used when both children are passive
// (e.g. the flow detail view, where keys are global only). Explicit
// constant rather than a magic -1 so call sites read clearly.
const activeChildNone = -1

// SplitPanel arranges two children left-to-right with a weighted
// width split. leftWeight is a percentage of the total width
// (0–100) given to the left child; the right child gets the
// remainder minus a one-cell separator gutter. Weights outside
// [10, 90] are clamped — extreme splits make one pane unusable
// and we prefer "ugly but legible" to "tiny pane the kernel can't
// render into."
type SplitPanel struct {
	left, right Panel

	// leftWeight is the percentage (0–100) of the available width
	// allocated to the left child. After accounting for a one-cell
	// gutter between panes, right gets the remainder.
	leftWeight int

	// activeChild is the index (0 = left, 1 = right) that receives
	// key messages. activeChildNone (-1) routes nothing — every key
	// flows back to the kernel. Fixed at construction; the
	// composite has no internal focus switching.
	activeChild int
}

// Panel is a thin alias for dashboard.Panel so this file stays
// readable without re-importing the dashboard package on every
// type signature. The composites implement dashboard.Panel; this
// alias is package-private convenience.
type Panel = dashboard.Panel

// NewSplitPanel constructs a horizontal-split composite. leftWeight
// is clamped to [10, 90]; activeChild may be 0 (left), 1 (right),
// or activeChildNone (-1) for "no key routing."
func NewSplitPanel(left, right Panel, leftWeight int, activeChild int) *SplitPanel {
	if leftWeight < 10 {
		leftWeight = 10
	}
	if leftWeight > 90 {
		leftWeight = 90
	}
	if activeChild != activeChildNone && activeChild != 0 && activeChild != 1 {
		// Caller passed a nonsense index; default to no routing
		// rather than panicking — composites are infrastructure,
		// they shouldn't blow up the dashboard for a programmer
		// error in the panel-tree builder.
		activeChild = activeChildNone
	}
	return &SplitPanel{
		left:        left,
		right:       right,
		leftWeight:  leftWeight,
		activeChild: activeChild,
	}
}

// Init returns the children's Init commands batched.
func (p *SplitPanel) Init() tea.Cmd {
	return tea.Batch(p.left.Init(), p.right.Init())
}

// Update forwards the message:
//
//   - tea.KeyMsg goes only to the active child (or nowhere if
//     activeChild is None — the kernel's global handler picks it up).
//   - everything else (size, custom messages, feed ticks, selection
//     changes) goes to both children.
//
// Children are mutated in place via type assertion back to *SplitPanel
// — but since we hold them as Panel-interface values, we install the
// returned Panel back into our field.
func (p *SplitPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if _, isKey := msg.(tea.KeyMsg); isKey {
		switch p.activeChild {
		case 0:
			updated, cmd := p.left.Update(msg)
			p.left = updated
			return p, cmd
		case 1:
			updated, cmd := p.right.Update(msg)
			p.right = updated
			return p, cmd
		default:
			// activeChildNone — drop the key. Kernel handles
			// global keys before reaching the panel tree, so this
			// branch is for keys that fell through global handling
			// AND have no active child. Nothing to do.
			return p, nil
		}
	}
	// Non-key: broadcast to both. Collect commands.
	leftUpdated, leftCmd := p.left.Update(msg)
	p.left = leftUpdated
	rightUpdated, rightCmd := p.right.Update(msg)
	p.right = rightUpdated
	return p, tea.Batch(leftCmd, rightCmd)
}

// View renders the two children side-by-side. The full width is
// split per leftWeight, minus a one-cell gutter between them.
func (p *SplitPanel) View(width, height int, ctx dashboard.PanelContext) string {
	if width < 4 {
		// Too narrow to split usefully. Just render the left child
		// at the available width; the right child is dropped. This
		// is graceful degradation for tiny terminals.
		return normalizePanelBlock(p.left.View(width, height, ctx), width, height)
	}
	leftW := (width - 1) * p.leftWeight / 100
	if leftW < 1 {
		leftW = 1
	}
	rightW := width - 1 - leftW
	if rightW < 1 {
		rightW = 1
		leftW = width - 1 - rightW
	}
	leftView := normalizePanelBlock(p.left.View(leftW, height, ctx), leftW, height)
	rightView := normalizePanelBlock(p.right.View(rightW, height, ctx), rightW, height)
	return normalizePanelBlock(lipgloss.JoinHorizontal(lipgloss.Top, leftView, " ", rightView), width, height)
}

// Subscriptions returns the union of both children's subscriptions,
// deduplicated. The kernel's FeedRouter also dedupes, but doing it
// here keeps the FeedSpec returned by Subscriptions cleanly minimal
// and avoids confusing diagnostics that show inflated subscription
// counts before the router collapses them.
func (p *SplitPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	return unionSubs(p.left.Subscriptions(sel), p.right.Subscriptions(sel))
}

// Title is empty — composites have no chrome of their own. The
// kernel's layout chrome sits at the dashboard root level.
func (p *SplitPanel) Title() string { return "" }

// Capabilities returns the union of both children's. Kernel ORs
// this with its own layout-derived flags (e.g. MultiPane) and
// passes it to keymap.FooterHints, so the hints reflect everything
// any panel in the composite can do.
func (p *SplitPanel) Capabilities() keymap.Capabilities {
	return p.left.Capabilities().Union(p.right.Capabilities())
}

// StackPanel arranges two children top-to-bottom. topRows is the
// fixed row count given to the top child; the bottom child gets
// (height - topRows - 1), with a one-row separator gutter between.
//
// Fixed rows on top (rather than weighted percentages) because the
// flow stats bar is naturally one row — if we made it weighted, a
// short terminal would shrink it to nothing. Use FlexStackPanel
// when you actually want weighted heights; that's a different
// constructor below.
type StackPanel struct {
	top, bottom Panel
	topRows     int
	gutter      bool // true → one-row separator between panes
	activeChild int
}

// NewStackPanel constructs a top/bottom composite where the top
// pane has a fixed row count. Use NewFlexStackPanel for weighted
// heights.
func NewStackPanel(top, bottom Panel, topRows int, activeChild int) *StackPanel {
	if topRows < 1 {
		topRows = 1
	}
	if activeChild != activeChildNone && activeChild != 0 && activeChild != 1 {
		activeChild = activeChildNone
	}
	return &StackPanel{
		top:         top,
		bottom:      bottom,
		topRows:     topRows,
		gutter:      false,
		activeChild: activeChild,
	}
}

// Init batches both children's Init commands.
func (p *StackPanel) Init() tea.Cmd {
	return tea.Batch(p.top.Init(), p.bottom.Init())
}

// Update mirrors SplitPanel: keys to active child only, everything
// else to both.
func (p *StackPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if _, isKey := msg.(tea.KeyMsg); isKey {
		switch p.activeChild {
		case 0:
			updated, cmd := p.top.Update(msg)
			p.top = updated
			return p, cmd
		case 1:
			updated, cmd := p.bottom.Update(msg)
			p.bottom = updated
			return p, cmd
		default:
			return p, nil
		}
	}
	topUpdated, topCmd := p.top.Update(msg)
	p.top = topUpdated
	bottomUpdated, bottomCmd := p.bottom.Update(msg)
	p.bottom = bottomUpdated
	return p, tea.Batch(topCmd, bottomCmd)
}

// View stacks the two children. Top child gets topRows; bottom
// child gets the rest minus the gutter (if enabled).
func (p *StackPanel) View(width, height int, ctx dashboard.PanelContext) string {
	if height < 3 {
		// Too short to stack. Render top only.
		return normalizePanelBlock(p.top.View(width, height, ctx), width, height)
	}
	gutterRows := 0
	if p.gutter {
		gutterRows = 1
	}
	topH := p.topRows
	if topH > height-gutterRows-1 {
		topH = height - gutterRows - 1
	}
	if topH < 1 {
		topH = 1
	}
	bottomH := height - topH - gutterRows
	if bottomH < 1 {
		bottomH = 1
	}
	topView := normalizePanelBlock(p.top.View(width, topH, ctx), width, topH)
	bottomView := normalizePanelBlock(p.bottom.View(width, bottomH, ctx), width, bottomH)
	if gutterRows > 0 {
		return normalizePanelBlock(lipgloss.JoinVertical(lipgloss.Left, topView, "", bottomView), width, height)
	}
	return normalizePanelBlock(lipgloss.JoinVertical(lipgloss.Left, topView, bottomView), width, height)
}

// Subscriptions, Title, Capabilities — same shape as SplitPanel.
func (p *StackPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	return unionSubs(p.top.Subscriptions(sel), p.bottom.Subscriptions(sel))
}
func (p *StackPanel) Title() string { return "" }
func (p *StackPanel) Capabilities() keymap.Capabilities {
	return p.top.Capabilities().Union(p.bottom.Capabilities())
}

// FlexStackPanel arranges two children top-to-bottom with weighted
// heights. topWeight is a percentage (10–90) of the total height
// allocated to the top child after the gutter. Use this for
// equal-or-flexible splits (e.g. chart 50% / book ladder 50%);
// use StackPanel for fixed-row arrangements (e.g. one-row stats
// bar on top of everything else).
type FlexStackPanel struct {
	top, bottom Panel
	topWeight   int
	activeChild int
}

// NewFlexStackPanel — weighted heights variant.
func NewFlexStackPanel(top, bottom Panel, topWeight int, activeChild int) *FlexStackPanel {
	if topWeight < 10 {
		topWeight = 10
	}
	if topWeight > 90 {
		topWeight = 90
	}
	if activeChild != activeChildNone && activeChild != 0 && activeChild != 1 {
		activeChild = activeChildNone
	}
	return &FlexStackPanel{
		top:         top,
		bottom:      bottom,
		topWeight:   topWeight,
		activeChild: activeChild,
	}
}

func (p *FlexStackPanel) Init() tea.Cmd {
	return tea.Batch(p.top.Init(), p.bottom.Init())
}

func (p *FlexStackPanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if _, isKey := msg.(tea.KeyMsg); isKey {
		switch p.activeChild {
		case 0:
			updated, cmd := p.top.Update(msg)
			p.top = updated
			return p, cmd
		case 1:
			updated, cmd := p.bottom.Update(msg)
			p.bottom = updated
			return p, cmd
		default:
			return p, nil
		}
	}
	topUpdated, topCmd := p.top.Update(msg)
	p.top = topUpdated
	bottomUpdated, bottomCmd := p.bottom.Update(msg)
	p.bottom = bottomUpdated
	return p, tea.Batch(topCmd, bottomCmd)
}

func (p *FlexStackPanel) View(width, height int, ctx dashboard.PanelContext) string {
	if height < 4 {
		return normalizePanelBlock(p.top.View(width, height, ctx), width, height)
	}
	topH := height * p.topWeight / 100
	if topH < 1 {
		topH = 1
	}
	bottomH := height - topH
	if bottomH < 1 {
		bottomH = 1
		topH = height - bottomH
	}
	topView := normalizePanelBlock(p.top.View(width, topH, ctx), width, topH)
	bottomView := normalizePanelBlock(p.bottom.View(width, bottomH, ctx), width, bottomH)
	return normalizePanelBlock(lipgloss.JoinVertical(lipgloss.Left, topView, bottomView), width, height)
}

func (p *FlexStackPanel) Subscriptions(sel dashboard.Selection) dashboard.FeedSpec {
	return unionSubs(p.top.Subscriptions(sel), p.bottom.Subscriptions(sel))
}
func (p *FlexStackPanel) Title() string { return "" }
func (p *FlexStackPanel) Capabilities() keymap.Capabilities {
	return p.top.Capabilities().Union(p.bottom.Capabilities())
}

// unionSubs deduplicates two FeedSpec channel lists. Both kernel-
// level dedupe (FeedRouter computes the desired set as a map) and
// composite-level dedupe matter: clean diagnostics prefer minimal
// FeedSpec returns, and unioning at the composite means subscribe
// counters in the perf overlay reflect the actual desired set
// rather than inflated child counts.
func unionSubs(a, b dashboard.FeedSpec) dashboard.FeedSpec {
	// Single dedupe pass over both inputs — no empty-side fast path.
	// An earlier "if len(x) == 0 return copy of other" shortcut left
	// duplicates inside `other` untouched, breaking the helper's
	// minimal-FeedSpec contract for inputs like ["a","a"] + []. The
	// extra map allocation in the empty case is negligible against
	// the cost of inflated subscription counts in diagnostics.
	seen := make(map[string]struct{}, len(a.Channels)+len(b.Channels))
	out := make([]string, 0, len(a.Channels)+len(b.Channels))
	for _, ch := range a.Channels {
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		out = append(out, ch)
	}
	for _, ch := range b.Channels {
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		out = append(out, ch)
	}
	return dashboard.FeedSpec{Channels: out}
}

// normalizePanelBlock enforces the rectangle contract composites
// depend on: exactly height rows, each exactly width visible cells.
// Children should normally render that shape themselves, but the
// composite is the last line of defence before lipgloss joins the
// panes. Without this, a short or over-wide line changes where the
// neighbouring pane starts on that row.
func normalizePanelBlock(s string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = output.PadRightAnsi(line, width)
	}
	return strings.Join(lines, "\n")
}

// Compile-time interface satisfaction — catches drift if the Panel
// interface changes underneath us.
var (
	_ Panel = (*SplitPanel)(nil)
	_ Panel = (*StackPanel)(nil)
	_ Panel = (*FlexStackPanel)(nil)
)
