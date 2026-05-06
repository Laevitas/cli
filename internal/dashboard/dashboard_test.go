package dashboard

// FeedRouter tests covering the pre-dial state-machine.
//
// The post-dial race tests would require an injectable wsclient
// interface — currently the FeedRouter holds *wsclient.Client
// directly. That refactor is queued; for now the wsclient package's
// own state-machine tests (internal/wsclient/wsclient_test.go) cover
// the bugs that actually leak server-side state, and FeedRouter's
// pre-dial path is testable without a real connection because it
// only touches f.pending.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/laevitas/cli/internal/keymap"
)

type capsPanel struct {
	caps keymap.Capabilities
}

func (p capsPanel) Init() tea.Cmd                      { return nil }
func (p capsPanel) Update(tea.Msg) (Panel, tea.Cmd)    { return p, nil }
func (p capsPanel) View(int, int, PanelContext) string { return "" }
func (p capsPanel) Subscriptions(Selection) FeedSpec   { return FeedSpec{} }
func (p capsPanel) Title() string                      { return "" }
func (p capsPanel) Capabilities() keymap.Capabilities  { return p.caps }

// TestSubscribePreDialReplacesPending verifies that subscribe()
// before dial completes replaces (not accumulates) f.pending. This
// catches the original "additive drift" bug that motivated the
// kernel cleanup: Root.Init may call refreshSubscriptions multiple
// times before dial completes, and we want the latest desired set
// to win, not all the intermediate ones concatenated.
func TestSubscribePreDialReplacesPending(t *testing.T) {
	f := newFeedRouter("", "")
	defer f.cancel()

	f.subscribe([]string{"a", "b", "c"})
	if got := len(f.pending); got != 3 {
		t.Fatalf("after first subscribe: pending len = %d, want 3", got)
	}

	// Second subscribe with a different set must REPLACE, not append.
	f.subscribe([]string{"x", "y"})
	if got := len(f.pending); got != 2 {
		t.Fatalf("after second subscribe: pending len = %d, want 2 (replace, not append)", got)
	}

	// Verify contents match the second call exactly.
	want := map[string]bool{"x": true, "y": true}
	for _, ch := range f.pending {
		if !want[ch] {
			t.Fatalf("pending contains %q from the first call; expected only x and y", ch)
		}
	}
}

// TestChannelSetsEqual exercises the helper that gates the post-dial
// reconciliation. Order-independence and duplicate-handling matter
// because subscribe() callers may produce either.
func TestChannelSetsEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"empty empty", nil, nil, true},
		{"empty vs one", nil, []string{"x"}, false},
		{"identical order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"different order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"different content", []string{"a", "b"}, []string{"a", "c"}, false},
		{"different size", []string{"a"}, []string{"a", "b"}, false},
		{"duplicate one side", []string{"a", "a", "b"}, []string{"a", "b"}, true},
		// Codex round-4 caught this: same length, b has duplicates that
		// collapse to a smaller set than a. The earlier fast path returned
		// true because every element of b was in aSet. Must return false.
		{"same length, b has duplicates", []string{"a", "b"}, []string{"a", "a"}, false},
		{"same length, a has duplicates", []string{"a", "a"}, []string{"a", "b"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := channelSetsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("channelSetsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestRootActiveCapabilitiesUnionsPanelCapabilities(t *testing.T) {
	r := NewRoot(Config{
		Layout: LayoutSingle,
		Panels: map[PaneSlot]Panel{
			PaneMain: capsPanel{caps: keymap.Capabilities{
				ChartTimeframe: true,
				DepthTier:      true,
				MultiPane:      true,
			}},
		},
	})

	caps := r.activeCapabilities()
	if !caps.ChartTimeframe {
		t.Fatal("ChartTimeframe capability was dropped before footer/help rendering")
	}
	if !caps.DepthTier {
		t.Fatal("DepthTier capability was dropped before footer/help rendering")
	}
	if !caps.MultiPane {
		t.Fatal("MultiPane capability was dropped before footer/help rendering")
	}
}

// Note: a real test for SelectionChangedMsg's mutation contract
// (r.selection = msg.New must happen before broadcast) would need
// to drive Root.Update, which expects a Bubble Tea program context.
// Refactoring the handler purely for testability is not worth the
// churn for a 3-line branch; the contract is exercised end-to-end
// by `dash flow`'s screener→detail navigation in v0.10.0 and any
// regression there would surface immediately.
