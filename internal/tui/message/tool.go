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

// renderToolDetail renders tool detail with useful info extracted from JSON input.
func renderToolDetail(toolLine, detail string) string {
	// Parse JSON to extract meaningful fields.
	var raw map[string]any
	if json.Unmarshal([]byte(detail), &raw) != nil {
		return truncateStr(detail, 200)
	}

	switch {
	case strings.Contains(toolLine, "Bash") || strings.Contains(toolLine, "bash"):
		if cmd, ok := raw["command"].(string); ok {
			return ToolResultStyle.Render("$ " + truncateStr(cmd, 200))
		}
	case strings.Contains(toolLine, "Edit") || strings.Contains(toolLine, "edit"):
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
	default:
		if fp, ok := raw["file_path"].(string); ok && fp != "" {
			return lipgloss.NewStyle().Foreground(Info).Render(fp)
		}
	}

	// Re-encode cleanly to eliminate Unicode escapes.
	clean, _ := json.MarshalIndent(raw, "", "  ")
	return ToolResultStyle.Render(string(clean))
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

// renderToolChain renders a tool chain message with head/tail summary.
// The Detail field contains JSON-encoded []ToolChainItem.
func renderToolChain(m *ChatMessage, width int) string {
	if m.Detail == "" {
		return m.Content
	}
	contentW := max(width-4, 20)
	bar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("│")

	var items []struct {
		Name    string `json:"name"`
		Input   string `json:"input"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal([]byte(m.Detail), &items); err != nil || len(items) == 0 {
		return bar + " " + ToolStyle.Render(m.Content)
	}

	var b strings.Builder

	// Header: chain summary line.
	passedCount := 0
	errorCount := 0
	for _, it := range items {
		if it.IsError {
			errorCount++
		} else {
			passedCount++
		}
	}
	statusIcon := "✓"
	if errorCount > 0 {
		statusIcon = "✗"
	}
	b.WriteString(bar + " " + ToolStyle.Render(m.ChainSummary) + " " + statusIcon)
	b.WriteString("\n")

	// Each tool: name + head/tail summary.
	for _, it := range items {
		b.WriteString(bar + "  " + renderToolChainItem(it, contentW-2))
	}

	// Footer: hint.
	b.WriteString(FoldStyle.Render("═══ Ctrl+O 展开 · 点击展开 ═══"))

	return b.String()
}

// renderToolChainItem renders a single tool within a chain as head/tail summary.
func renderToolChainItem(item struct {
	Name    string "json:\"name\""
	Input   string "json:\"input\""
	Content string "json:\"content\""
	IsError bool   "json:\"is_error\""
}, width int) string {
	// Extract meaningful fields from input.
	var filePath, command string
	var rawInput map[string]interface{}
	if json.Unmarshal([]byte(item.Input), &rawInput) == nil {
		if fp, ok := rawInput["file_path"].(string); ok {
			filePath = fp
		}
		if cmd, ok := rawInput["command"].(string); ok {
			command = cmd
		}
	}

	lines := strings.Split(item.Content, "\n")
	totalLines := len(lines)

	var b strings.Builder

	switch {
	case item.IsError:
		// Error: show first 3 lines.
		errStyle := lipgloss.NewStyle().Foreground(Error)
		b.WriteString(errStyle.Render(item.Name + " ✗"))
		if filePath != "" {
			b.WriteString(" " + lipgloss.NewStyle().Foreground(Muted).Render(filePath))
		}
		b.WriteString("\n")
		maxLines := min(totalLines, 3)
		for i := 0; i < maxLines; i++ {
			b.WriteString("    " + errStyle.Render(truncateLine(lines[i], width-4)))
			b.WriteString("\n")
		}
		if totalLines > maxLines {
			b.WriteString(FoldStyle.Render(fmt.Sprintf("    ... %d more lines\n", totalLines-maxLines)))
		}

	case item.Name == "Edit":
		// Edit: show diff summary (head + tail).
		b.WriteString(ToolResultStyle.Render(item.Name))
		if filePath != "" {
			b.WriteString(" " + lipgloss.NewStyle().Foreground(Info).Render(filePath))
		}
		// Count +/- lines.
		addCount, delCount := 0, 0
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), "+") {
				addCount++
			}
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), "-") {
				delCount++
			}
		}
		b.WriteString(lipgloss.NewStyle().Foreground(Success).Render(fmt.Sprintf(" (+%d", addCount)))
		b.WriteString(lipgloss.NewStyle().Foreground(Error).Render(fmt.Sprintf(" -%d)", delCount)))
		b.WriteString("\n")
		// Head 3 + tail 3.
		if totalLines <= 7 {
			for _, line := range lines {
				b.WriteString("    " + renderDiffLine(line))
				b.WriteString("\n")
			}
		} else {
			for i := 0; i < 3; i++ {
				b.WriteString("    " + renderDiffLine(lines[i]))
				b.WriteString("\n")
			}
			b.WriteString(FoldStyle.Render(fmt.Sprintf("    ... %d lines\n", totalLines-6)))
			for i := totalLines - 3; i < totalLines; i++ {
				if i >= 3 {
					b.WriteString("    " + renderDiffLine(lines[i]))
					b.WriteString("\n")
				}
			}
		}

	case item.Name == "Bash":
		// Bash: show command + head/tail output.
		status := "✓"
		statusStyle := lipgloss.NewStyle().Foreground(Success)
		b.WriteString(ToolResultStyle.Render(item.Name + " ") + statusStyle.Render(status))
		if command != "" {
			b.WriteString("  " + lipgloss.NewStyle().Foreground(Muted).Render("$ "+truncateStr(command, width-10)))
		}
		b.WriteString("\n")
		if totalLines <= 7 {
			for _, line := range lines {
				b.WriteString("    " + truncateLine(line, width-4))
				b.WriteString("\n")
			}
		} else {
			for i := 0; i < 3; i++ {
				b.WriteString("    " + truncateLine(lines[i], width-4))
				b.WriteString("\n")
			}
			b.WriteString(FoldStyle.Render(fmt.Sprintf("    ... %d lines\n", totalLines-6)))
			for i := totalLines - 3; i < totalLines; i++ {
				if i >= 3 {
					b.WriteString("    " + truncateLine(lines[i], width-4))
					b.WriteString("\n")
				}
			}
		}

	default:
		// Read, Grep, Glob, etc.: show head + tail + file name + line count.
		b.WriteString(ToolResultStyle.Render(item.Name + ":"))
		if filePath != "" {
			b.WriteString(" " + lipgloss.NewStyle().Foreground(Info).Render(filePath))
		}
		b.WriteString(FoldStyle.Render(fmt.Sprintf("  %d lines", totalLines)))
		b.WriteString("\n")
		if totalLines <= 7 {
			for _, line := range lines {
				b.WriteString("    " + truncateLine(line, width-4))
				b.WriteString("\n")
			}
		} else {
			for i := 0; i < 4; i++ {
				b.WriteString("    " + truncateLine(lines[i], width-4))
				b.WriteString("\n")
			}
			b.WriteString(FoldStyle.Render(fmt.Sprintf("    ...\n")))
			for i := totalLines - 3; i < totalLines; i++ {
				if i >= 4 {
					b.WriteString("    " + truncateLine(lines[i], width-4))
					b.WriteString("\n")
				}
			}
		}
	}

	return b.String()
}

// renderDiffLine renders a single line with diff coloring based on prefix.
func renderDiffLine(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "+"):
		return DiffAdded.Render(line)
	case strings.HasPrefix(trimmed, "-"):
		return DiffRemoved.Render(line)
	case strings.HasPrefix(trimmed, "@@"):
		return DiffHunk.Render(line)
	default:
		return ToolResultStyle.Render(line)
	}
}

// truncateLine truncates a single line to maxLen characters.
func truncateLine(line string, maxLen int) string {
	if maxLen < 4 {
		maxLen = 4
	}
	if len(line) <= maxLen {
		return line
	}
	return line[:maxLen-3] + "..."
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
