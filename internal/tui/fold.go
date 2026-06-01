package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// FoldToggle renders a fold/expand indicator bar. Use Prefix to add a leading
// decoration (e.g. the assistant sidebar "│ ").
// When Collapsed and Hint is set, shows "[+] <hint> — click to expand".
// Otherwise shows "[-] click to collapse".
type FoldToggle struct {
	Collapsed bool
	Prefix    string
	Hint      string // e.g. "5 more lines"; empty = use generic label
}

var foldStyle = lipgloss.NewStyle().Foreground(Accent).Bold(true)

// View renders the fold bar as a single line.
func (f FoldToggle) View() string {
	if f.Collapsed && f.Hint != "" {
		return f.Prefix + foldStyle.Render(
			fmt.Sprintf("─── [+] %s — click to expand ───", f.Hint),
		)
	}
	return f.Prefix + foldStyle.Render("─── [-] click to collapse ───")
}
