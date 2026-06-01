package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders the turn, token, cache, cost, and session info.
type StatusBar struct {
	Turn       int
	TotalIn    int
	TotalOut   int
	Model      string
	CacheRatio int
	SessionID  string
	Running    bool
}

// View renders the status bar as a single line.
func (s StatusBar) View() string {
	totalTokens := s.TotalIn + s.TotalOut
	ctxPct := 0
	if totalTokens > 0 {
		ctxPct = totalTokens * 100 / 128000
	}
	cost := estimateCost(s.Model, s.TotalIn, s.TotalOut)

	cachePart := fmt.Sprintf(" | cache:%d%%", s.CacheRatio)
	if s.CacheRatio < 100 {
		cachePart = fmt.Sprintf(" | %s", lipgloss.NewStyle().Foreground(Warning).Render(
			fmt.Sprintf("cache:%d%%", s.CacheRatio),
		))
	}

	status := fmt.Sprintf(" turn:%d | ctx:%d%%%s | $%.4f", s.Turn, ctxPct, cachePart, cost)
	if s.SessionID != "" {
		status += fmt.Sprintf(" | %s", truncateStr(s.SessionID, 12))
	}
	if s.Running {
		status = " ⏳" + status
	}
	return StatusBarStyle.Render(" " + status + " ")
}
