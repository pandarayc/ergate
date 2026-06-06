package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

var foldStyle = lipgloss.NewStyle().Foreground(Accent).Bold(true)

// FoldToggle renders a fold/expand indicator line.
type FoldToggle struct {
	Collapsed bool
	Prefix    string
	Hint      string
}

// View renders the toggle line.
func (f FoldToggle) View() string {
	if f.Collapsed && f.Hint != "" {
		return foldStyle.Render(fmt.Sprintf("─── [+] %s ─ click to expand", f.Hint))
	}
	return foldStyle.Render("─── [-] click to collapse ───")
}
