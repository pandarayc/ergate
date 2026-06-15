package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/raydraw/ergate/internal/config"
)

// StatusBar renders the turn, token, cache, cost, and session info.
type StatusBar struct {
	Turn       int
	TotalIn    int
	TotalOut   int
	LastTurnIn int // last turn input tokens for ctx% (current buffer utilization)
	Model      string
	CacheRatio int
	SessionID  string
	Running    bool

	// DeepSeek cache metrics (from API response)
	CacheHitTokens  int
	CacheMissTokens int

	// Last turn timing
	LastLatencyMS int64
	LastTTFTMS    int64

	// Model metadata from config
	ContextWindow int // 0 = use default 128000
	ModelOpts     config.ModelOptions
}

// View renders the status bar as a single line.
func (s StatusBar) View() string {
	// ctx% = last turn input tokens / context window (current buffer utilization).
	// Falls back to TotalIn if no last-turn data (legacy compatibility).
	ctxIn := s.LastTurnIn
	if ctxIn <= 0 {
		ctxIn = s.TotalIn
	}
	ctxWindow := s.ContextWindow
	if ctxWindow <= 0 {
		ctxWindow = 128000
	}
	ctxPct := 0
	if ctxIn > 0 {
		ctxPct = ctxIn * 100 / ctxWindow
	}
	cost := estimateCost(s.Model, s.TotalIn, s.TotalOut, s.ModelOpts)

	cachePart := fmt.Sprintf(" | cache:%d%%", s.CacheRatio)
	if s.CacheRatio < 100 {
		cachePart = fmt.Sprintf(" | %s", lipgloss.NewStyle().Foreground(Warning).Render(
			fmt.Sprintf("cache:%d%%", s.CacheRatio),
		))
	}

	// DeepSeek API cache hit rate (overrides cachestability ratio when available).
	totalCache := s.CacheHitTokens + s.CacheMissTokens
	if totalCache > 0 {
		hitPct := s.CacheHitTokens * 100 / totalCache
		cacheStyle := lipgloss.NewStyle().Foreground(Success)
		if hitPct < 50 {
			cacheStyle = lipgloss.NewStyle().Foreground(Warning)
		}
		if hitPct < 20 {
			cacheStyle = lipgloss.NewStyle().Foreground(Error)
		}
		cachePart = fmt.Sprintf(" | %s", cacheStyle.Render(
			fmt.Sprintf("cache:%d%%", hitPct),
		))
	}

	status := fmt.Sprintf(" turn:%d | ctx:%d%%%s", s.Turn, ctxPct, cachePart)
	if s.LastLatencyMS > 0 {
		status += fmt.Sprintf(" | %.1fs", float64(s.LastLatencyMS)/1000)
	}
	status += fmt.Sprintf(" | $%.4f", cost)
	if s.SessionID != "" {
		status += fmt.Sprintf(" | %s", truncateStr(s.SessionID, 12))
	}
	if s.Running {
		status = " ⏳" + status
	}
	return StatusBarStyle.Render(" " + status + " ")
}
