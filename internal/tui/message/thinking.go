package message

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// MaxThinkingLines is the fold threshold for thinking output.
var MaxThinkingLines = DefaultMaxThinkingLines

// renderThinking renders a thinking/reasoning message.
// Returns empty string if thinking is suppressed (caller should filter before adding to list).
func renderThinking(m *ChatMessage, width int) string {
	contentW := max(width-4, 20)
	bar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("│")

	_, overflow, total := foldOutput(m.Content, contentW, MaxThinkingLines)
	if overflow && !m.wasFolded {
		m.Collapsed = true
		m.wasFolded = true
	}

	if overflow {
		if m.Collapsed {
			hint := ThinkingStyle.Render("[thinking]") + " " + FoldStyle.Render(
				fmt.Sprintf("─── %d lines ─ click to expand", total),
			)
			return bar + " " + hint
		}
		fold := foldView(false, "")
		return bar + " " + ThinkingStyle.Render("[thinking] "+m.Content) + "\n" + fold
	}

	return bar + " " + ThinkingStyle.Render("[thinking] "+m.Content)
}
