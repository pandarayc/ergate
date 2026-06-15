package message

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MaxToolOutputLines is the fold threshold for tool output.
var MaxToolOutputLines = DefaultMaxToolOutputLines

// renderTool renders a tool call or tool result message.
// Tool calls have Content="⚙ Name" and Detail="json input" (Content != Detail).
// Tool results have Content==Detail (both are the output text).
func renderTool(m *ChatMessage, width int) string {
	contentW := max(width-4, 20)
	bar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("│")

	// Determine what to fold: for tool calls, fold the Detail (JSON input).
	// For tool results, fold the Content (output text).
	isToolCall := m.Detail != "" && m.Content != m.Detail
	src := m.Content
	if isToolCall {
		src = m.Detail
	}

	_, overflow, total := foldOutput(src, contentW, MaxToolOutputLines)
	if overflow && !m.wasFolded {
		m.Collapsed = true
		m.wasFolded = true
		m.Bump()
	}

	if overflow {
		if m.Collapsed {
			// Tool call: show icon line + fold hint on one line.
			// Tool result: show first line of content + fold hint.
			if isToolCall {
				label := ToolStyle.Render(m.Content)
				hint := FoldStyle.Render(fmt.Sprintf("─── %d lines ─ click to expand", total))
				return bar + " " + label + "\n" + hint
			}
			// Tool result: show first line preview + fold hint on next line.
			firstLine := strings.SplitN(m.Content, "\n", 2)[0]
			hint := FoldStyle.Render(fmt.Sprintf("─── %d lines ─ click to expand", total))
			return bar + " " + ToolResultStyle.Render(firstLine) + "\n" + hint
		}

		// Expanded.
		fold := foldView(false, "")
		if isToolCall {
			label := ToolStyle.Render(m.Content)
			return label + "\n" + bar + " " + ToolResultStyle.Render(src) + "\n" + fold
		}
		// Tool result expanded: show full content.
		return bar + " " + ToolResultStyle.Render(m.Content) + "\n" + fold
	}

	// No overflow.
	if isToolCall {
		result := bar + " " + ToolStyle.Render(m.Content)
		disp := renderToolDetail(m.Content, m.Detail)
		result += "\n" + ToolResultStyle.Render(disp)
		return result
	}
	// Tool result, no overflow: just show the content.
	return bar + " " + ToolResultStyle.Render(m.Content)
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
		var in editInput
		if err := json.Unmarshal([]byte(detail), &in); err == nil && in.OldString != "" {
			return renderEditDiff(in.FilePath, in.OldString, in.NewString)
		}
		var out strings.Builder
		for _, line := range strings.Split(detail, "\n") {
			trimmed := strings.TrimLeft(line, " \t")
			switch {
			case strings.HasPrefix(trimmed, "+"):
				out.WriteString(DiffAdded.Render(line))
			case strings.HasPrefix(trimmed, "-"):
				out.WriteString(DiffRemoved.Render(line))
			case strings.HasPrefix(trimmed, "@@"):
				out.WriteString(DiffHunk.Render(line))
			default:
				out.WriteString(ToolResultStyle.Render(line))
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

	for _, line := range strings.Split(oldStr, "\n") {
		b.WriteString(DiffRemoved.Render("- " + line))
		b.WriteString("\n")
	}

	for _, line := range strings.Split(newStr, "\n") {
		b.WriteString(DiffAdded.Render("+ " + line))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
