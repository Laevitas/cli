package panels

import (
	"strings"
	"testing"

	"github.com/laevitas/cli/internal/dashboard"
)

func TestCardPanelViewNormalizesInnerBodyBeforeBorder(t *testing.T) {
	inner := newFakePanel("inner")
	inner.view = strings.Join([]string{
		"this-line-is-too-wide-for-the-card-content",
		"short",
		"extra",
		"extra",
		"extra",
	}, "\n")
	card := NewCardPanel(inner, "CARD")

	view := card.View(24, 5, dashboard.PanelContext{})
	assertRect(t, view, 24, 5)
}
