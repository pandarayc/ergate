package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const maxToolBarLines = 8

// ToolsBarItem represents a single entry in the tools status bar.
type ToolsBarItem struct {
	Icon   string // ⚙ ☐ ✓ ✖ ⚑
	Label  string
	Expand string // content to show in detail overlay when clicked (empty = non-interactive)
}

// ToolsBar displays current tool/task progress between viewport and footer.
// Each item occupies one line. Items beyond 8 are collapsed into a fold line
// ("[+N more — Enter to expand]"); clicking/entering expands to show all.
type ToolsBar struct {
	Items    []ToolsBarItem
	Expanded bool // when true, show all items regardless of cap
}

// Height returns the number of lines the tools bar occupies.
func (tb *ToolsBar) Height() int {
	n := len(tb.Items)
	if n == 0 {
		return 0
	}
	if tb.Expanded || n <= maxToolBarLines {
		return n
	}
	return maxToolBarLines // collapsed: 7 items + 1 fold line
}

// View renders the tools bar: one muted line per item, with fold when collapsed.
func (tb *ToolsBar) View(width int) string {
	if len(tb.Items) == 0 {
		return ""
	}
	available := width - 2 // left padding
	row := lipgloss.NewStyle().Foreground(Muted).Padding(0, 1)
	accent := lipgloss.NewStyle().Foreground(Accent).Padding(0, 1)

	collapsed := !tb.Expanded && len(tb.Items) > maxToolBarLines

	var lines []string
	n := len(tb.Items)
	if collapsed {
		n = maxToolBarLines - 1 // leave room for fold line
	}

	for i := 0; i < n; i++ {
		line := tb.Items[i].Icon + " " + tb.Items[i].Label
		if len(line) > available && available > 5 {
			line = line[:available-3] + "..."
		}
		lines = append(lines, row.Render(line))
	}

	if collapsed {
		remaining := len(tb.Items) - (maxToolBarLines - 1)
		fold := accent.Render("[+] " + pluralize(remaining, "more item", "more items") + " — click to expand")
		lines = append(lines, fold)
	}

	return strings.Join(lines, "\n")
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + plural
}

// Set replaces all items, resetting expand state. nil or empty slice clears the bar.
func (tb *ToolsBar) Set(items []ToolsBarItem) {
	tb.Items = items
	tb.Expanded = false
}

// ToggleExpand flips the expanded/collapsed state.
func (tb *ToolsBar) ToggleExpand() {
	tb.Expanded = !tb.Expanded
}
