package tui

import (
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
)

// --- Spinner (moved from chat.go) ---

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerTickInterval = 80 * time.Millisecond

type spinnerTickMsg struct{}

func nextSpinnerTick() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// --- Engine event bridge (moved from chat.go) ---

type engineEventMsg struct {
	event engine.Event
}

// --- Cost estimation (moved from chat.go) ---

func estimateCost(model string, inTokens, outTokens int, opts ...config.ModelOptions) float64 {
	if len(opts) > 0 && opts[0].CostPer1MIn > 0 {
		o := opts[0]
		return float64(inTokens)/1e6*o.CostPer1MIn + float64(outTokens)/1e6*o.CostPer1MOut
	}

	rates := map[string]struct{ in, out float64 }{
		"claude-sonnet-4-20250514": {3.0, 15.0},
		"claude-opus-4-20250514":   {15.0, 75.0},
		"claude-haiku-3-5":         {0.8, 4.0},
		"gpt-4o":                   {2.5, 10.0},
		"gpt-4o-mini":              {0.15, 0.6},
		"deepseek-chat":            {0.27, 1.10},
		"deepseek-reasoner":        {0.55, 2.19},
	}
	prefixes := make([]string, 0, len(rates))
	for prefix := range rates {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	for _, prefix := range prefixes {
		if len(model) >= len(prefix) && model[:len(prefix)] == prefix {
			rate := rates[prefix]
			return float64(inTokens)/1e6*rate.in + float64(outTokens)/1e6*rate.out
		}
	}
	return float64(inTokens)/1e6*3.0 + float64(outTokens)/1e6*15.0
}

// --- Content wrapping (moved from chat_render.go) ---

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

// truncateStr truncates a string to maxLen characters.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// max returns the larger of a and b.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
