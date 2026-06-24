package message

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// MaxThinkingLines is the fold threshold for thinking output.
var MaxThinkingLines = DefaultMaxThinkingLines

// renderThinking renders a thinking/reasoning message.
// When folded: shows first line preview + "— click to expand in pop layer".
// When expanded: shows full content with fold toggle.
func renderThinking(m *ChatMessage, width int) string {
	contentW := max(width-4, 20)
	bar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("│")

	_, overflow, total := foldOutput(m.Content, contentW, MaxThinkingLines)
	if overflow && !m.wasFolded {
		m.Collapsed = true
		m.wasFolded = true
		m.Bump()
	}

	if overflow {
		if m.Collapsed {
			// Show first line preview + fold hint on a second line,
			// matching opencode's multi-line collapsed format.
			// Truncate firstLine to fit within contentW so it never
			// wraps — otherwise the list cache height (2 lines) drifts
			// from the actual rendered line count and click Y mapping breaks.
			firstLine := strings.SplitN(m.Content, "\n", 2)[0]
			prefix := bar + " " + ThinkingStyle.Render("[thinking]") + " "
			avail := contentW - lipgloss.Width(prefix)
			if lipgloss.Width(firstLine) > avail {
				firstLine = ansi.Truncate(firstLine, avail-3, "...")
			}
			preview := prefix + lipgloss.NewStyle().Foreground(Muted).Render(firstLine)
			foldHint := FoldStyle.Render(fmt.Sprintf("│── %d lines · click to expand", total))
			return preview + "\n" + foldHint
		}
		fold := foldView(false, "")
		return bar + " " + ThinkingStyle.Render("[thinking] "+m.Content) + "\n" + fold
	}

	return bar + " " + ThinkingStyle.Render("[thinking] "+m.Content)
}
