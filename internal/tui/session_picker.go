package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	pickerMaxWidth  = 70
	pickerMinWidth  = 30
	pickerMaxHeight = 20
	pickerMinHeight = 6
)

// renderSessionPicker renders the session selection popup.
func renderSessionPicker(o *Overlay, termW, termH int) string {
	w := termW * 80 / 100
	if w > pickerMaxWidth {
		w = pickerMaxWidth
	}
	if w < pickerMinWidth {
		w = pickerMinWidth
	}

	h := termH * 70 / 100
	if h > pickerMaxHeight {
		h = pickerMaxHeight
	}
	if h < pickerMinHeight {
		h = pickerMinHeight
	}

	var b strings.Builder

	// Title
	titleStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true)
	b.WriteString(titleStyle.Render("Sessions"))
	b.WriteString("\n\n")

	// Session list
	items := o.SessionPickerItems
	cursor := o.SessionPickerCursor
	scroll := o.SessionPickerScroll

	// Clamp scroll: ensure cursor is visible
	visibleRows := h - 5 // title + hints space
	if visibleRows < 1 {
		visibleRows = 1
	}
	if cursor < scroll {
		scroll = cursor
	}
	if cursor >= scroll+visibleRows {
		scroll = cursor - visibleRows + 1
	}
	if scroll < 0 {
		scroll = 0
	}
	o.SessionPickerScroll = scroll

	cursorStyle := lipgloss.NewStyle().Foreground(TextColor).Background(Accent).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(TextColor)
	mutedStyle := lipgloss.NewStyle().Foreground(Muted)
	dimStyle := lipgloss.NewStyle().Foreground(BorderDim)

	end := scroll + visibleRows
	if end > len(items) {
		end = len(items)
	}

	for i := scroll; i < end; i++ {
		item := items[i]
		line := fmt.Sprintf(" %s  %s  %s",
			item.UpdatedAt.Format("01-02 15:04"),
			dimStyle.Render(fmt.Sprintf("%d msgs", item.MessageCount)),
			mutedStyle.Render(item.Model),
		)

		if i == cursor {
			b.WriteString(cursorStyle.Render(line))
		} else {
			b.WriteString(normalStyle.Render(line))
		}
		b.WriteString("\n")
	}

	// Empty state
	if len(items) == 0 {
		b.WriteString(mutedStyle.Render("  No sessions found"))
		b.WriteString("\n")
	}

	// Bottom hints
	b.WriteString("\n")
	hintStyle := lipgloss.NewStyle().Foreground(BorderDim)
	b.WriteString(hintStyle.Render("  ↑↓ navigate  Enter select  Esc new session"))

	inner := lipgloss.NewStyle().Padding(0, 1).Render(b.String())

	return lipgloss.NewStyle().
		Width(w).
		Height(h).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Accent).
		Padding(1, 1).
		Render(inner)
}
