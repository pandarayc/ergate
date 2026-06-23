package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	detailMaxWidth  = 90
	detailMaxHeight = 30
	detailMinWidth  = 40
	detailMinHeight = 10
	detailDropdownN = 5 // max visible dropdown items
)

// detailSearch runs fuzzy case-insensitive search against content lines.
// Returns up to 20 matches sorted by match quality (earlier + contiguous > later).
func detailSearch(content, query string) []DetailMatch {
	if query == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	lowerQ := strings.ToLower(query)
	var matches []DetailMatch
	for i, line := range lines {
		lowerL := strings.ToLower(line)
		pos := strings.Index(lowerL, lowerQ)
		score := 0
		if pos >= 0 {
			score = 1000 - pos // earlier match = higher score
		} else if fuzzyMatch(lowerL, lowerQ) {
			score = 500
		}
		if score > 0 {
			matches = append(matches, DetailMatch{
				Line: i,
				Text: truncateForDisplay(line, 60),
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		// TODO: use actual score in sort
		aPos := strings.Index(strings.ToLower(matches[i].Text), lowerQ)
		bPos := strings.Index(strings.ToLower(matches[j].Text), lowerQ)
		if aPos >= 0 && bPos >= 0 {
			return aPos < bPos
		}
		return matches[i].Line < matches[j].Line
	})
	if len(matches) > 20 {
		matches = matches[:20]
	}
	return matches
}

// fuzzyMatch checks if query chars appear in order within s (fuzzy match).
func fuzzyMatch(s, query string) bool {
	j := 0
	for i := 0; i < len(s) && j < len(query); i++ {
		if s[i] == query[j] {
			j++
		}
	}
	return j == len(query)
}

func truncateForDisplay(s string, maxLen int) string {
	// Collapse whitespace and truncate.
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace && b.Len() > 0 {
				b.WriteRune(' ')
				prevSpace = true
			}
		} else {
			b.WriteRune(r)
			prevSpace = false
		}
		if b.Len() >= maxLen {
			break
		}
	}
	result := b.String()
	if len(s) > maxLen {
		result += "..."
	}
	return result
}

// renderPermissionOverlay renders the permission dialog with single-key options.
func renderPermissionOverlay(o *Overlay, width int) string {
	style := PermissionDialogStyle.Width(width - 2)
	toolStyle := lipgloss.NewStyle().Foreground(Warning).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(Muted)

	var b strings.Builder
	b.WriteString(toolStyle.Render(o.ToolName))
	if o.Summary != "" {
		b.WriteString(" " + dimStyle.Render(o.Summary))
	}
	b.WriteString("\n\n")
	b.WriteString(keyStyle.Render("y") + dimStyle.Render("  approve once"))
	b.WriteString("\n")
	b.WriteString(keyStyle.Render("a") + dimStyle.Render("  always allow"))
	b.WriteString("\n")
	b.WriteString(keyStyle.Render("n") + dimStyle.Render("  deny"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Esc  deny"))

	return style.Render(b.String())
}

// renderDetailOverlay renders the detail popup with search bar, dropdown, and content.
func renderDetailOverlay(o *Overlay, termW, termH int) string {
	w := termW * 80 / 100
	if w > detailMaxWidth {
		w = detailMaxWidth
	}
	if w < detailMinWidth {
		w = detailMinWidth
	}
	h := termH * 80 / 100
	if h > detailMaxHeight {
		h = detailMaxHeight
	}
	if h < detailMinHeight {
		h = detailMinHeight
	}

	innerW := w - 4 // inside border

	// Title bar
	title := renderDetailTitle(o, innerW)

	// Search input + dropdown
	searchBlock := renderDetailSearch(o, innerW)

	// Divider between search and content
	divider := lipgloss.NewStyle().Foreground(BorderDim).Width(innerW).Render(strings.Repeat("─", innerW))

	// Content area — remaining height after title + search + divider + hints + borders
	contentH := h - 2 - strings.Count(title, "\n") - 1 - strings.Count(searchBlock, "\n") - 1 - 1 - 1
	if contentH < 3 {
		contentH = 3
	}
	body := renderDetailBody(o, innerW, contentH)

	// Hints
	hints := renderDetailHints(o, innerW)

	// Compute where body content starts within the detail view (for copy mode).
	bodyLineY := 2 + strings.Count(title, "\n") + 1 +
		strings.Count(searchBlock, "\n") + 1 +
		strings.Count(divider, "\n") + 1
	o.ContentStartY = bodyLineY

	// Assemble
	inner := lipgloss.JoinVertical(lipgloss.Left,
		title,
		searchBlock,
		divider,
		body,
		"",
		hints,
	)

	return lipgloss.NewStyle().
		Width(w).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Accent).
		Padding(0, 1).
		Render(inner)
}

func renderDetailTitle(o *Overlay, w int) string {
	label := o.DetailTitle
	if label == "" {
		label = "Detail"
	}
	return lipgloss.NewStyle().
		Foreground(Accent).
		Bold(true).
		Width(w).
		Align(lipgloss.Left).
		Render(" " + label)
}

func renderDetailSearch(o *Overlay, w int) string {
	style := lipgloss.NewStyle().Foreground(Muted).Width(w)
	if !o.DetailSearchMode {
		return style.Render("  / to search")
	}

	// Search input line with distinct background
	cursor := " "
	if len(o.DetailSearchQ)%2 == 0 { // simple blink approximation
		cursor = "█"
	}
	inputLine := lipgloss.NewStyle().
		Foreground(Info).
		Background(BgPanel).
		Width(w).
		Render(" > " + o.DetailSearchQ + cursor)

	if len(o.DetailMatches) == 0 {
		if o.DetailSearchQ != "" {
			return inputLine + "\n" + style.Render("  no matches")
		}
		return inputLine
	}

	// Dropdown — selected item uses background highlight, no arrow.
	start := o.DetailMatchOff
	end := start + detailDropdownN
	if end > len(o.DetailMatches) {
		end = len(o.DetailMatches)
	}

	selBg := lipgloss.NewStyle().Background(Accent).Foreground(TextColor)
	norm := lipgloss.NewStyle().Foreground(Muted)

	var dd []string
	for i := start; i < end; i++ {
		m := o.DetailMatches[i]
		entry := fmt.Sprintf("  %d. %s", m.Line+1, m.Text)
		if i == o.DetailMatchIdx {
			entry = selBg.Width(w).Render(entry)
		} else {
			entry = norm.Render(entry)
		}
		dd = append(dd, entry)
	}
	dropdown := strings.Join(dd, "\n")

	return inputLine + "\n" + dropdown
}

func renderDetailBody(o *Overlay, w, maxLines int) string {
	if o.DetailContent == "" {
		return lipgloss.NewStyle().Foreground(Muted).Render("(empty)")
	}
	lines := strings.Split(o.DetailContent, "\n")

	// Scroll bounds
	if o.DetailScroll < 0 {
		o.DetailScroll = 0
	}
	if o.DetailScroll > len(lines)-maxLines {
		o.DetailScroll = len(lines) - maxLines
	}
	if o.DetailScroll < 0 {
		o.DetailScroll = 0
	}

	end := o.DetailScroll + maxLines
	if end > len(lines) {
		end = len(lines)
	}

	searchQ := ""
	if o.DetailSearchMode && o.DetailSearchQ != "" {
		searchQ = strings.ToLower(o.DetailSearchQ)
	}

	// Determine copy-mode selection range.
	cpStart, cpEnd := o.CopyAnchorY, o.CopyFocusY
	if cpStart > cpEnd {
		cpStart, cpEnd = cpEnd, cpStart
	}
	selStyle := lipgloss.NewStyle().Background(Accent).Foreground(TextColor)

	var visible []string
	for i := o.DetailScroll; i < end; i++ {
		line := lines[i]
		if len(line) > w-2 {
			line = line[:w-5] + "..."
		}
		// Highlight current search match
		if searchQ != "" && o.DetailMatchIdx >= 0 && o.DetailMatchIdx < len(o.DetailMatches) {
			if o.DetailMatches[o.DetailMatchIdx].Line == i {
				line = highlightMatch(line, searchQ)
			}
		}
		// Highlight copy-mode selection.
		if o.CopyActive && i >= cpStart && i <= cpEnd {
			line = selStyle.Render(line)
		}
		visible = append(visible, line)
	}

	return lipgloss.NewStyle().Foreground(TextColor).Width(w).
		Render(strings.Join(visible, "\n"))
}

func highlightMatch(line, query string) string {
	lower := strings.ToLower(line)
	pos := strings.Index(lower, query)
	if pos < 0 {
		return line
	}
	hl := lipgloss.NewStyle().Background(Accent).Foreground(TextColor)
	return line[:pos] + hl.Render(line[pos:pos+len(query)]) + line[pos+len(query):]
}

func renderDetailHints(o *Overlay, w int) string {
	if o.DetailSearchMode {
		return lipgloss.NewStyle().Foreground(Muted).Width(w).Render(
			"  Ctrl+P/N match · Enter jump · Esc search off · q close",
		)
	}
	return lipgloss.NewStyle().Foreground(Muted).Width(w).Render(
		"  ↑↓/jk scroll · PgUp/PgDn · / search · Esc close",
	)
}

// handleDetailSearchKey handles keyboard input in detail search mode.
func handleDetailSearchKey(o *Overlay, msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyEsc:
		o.DetailSearchMode = false
		o.DetailSearchQ = ""
		o.DetailMatches = nil

	case tea.KeyEnter:
		if o.DetailMatchIdx >= 0 && o.DetailMatchIdx < len(o.DetailMatches) {
			o.DetailScroll = o.DetailMatches[o.DetailMatchIdx].Line
			o.DetailSearchMode = false
		}

	case tea.KeyCtrlP:
		if len(o.DetailMatches) > 0 {
			if o.DetailMatchIdx > 0 {
				o.DetailMatchIdx--
			} else {
				o.DetailMatchIdx = len(o.DetailMatches) - 1
			}
			if o.DetailMatchIdx < o.DetailMatchOff {
				o.DetailMatchOff = o.DetailMatchIdx
			}
		}

	case tea.KeyCtrlN:
		if len(o.DetailMatches) > 0 {
			if o.DetailMatchIdx < len(o.DetailMatches)-1 {
				o.DetailMatchIdx++
			} else {
				o.DetailMatchIdx = 0
			}
			if o.DetailMatchIdx >= o.DetailMatchOff+detailDropdownN {
				o.DetailMatchOff = o.DetailMatchIdx - detailDropdownN + 1
			}
		}

	case tea.KeyBackspace:
		if len(o.DetailSearchQ) > 0 {
			o.DetailSearchQ = o.DetailSearchQ[:len(o.DetailSearchQ)-1]
			o.DetailMatches = detailSearch(o.DetailContent, o.DetailSearchQ)
			o.DetailMatchIdx = 0
			o.DetailMatchOff = 0
		}

	case tea.KeyRunes:
		o.DetailSearchQ += string(msg.Runes)
		o.DetailMatches = detailSearch(o.DetailContent, o.DetailSearchQ)
		o.DetailMatchIdx = 0
		o.DetailMatchOff = 0
	}
}
