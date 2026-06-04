package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/raydraw/ergate/internal/util"
)

// View renders the chat page.
func (m *ChatModel) View() string {
	var b strings.Builder

	// Header (fixed at top)
	headerStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true).Padding(0, 1)
	b.WriteString(headerStyle.Render("Ergate"))
	b.WriteString(lipgloss.NewStyle().Foreground(Muted).Width(m.viewport.Width).Render(fmt.Sprintf("  model: %s\n", m.cfg.Model)))

	// Welcome page when no messages
	if len(m.messages) == 0 {
		welcome := lipgloss.NewStyle().Foreground(Muted).Padding(1).Render(
			"Welcome to Ergate!\n\n" +
				"  Ctrl+C  Quit\n" +
				"  ↑/↓     Input history\n" +
				"  PgUp/Dn Scroll viewport\n" +
				"  Ctrl+P  Previous input\n" +
				"  Ctrl+N  Next input\n" +
				"\nType a message to start...",
		)
		b.WriteString(welcome)
	} else {
		b.WriteString(m.renderContent())
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
	m.input.SyncHeight()
	m.syncViewportHeight()

	// Set viewport — preserve scroll position when user scrolled up,
	// except after session restore which forces scroll to bottom.
	content := b.String()
	wrapped := prewrapContent(content, m.viewport.Width)
	m.copyMode.SetContent(wrapped)
	if m.copyMode.IsActive() {
		wrapped = m.copyMode.Highlight(wrapped)
	}
	atBottom := m.viewport.AtBottom() || m.forceScrollBottom
	m.viewport.SetContent(wrapped)
	if atBottom {
		m.viewport.GotoBottom()
	}
	m.forceScrollBottom = false

	// Footer area outside viewport.
	var bottom strings.Builder

	// Tools bar — between viewport and spacer.
	if tb := m.toolsBar.View(m.width); tb != "" {
		bottom.WriteString(tb)
		bottom.WriteString("\n")

		// Populate footer widgets for hit testing.
		m.layout.Footer = m.layout.Footer[:0]
		tbBaseY := m.viewport.Height
		collapsed := !m.toolsBar.Expanded && len(m.toolsBar.Items) > maxToolBarLines
		n := len(m.toolsBar.Items)
		if collapsed {
			n = maxToolBarLines - 1
		}
		for i := 0; i < n; i++ {
			m.layout.Footer = append(m.layout.Footer, Widget{
				Kind:   WidgetToolbar,
				Y:      tbBaseY + i,
				Height: 1,
				Index:  i,
			})
		}
		if collapsed {
			m.layout.Footer = append(m.layout.Footer, Widget{
				Kind:   WidgetToolbar,
				Y:      tbBaseY + (maxToolBarLines - 1),
				Height: 1,
				Index:  -1, // fold toggle row
			})
		}
	} else {
		m.layout.Footer = m.layout.Footer[:0]
	}

	// Spacer
	spacerColor := lipgloss.NewStyle().Foreground(BorderDim)
	bottom.WriteString(spacerColor.Render("───"))
	bottom.WriteString("\n")

	accentBar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("┃")

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
	cacheHit, cacheMiss := m.eng.CacheUsage()
	modelOpts := m.cfg.ActiveModelOptions()
	sb := StatusBar{
		Turn:            m.currentTurn,
		TotalIn:         in,
		TotalOut:        out,
		Model:           m.cfg.Model,
		CacheRatio:      m.eng.CacheRatio(),
		SessionID:       m.sessionID,
		Running:         m.running,
		CacheHitTokens:  cacheHit,
		CacheMissTokens: cacheMiss,
		ContextWindow:   modelOpts.ContextWindow,
		ModelOpts:       modelOpts,
	}
	bottom.WriteString(accentBar + sb.View())

	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), bottom.String())
}

// headerLines account for the Ergate title + model line rendered in View()
// before renderContent output. Widget Y values must include this offset to
// align with contentY = mouseY + YOffset (which is full-viewport-content-relative).
// The header is " Ergate   model: xxx\n" → 1 text line + 1 empty line = 2 lines.
const headerLines = 2

// renderContent builds the message viewport content from cached renders.
// Only the last maxVisible messages are included.
// Also populates layout.Content for use by hit-test dispatch.
func (m *ChatModel) renderContent() string {
	const maxVisible = 50
	msgs := m.messages
	start := 0
	if len(msgs) > maxVisible {
		start = len(msgs) - maxVisible
	}

	m.layout.Content = m.layout.Content[:0]
	m.layout.ContentHeight = 0

	var b strings.Builder
	var prevRole string
	y := 0
	for i := start; i < len(msgs); i++ {
		msg := &msgs[i]
		if msg.Role == "user" && prevRole != "" && prevRole != "user" {
			b.WriteString("\n")
			y++
		}
		if msg.Role == "assistant" && prevRole != "assistant" {
			b.WriteString("\n")
			y++
		}

		rendered := m.renderMessage(msg)
		b.WriteString(rendered)
		b.WriteString("\n")
		vis := visualLineCount(rendered, m.viewport.Width)

		if msg.wasFolded && (msg.Role == "tool" || msg.Role == "thinking") {
			// Widget covers the full content extent (rendered lines + trailing newline).
			m.layout.Content = append(m.layout.Content, Widget{
				Kind:   WidgetMessage,
				Y:      headerLines + y,
				Height: vis + 1,
				Index:  i,
				Label:  msg.Content,
			})
		}
		y += vis + 1
		prevRole = msg.Role
	}
	m.layout.ContentHeight = headerLines + y
	return b.String()
}

// renderMessage renders a single message, caching the result.
func (m *ChatModel) renderMessage(msg *ChatMessage) string {
	if !msg.dirty && msg.rendered != "" {
		return msg.rendered
	}

	debugf("renderMessage: role=%s dirty=%v collapsed=%v wasFolded=%v contentLen=%d", msg.Role, msg.dirty, msg.Collapsed, msg.wasFolded, len(msg.Content))
	contentW := max(m.viewport.Width-4, 20)
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
			src = msg.Detail
		}
		_, overflow, total := foldToolOutput(src, contentW, maxToolOutputLines)
		if overflow && !msg.wasFolded {
			msg.Collapsed = true
			msg.wasFolded = true
		}
		if overflow {
			bar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("│")
			icon := bar + " " + AssistantToolStyle.Render(msg.Content)
			if msg.Collapsed {
				// Compact: icon + fold hint on one line.
				hint := icon + " " + foldStyle.Render(
					fmt.Sprintf("─── %d lines ─ click to expand", total),
				)
				result = hint
			} else {
				fold := FoldToggle{Collapsed: false, Prefix: bar + " ", Hint: ""}
				result = icon + "\n" + bar + " " + AssistantToolStyle.Render(src) + "\n" + fold.View()
			}
		} else {
			s := AssistantToolStyle.Render(msg.Content)
			if msg.Detail != "" && msg.Content != msg.Detail {
				disp := renderToolDetail(msg.Content, msg.Detail)
				s += "\n" + ToolResultStyle.Render(disp)
			}
			result = s
		}
	case "thinking":
		if m.hideThinking {
			result = ""
			break
		}
		_, overflow, total := foldToolOutput(msg.Content, contentW, maxThinkingLines)
		if overflow && !msg.wasFolded {
			msg.Collapsed = true
			msg.wasFolded = true
		}
		if overflow {
			bar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("│")
			if msg.Collapsed {
				// Compact single line: thinking label doubles as fold bar.
				hint := ThinkingStyle.Render("[thinking]") + " " + foldStyle.Render(
					fmt.Sprintf("─── %d lines ─ click to expand", total),
				)
				result = bar + " " + hint
			} else {
				fold := FoldToggle{Collapsed: false, Prefix: bar + " ", Hint: ""}
				result = bar + " " + ThinkingStyle.Render("[thinking] " + msg.Content) + "\n" + fold.View()
			}
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
	msg.dirty = false
	return result
}

// editInput mirrors the tool input for parsing Edit tool detail JSON.
type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

// renderToolDetail renders tool detail with diff-style coloring.
func renderToolDetail(toolLine, detail string) string {
	if strings.Contains(toolLine, "Edit") || strings.Contains(toolLine, "edit") {
		// Try to parse as JSON input; if that fails, fall back to line-level coloring.
		var in editInput
		if err := json.Unmarshal([]byte(detail), &in); err == nil && in.OldString != "" {
			return renderEditDiff(in.FilePath, in.OldString, in.NewString)
		}
		// Fallback: color lines that look like unified diff output
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

// renderEditDiff generates a colourised visual diff from old_string/new_string.
func renderEditDiff(filePath, oldStr, newStr string) string {
	var b strings.Builder

	if filePath != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(Info).Bold(true).Render("─── " + filePath + " ───"))
		b.WriteString("\n")
	}

	// Show removed lines (old_string) in red with "- " prefix.
	for _, line := range strings.Split(oldStr, "\n") {
		b.WriteString(DiffRemovedStyle.Render("- " + line))
		b.WriteString("\n")
	}

	// Show added lines (new_string) in green with "+ " prefix.
	for _, line := range strings.Split(newStr, "\n") {
		b.WriteString(DiffAddedStyle.Render("+ " + line))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (expand with Enter)"
}

// prewrapContent wraps each logical line to the given width using ANSI-aware
// word wrapping. This ensures viewport visual rows map 1:1 to \n-delimited
// lines, which fixes coordinate mapping for copy mode selection.
func prewrapContent(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = ansi.Wordwrap(line, width, " ")
		}
	}
	return strings.Join(lines, "\n")
}
