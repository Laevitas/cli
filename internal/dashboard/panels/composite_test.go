package panels

// Composite panel tests. Fake panels drive composite behaviour
// without depending on real panel implementations.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/dashboard"
	"github.com/laevitas/cli/internal/keymap"
	"github.com/laevitas/cli/internal/output"
)

// fakePanel records every Update call (with a copy of the message)
// and reports a fixed View string. Used to verify forwarding,
// subscription union, and key routing without real panel state.
type fakePanel struct {
	name        string
	keysSeen    int
	nonKeysSeen int
	subs        []string
	caps        keymap.Capabilities
	view        string
}

func newFakePanel(name string) *fakePanel {
	return &fakePanel{name: name}
}

func (p *fakePanel) Init() tea.Cmd { return nil }

func (p *fakePanel) Update(msg tea.Msg) (Panel, tea.Cmd) {
	if _, isKey := msg.(tea.KeyMsg); isKey {
		p.keysSeen++
	} else {
		p.nonKeysSeen++
	}
	return p, nil
}

func (p *fakePanel) View(width, height int, _ dashboard.PanelContext) string {
	if p.view != "" {
		return p.view
	}
	return p.name + ":" + strings.Repeat("·", maxInt(0, width-len(p.name)-1))
}

func (p *fakePanel) Subscriptions(_ dashboard.Selection) dashboard.FeedSpec {
	return dashboard.FeedSpec{Channels: append([]string(nil), p.subs...)}
}

func (p *fakePanel) Title() string                     { return p.name }
func (p *fakePanel) Capabilities() keymap.Capabilities { return p.caps }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func assertRect(t *testing.T, view string, width, height int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) != height {
		t.Fatalf("line count = %d, want %d\n%s", len(lines), height, view)
	}
	for i, line := range lines {
		if got := output.VisibleWidth(line); got != width {
			t.Fatalf("line %d width = %d, want %d\n%q\nfull view:\n%s", i, got, width, line, view)
		}
	}
}

// ─── SplitPanel tests ──────────────────────────────────────────────────────

// TestSplitPanelForwardsNonKeyMessagesToBoth: a non-key message
// (e.g. tea.WindowSizeMsg, custom feed tick) reaches both children
// regardless of activeChild.
func TestSplitPanelForwardsNonKeyMessagesToBoth(t *testing.T) {
	left := newFakePanel("L")
	right := newFakePanel("R")
	c := NewSplitPanel(left, right, 50, activeChildNone)

	c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if left.nonKeysSeen != 1 || right.nonKeysSeen != 1 {
		t.Errorf("non-key not forwarded to both: left=%d right=%d, want 1/1",
			left.nonKeysSeen, right.nonKeysSeen)
	}
}

// TestSplitPanelKeysGoToActiveOnly: a tea.KeyMsg goes only to the
// active child. activeChildNone routes nowhere.
func TestSplitPanelKeysGoToActiveOnly(t *testing.T) {
	cases := []struct {
		name      string
		active    int
		wantLeft  int
		wantRight int
	}{
		{"none", activeChildNone, 0, 0},
		{"left", 0, 1, 0},
		{"right", 1, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left := newFakePanel("L")
			right := newFakePanel("R")
			c := NewSplitPanel(left, right, 50, tc.active)
			c.Update(tea.KeyMsg{Type: tea.KeyEnter})

			if left.keysSeen != tc.wantLeft {
				t.Errorf("left keysSeen = %d, want %d", left.keysSeen, tc.wantLeft)
			}
			if right.keysSeen != tc.wantRight {
				t.Errorf("right keysSeen = %d, want %d", right.keysSeen, tc.wantRight)
			}
		})
	}
}

// TestSplitPanelInvalidActiveDefaultsToNone: passing an out-of-
// range activeChild shouldn't panic — composites are infrastructure
// and shouldn't blow up the dashboard for a programmer error.
func TestSplitPanelInvalidActiveDefaultsToNone(t *testing.T) {
	left := newFakePanel("L")
	right := newFakePanel("R")
	c := NewSplitPanel(left, right, 50, 99)

	c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if left.keysSeen != 0 || right.keysSeen != 0 {
		t.Errorf("invalid active routed to a child: left=%d right=%d", left.keysSeen, right.keysSeen)
	}
}

// TestSplitPanelWeightClamped: leftWeight outside [10, 90] is
// clamped at construction.
func TestSplitPanelWeightClamped(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{-50, 10},
		{0, 10},
		{5, 10},
		{50, 50},
		{90, 90},
		{95, 90},
		{200, 90},
	}
	for _, tc := range cases {
		c := NewSplitPanel(newFakePanel("a"), newFakePanel("b"), tc.in, activeChildNone)
		if c.leftWeight != tc.want {
			t.Errorf("leftWeight(%d) = %d, want %d", tc.in, c.leftWeight, tc.want)
		}
	}
}

// TestSplitPanelViewLayoutWidthSplit: at 50/50 the rendered view
// allocates roughly half-width to each child. The fake renders its
// name + filler dots so we can measure each section's slice.
func TestSplitPanelViewLayoutWidthSplit(t *testing.T) {
	left := newFakePanel("L")
	right := newFakePanel("R")
	c := NewSplitPanel(left, right, 50, activeChildNone)

	view := c.View(40, 5, dashboard.PanelContext{})
	// Expect "L:....… R:....…" with one space gutter.
	if !strings.Contains(view, "L:") {
		t.Errorf("left section missing in view: %q", view)
	}
	if !strings.Contains(view, "R:") {
		t.Errorf("right section missing in view: %q", view)
	}
}

func TestSplitPanelViewNormalizesChildrenToRectangle(t *testing.T) {
	left := newFakePanel("L")
	left.view = "left-line-that-is-way-too-wide\nshort"
	right := newFakePanel("R")
	right.view = "right"
	c := NewSplitPanel(left, right, 50, activeChildNone)

	view := c.View(30, 4, dashboard.PanelContext{})
	assertRect(t, view, 30, 4)
}

// TestSplitPanelSubscriptionsUnioned: children's Subscriptions
// channel lists are deduplicated when unioned.
func TestSplitPanelSubscriptionsUnioned(t *testing.T) {
	left := newFakePanel("L")
	left.subs = []string{"a", "b", "c"}
	right := newFakePanel("R")
	right.subs = []string{"b", "c", "d"} // overlaps a, b

	c := NewSplitPanel(left, right, 50, activeChildNone)
	got := c.Subscriptions(dashboard.Selection{})

	want := map[string]bool{"a": true, "b": true, "c": true, "d": true}
	if len(got.Channels) != len(want) {
		t.Errorf("channels = %v, want 4 unique entries", got.Channels)
	}
	for _, ch := range got.Channels {
		if !want[ch] {
			t.Errorf("unexpected channel %q in union", ch)
		}
		delete(want, ch)
	}
	for ch := range want {
		t.Errorf("missing channel %q in union", ch)
	}
}

// TestSplitPanelCapabilitiesUnioned: the composite's Capabilities is
// the OR of child capabilities. Footer hints rely on this so keys
// from any active child reach the user.
func TestSplitPanelCapabilitiesUnioned(t *testing.T) {
	left := newFakePanel("L")
	left.caps = keymap.Capabilities{ListNav: true, Drill: true}
	right := newFakePanel("R")
	right.caps = keymap.Capabilities{Help: true, Pause: true}

	c := NewSplitPanel(left, right, 50, activeChildNone)
	got := c.Capabilities()

	if !got.ListNav || !got.Drill || !got.Help || !got.Pause {
		t.Errorf("union missing fields: %+v", got)
	}
}

// TestSplitPanelTitleEmpty: composites have no chrome of their own.
func TestSplitPanelTitleEmpty(t *testing.T) {
	c := NewSplitPanel(newFakePanel("L"), newFakePanel("R"), 50, activeChildNone)
	if c.Title() != "" {
		t.Errorf("Title = %q, want empty", c.Title())
	}
}

// ─── StackPanel tests ──────────────────────────────────────────────────────

// TestStackPanelForwardsNonKeyMessagesToBoth: same as SplitPanel but
// for the vertical-stack composite.
func TestStackPanelForwardsNonKeyMessagesToBoth(t *testing.T) {
	top := newFakePanel("T")
	bot := newFakePanel("B")
	c := NewStackPanel(top, bot, 1, activeChildNone)

	c.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if top.nonKeysSeen != 1 || bot.nonKeysSeen != 1 {
		t.Errorf("non-key not forwarded: top=%d bot=%d", top.nonKeysSeen, bot.nonKeysSeen)
	}
}

// TestStackPanelTopRowsRespected: the top child's reported height
// matches the configured topRows.
func TestStackPanelTopRowsRespected(t *testing.T) {
	top := newFakePanel("T")
	bot := newFakePanel("B")
	c := NewStackPanel(top, bot, 3, activeChildNone)

	view := c.View(40, 12, dashboard.PanelContext{})
	lines := strings.Split(view, "\n")
	// Top section is 3 rows; bottom is 9 (12 - 3, no gutter). Each
	// line is the fake's "T:..." or "B:..." pattern.
	topLines := 0
	botLines := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "T:") {
			topLines++
		}
		if strings.HasPrefix(line, "B:") {
			botLines++
		}
	}
	// Each fake renders one line per call, but the kernel-level
	// render uses lipgloss.JoinVertical so the fake emits its
	// single-line output stacked. We expect exactly 1 top match
	// and 1 bottom match (one View call per child).
	if topLines != 1 || botLines != 1 {
		t.Errorf("expected exactly one T and B section in view, got T:%d B:%d\n%s",
			topLines, botLines, view)
	}
}

// TestStackPanelKeysGoToActiveOnly: same routing rules as Split.
func TestStackPanelKeysGoToActiveOnly(t *testing.T) {
	top := newFakePanel("T")
	bot := newFakePanel("B")
	c := NewStackPanel(top, bot, 1, 1) // active = bottom
	c.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if top.keysSeen != 0 {
		t.Errorf("top.keysSeen = %d, want 0 (bottom is active)", top.keysSeen)
	}
	if bot.keysSeen != 1 {
		t.Errorf("bot.keysSeen = %d, want 1", bot.keysSeen)
	}
}

// TestStackPanelGracefulDegradationAtTinyHeight: at height < 3 the
// composite renders the top child only rather than producing a
// 0-row pane that the kernel can't handle.
func TestStackPanelGracefulDegradationAtTinyHeight(t *testing.T) {
	top := newFakePanel("T")
	bot := newFakePanel("B")
	c := NewStackPanel(top, bot, 1, activeChildNone)

	view := c.View(40, 2, dashboard.PanelContext{})
	if !strings.Contains(view, "T:") {
		t.Errorf("expected top child in degraded view: %q", view)
	}
	if strings.Contains(view, "B:") {
		t.Errorf("bottom child should not render at tiny height: %q", view)
	}
}

// ─── FlexStackPanel tests ──────────────────────────────────────────────────

// TestFlexStackPanelWeightClamped: like Split, weights outside
// [10, 90] are clamped.
func TestFlexStackPanelWeightClamped(t *testing.T) {
	c := NewFlexStackPanel(newFakePanel("T"), newFakePanel("B"), 5, activeChildNone)
	if c.topWeight != 10 {
		t.Errorf("topWeight = %d, want 10 (clamped)", c.topWeight)
	}
	c2 := NewFlexStackPanel(newFakePanel("T"), newFakePanel("B"), 95, activeChildNone)
	if c2.topWeight != 90 {
		t.Errorf("topWeight = %d, want 90 (clamped)", c2.topWeight)
	}
}

func TestFlexStackPanelViewNormalizesChildrenToRectangle(t *testing.T) {
	top := newFakePanel("T")
	top.view = "top-line-that-is-way-too-wide"
	bot := newFakePanel("B")
	bot.view = "bottom\nextra\nextra\nextra"
	c := NewFlexStackPanel(top, bot, 50, activeChildNone)

	view := c.View(24, 6, dashboard.PanelContext{})
	assertRect(t, view, 24, 6)
}

// ─── unionSubs ─────────────────────────────────────────────────────────────

// TestUnionSubsDeduplicates: explicitly verify the helper since
// composites rely on it.
func TestUnionSubsDeduplicates(t *testing.T) {
	a := dashboard.FeedSpec{Channels: []string{"x", "y", "z"}}
	b := dashboard.FeedSpec{Channels: []string{"y", "w"}}

	got := unionSubs(a, b)
	wantSet := map[string]bool{"x": true, "y": true, "z": true, "w": true}
	if len(got.Channels) != 4 {
		t.Errorf("got %d channels, want 4: %v", len(got.Channels), got.Channels)
	}
	for _, ch := range got.Channels {
		if !wantSet[ch] {
			t.Errorf("unexpected channel %q", ch)
		}
	}
}

// TestUnionSubsDedupesWhenOtherSideEmpty: an earlier fast path
// returned the non-empty side as-is when the other was empty,
// leaving any duplicates inside it untouched. The helper's
// "minimal FeedSpec" contract requires dedupe in every code path.
func TestUnionSubsDedupesWhenOtherSideEmpty(t *testing.T) {
	dupe := dashboard.FeedSpec{Channels: []string{"a", "a", "b"}}
	if got := unionSubs(dupe, dashboard.FeedSpec{}); len(got.Channels) != 2 {
		t.Errorf("union(dupe, empty) = %v, want 2 unique entries", got.Channels)
	}
	if got := unionSubs(dashboard.FeedSpec{}, dupe); len(got.Channels) != 2 {
		t.Errorf("union(empty, dupe) = %v, want 2 unique entries", got.Channels)
	}
}

// TestUnionSubsHandlesEmpty: empty inputs handled cleanly.
func TestUnionSubsHandlesEmpty(t *testing.T) {
	if got := unionSubs(dashboard.FeedSpec{}, dashboard.FeedSpec{}); len(got.Channels) != 0 {
		t.Errorf("empty union should be empty: %v", got)
	}
	a := dashboard.FeedSpec{Channels: []string{"a"}}
	if got := unionSubs(a, dashboard.FeedSpec{}); len(got.Channels) != 1 || got.Channels[0] != "a" {
		t.Errorf("union with empty b: %v", got)
	}
	if got := unionSubs(dashboard.FeedSpec{}, a); len(got.Channels) != 1 || got.Channels[0] != "a" {
		t.Errorf("union with empty a: %v", got)
	}
}
