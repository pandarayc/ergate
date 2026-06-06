package message

import (
	"github.com/raydraw/ergate/internal/util"
)

// renderAssistant renders an assistant message with full markdown processing.
func renderAssistant(m *ChatMessage, width int) string {
	rendered := util.RenderMarkdown(m.Content, 0)
	if rendered == "" {
		return ""
	}
	bar := AssistantBorderStyle.Render("│")
	return bar + " " + AssistantTextStyle.Render(rendered)
}
