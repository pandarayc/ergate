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
			b.WriteString(renderMessage(msg, m.viewport.Width))
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

	// Sync layout before rendering viewport.
	m.syncInputHeight()
	m.syncViewportHeight()

	// Set viewport — preserve scroll position when user scrolled up,
	// except after session restore which forces scroll to bottom.
	atBottom := m.viewport.AtBottom() || m.forceScrollBottom
	m.viewport.SetContent(b.String())
	if atBottom {
		m.viewport.GotoBottom()
	}
	m.forceScrollBottom = false

	// Tools bar — between viewport and spacer, no accent.
	var bottom strings.Builder
	if tb := m.toolsBar.View(m.width); tb != "" {
		bottom.WriteString(tb)
		bottom.WriteString("\n")
	}

	// Spacer
	spacerColor := lipgloss.NewStyle().Foreground(BorderDim)
	bottom.WriteString(spacerColor.Render("───"))
	bottom.WriteString("\n")

	accentBar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("┃")

	if m.overlay != nil {
		switch m.overlay.Kind {
		case OverlayPermission:
			bottom.WriteString(accentBar)
			bottom.WriteString(renderPermDialog(m.overlay.ToolName, m.overlay.Summary, m.overlay.Selected, m.width))
			bottom.WriteString("\n")
		case OverlayDetail:
			// Render detail as centered modal, then skip footer.
			detailView := renderDetailOverlay(m.overlay, m.width, m.height)
			return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), detailView)
		}
	}

	// Input area.
	inputView := InputAreaStyle.Render(m.input.View())
	for i, line := range strings.Split(inputView, "\n") {
		if i > 0 {
			bottom.WriteString("\n")
		}
		bottom.WriteString(accentBar + line)
	}
	bottom.WriteString("\n")

	// Status bar
	in, out := m.eng.TotalUsage()
	totalTokens := in + out
	ctxPct := 0
	if totalTokens > 0 {
		ctxPct = totalTokens * 100 / 128000
	}
	cost := estimateCost(m.cfg.Model, in, out)
	cacheRatio := m.eng.CacheRatio()
	cachePart := fmt.Sprintf(" | cache:%d%%", cacheRatio)
	if cacheRatio < 100 {
		cachePart = fmt.Sprintf(" | %s", lipgloss.NewStyle().Foreground(Warning).Render(fmt.Sprintf("cache:%d%%", cacheRatio)))
	}
	status := fmt.Sprintf(" turn:%d | ctx:%d%%%s | $%.4f", m.currentTurn, ctxPct, cachePart, cost)
	if m.sessionID != "" {
		status += fmt.Sprintf(" | %s", truncateStr(m.sessionID, 12))
	}
	if m.running {
		status = " ⏳" + status
	}
	statusLine := StatusBarStyle.Render(" " + status + " ")
	bottom.WriteString(accentBar + statusLine)

	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), bottom.String())
}

func renderMessage(msg *ChatMessage, vpWidth int) string {
	if msg.rendered != "" {
		return msg.rendered
	}
	// Content area width: viewport minus left padding/border.
	contentW := max(vpWidth-4, 20)
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
		src := msg.Content
		if msg.Detail != "" && msg.Content != msg.Detail {
			src = msg.Detail // use Detail for fold check on tool USE (input) and RESULT (output)
		}
		display, overflow, total := foldToolOutput(src, contentW)
		// First render: detect overflow and mark collapsed.
		if overflow && msg.rendered == "" {
			msg.Collapsed = true
			msg.wasFolded = true
		}
		if msg.Collapsed && overflow {
			remaining := total - maxToolOutputLines + 1
			fold := lipgloss.NewStyle().Foreground(Accent).Render(
				fmt.Sprintf("[+] %d more lines — click to expand", remaining),
			)
			result = AssistantToolStyle.Render(display) + "\n" + fold
		} else if !msg.Collapsed && overflow {
			collapse := lipgloss.NewStyle().Foreground(Accent).Render("[-] click to collapse")
			result = AssistantToolStyle.Render(src) + "\n" + collapse
		} else {
			s := AssistantToolStyle.Render(msg.Content)
			if msg.Detail != "" && msg.Content != msg.Detail {
				disp := renderToolDetail(msg.Content, msg.Detail)
				s += "\n" + ToolResultStyle.Render(disp)
			}
			result = s
		}
	case "thinking":
		thinkW := max(contentW-12, 20) // "[thinking] " prefix
		display, overflow, total := foldToolOutput(msg.Content, thinkW)
		if overflow && msg.rendered == "" {
			msg.Collapsed = true
			msg.wasFolded = true
		}
		if msg.Collapsed && overflow {
			remaining := total - maxToolOutputLines + 1
			fold := lipgloss.NewStyle().Foreground(Accent).Render(
				fmt.Sprintf("[+] %d more lines — click to expand", remaining),
			)
			result = ThinkingStyle.Width(contentW).Render("[thinking] " + display) + "\n" + fold
		} else if !msg.Collapsed && overflow {
			collapse := lipgloss.NewStyle().Foreground(Accent).Render("[-] click to collapse")
			result = ThinkingStyle.Width(contentW).Render("[thinking] " + msg.Content) + "\n" + collapse
		} else {
			result = ThinkingStyle.Width(contentW).Render("[thinking] " + msg.Content)
		}
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
