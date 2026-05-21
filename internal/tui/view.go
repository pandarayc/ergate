package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/raydraw/ergate/internal/util"
)

// View renders the full screen.
func (m Model) View() string {
	if m.quitting {
		return "\n  Goodbye!\n\n"
	}

	var b strings.Builder

	// Header (fixed at top)
	headerStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true).Padding(0, 1)
	b.WriteString(headerStyle.Render("Ergate"))
	b.WriteString(lipgloss.NewStyle().Foreground(Muted).Render(fmt.Sprintf("  model: %s\n\n", m.cfg.Model)))

	// Welcome page when no messages
	if len(m.messages) == 0 {
		welcome := lipgloss.NewStyle().Foreground(Muted).Padding(1).Render(
			"Welcome to Ergate!\n\n" +
				"  Ctrl+C  Quit\n" +
				"  ↑/↓     Scroll\n" +
				"  Ctrl+P  Previous input\n" +
				"  Ctrl+N  Next input\n" +
				"  PgUp/Dn Page scroll\n" +
				"\nType a message to start...",
		)
		b.WriteString(welcome)
	} else {
		// Messages with separation
		var prevRole string
		for i := range m.messages {
			msg := &m.messages[i]
			// Add blank line between user messages and previous content
			if msg.Role == "user" && prevRole != "" && prevRole != "user" {
				b.WriteString("\n")
			}
			// Add left border accent for assistant blocks
			if msg.Role == "assistant" && prevRole != "assistant" {
				b.WriteString("\n")
			}
			b.WriteString(renderMessage(msg))
			b.WriteString("\n")
			prevRole = msg.Role
		}
	}

	// Spinner with context
	if m.running {
		spinnerText := spinnerFrames[m.spinnerIdx] + " Thinking..."
		if m.currentToolName != "" {
			spinnerText = spinnerFrames[m.spinnerIdx] + " " + m.currentToolName + "..."
		}
		b.WriteString(SpinnerStyle.Render(spinnerText + "\n"))
	}

	b.WriteString("\n")

	// Set viewport
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()

	// Permission dialog
	var bottom strings.Builder
	if m.permActive {
		bottom.WriteString(renderPermDialog(m.permToolName, m.permSummary, m.permSelected, m.width))
		bottom.WriteString("\n")
	}

	// Input area
	inputView := InputAreaStyle.Render(m.input.View())
	bottom.WriteString(inputView)
	bottom.WriteString("\n")

	// Status bar
	in, out := m.eng.TotalUsage()
	totalTokens := in + out
	ctxPct := 0
	if totalTokens > 0 {
		ctxPct = totalTokens * 100 / 128000
	}
	cost := estimateCost(m.cfg.Model, in, out)
	status := fmt.Sprintf(" turn:%d | ctx:%d%% | $%.4f", m.currentTurn, ctxPct, cost)
	if m.sessionID != "" {
		status += fmt.Sprintf(" | %s", truncateStr(m.sessionID, 12))
	}
	if m.running {
		status = " ⏳" + status
	}
	bottom.WriteString(StatusBarStyle.Render(" " + status + " "))

	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), bottom.String())
}

func renderMessage(msg *ChatMessage) string {
	if msg.rendered != "" {
		return msg.rendered
	}
	var result string
	switch msg.Role {
	case "user":
		result = UserMsgStyle.Render("▸ ") + AssistantTextStyle.Render(msg.Content)
	case "assistant":
		rendered := util.RenderMarkdown(msg.Content, 0)
		if rendered != "" {
			result = AssistantBorderStyle.Render("│") + " " + AssistantTextStyle.Render(rendered)
		}
	case "tool":
		s := AssistantToolStyle.Render(msg.Content)
		if msg.Detail != "" {
			display := renderToolDetail(msg.Content, msg.Detail)
			s += "\n" + ToolResultStyle.Render(display)
		}
		result = s
	case "thinking":
		result = ThinkingStyle.Render("[thinking] " + truncateStr(msg.Content, 80))
	case "error":
		result = ErrorStyle.Render("✖ " + msg.Content)
	case "system":
		result = HelpStyle.Render("· " + msg.Content)
	default:
		result = msg.Content
	}
	msg.rendered = result
	return result
}

// renderToolDetail renders tool detail with diff-style coloring.
func renderToolDetail(toolLine, detail string) string {
	if strings.Contains(toolLine, "Edit") || strings.Contains(toolLine, "edit") {
		var out strings.Builder
		for _, line := range strings.Split(detail, "\n") {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "+") {
				out.WriteString(DiffAddedStyle.Render(line))
			} else if strings.HasPrefix(trimmed, "-") {
				out.WriteString(DiffRemovedStyle.Render(line))
			} else if strings.HasPrefix(trimmed, "@@") {
				out.WriteString(DiffHunkStyle.Render(line))
			} else {
				out.WriteString(MutedStyle(line))
			}
			out.WriteString("\n")
		}
		return strings.TrimRight(out.String(), "\n")
	}
	return truncateStr(detail, 100)
}

func renderPermDialog(toolName, summary string, selected int, width int) string {
	opts := []string{"Allow Once", "Always Allow", "Deny", "Always Deny"}
	dialogW := width * 60 / 100
	if dialogW > 72 {
		dialogW = 72
	}
	if dialogW < 40 {
		dialogW = 40
	}
	title := " Permission Required "
	var b strings.Builder
	b.WriteString("┌" + strings.Repeat("─", dialogW-2) + "┐\n")
	b.WriteString(fmt.Sprintf("│ %-*s │\n", dialogW-4, title))
	b.WriteString(fmt.Sprintf("│ Tool: %-*s │\n", dialogW-10, toolName))
	summaryLine := truncateStr(summary, dialogW-6)
	b.WriteString(fmt.Sprintf("│ %-*s │\n", dialogW-4, summaryLine))
	b.WriteString("│" + strings.Repeat("─", dialogW-2) + "│\n")
	for i, opt := range opts {
		cursor := "  "
		if i == selected {
			cursor = "▶ "
		}
		line := cursor + opt
		b.WriteString(fmt.Sprintf("│ %-*s │\n", dialogW-4, line))
	}
	b.WriteString("└" + strings.Repeat("─", dialogW-2) + "┘")
	return PermissionDialogStyle.Render(b.String())
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (expand with Enter)"
}
