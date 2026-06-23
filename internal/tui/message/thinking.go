package message

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
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
			// Show first line as preview, not just line count.
			firstLine := strings.SplitN(m.Content, "\n", 2)[0]
			if len(firstLine) > 80 {
				firstLine = firstLine[:77] + "..."
			}
			hint := ThinkingStyle.Render("[thinking]") + " " +
				lipgloss.NewStyle().Foreground(Muted).Render(firstLine) + "  " +
				FoldStyle.Render(fmt.Sprintf("── %d lines · click to expand", total))
			return bar + " " + hint
		}
		fold := foldView(false, "")
		return bar + " " + ThinkingStyle.Render("[thinking] "+m.Content) + "\n" + fold
	}

	return bar + " " + ThinkingStyle.Render("[thinking] "+m.Content)
}
