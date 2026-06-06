package message

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// foldOutput determines if content needs folding and returns the display string.
// Returns (display, overflow, totalLines).
func foldOutput(content string, width, maxLines int) (string, bool, int) {
	if maxLines <= 0 {
		return content, false, 1
	}
	totalLines := visualLines(content, width)
	if totalLines <= maxLines {
		return content, false, totalLines
	}
	// Truncate to ~maxLines visual lines.
	lines := strings.Split(content, "\n")
	visual := 0
	cut := 0
	for i, line := range lines {
		lineWidth := len([]rune(line))
		if lineWidth == 0 {
			visual++
		} else {
			visual += (lineWidth + width - 1) / width
		}
		if visual >= maxLines-1 {
			cut = i + 1
			break
		}
	}
	if cut == 0 || cut > len(lines) {
		cut = len(lines)
	}
	return strings.Join(lines[:cut], "\n"), true, totalLines
}

// visualLines counts visual lines after ANSI-aware word wrapping.
// Must produce identical results as prewrapContent in the tui package.
func visualLines(text string, width int) int {
	width = max(width, 20)
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if len(line) == 0 {
			count++
		} else {
			wrapped := ansi.Wordwrap(line, width, " ")
			count += strings.Count(wrapped, "\n") + 1
		}
	}
	return count
}

// foldView renders the fold toggle line.
func foldView(collapsed bool, hint string) string {
	if collapsed && hint != "" {
		return FoldStyle.Render(fmt.Sprintf("─── [+] %s ─ click to expand", hint))
	}
	return FoldStyle.Render("─── [-] click to collapse ───")
}
