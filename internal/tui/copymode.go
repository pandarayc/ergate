package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
)

// CopyMode handles text selection and clipboard copy within the viewport.
type CopyMode struct {
	active     bool
	startX     int // viewport content X coordinate (column)
	startY     int // viewport content Y coordinate (line)
	endX       int
	endY       int
	viewport   *viewport.Model
	rawContent string
}

// Enter starts copy mode at the given mouse position.
func (cm *CopyMode) Enter(mouseX, mouseY int) {
	cm.active = true
	cm.startX = mouseX
	cm.startY = mouseY + cm.viewport.YOffset
	cm.endX = mouseX
	cm.endY = cm.startY
}

// Track updates the selection end point during drag.
func (cm *CopyMode) Track(mouseX, mouseY int) {
	if !cm.active {
		return
	}
	cm.endX = mouseX
	cm.endY = mouseY + cm.viewport.YOffset
}

// Finish ends copy mode, extracts selected text, and returns it.
func (cm *CopyMode) Finish() string {
	cm.active = false
	text := cm.extractText()
	cm.startX, cm.startY = 0, 0
	cm.endX, cm.endY = 0, 0
	cm.rawContent = ""
	return text
}

// Cancel aborts copy mode without copying.
func (cm *CopyMode) Cancel() {
	cm.active = false
	cm.startX, cm.startY = 0, 0
	cm.endX, cm.endY = 0, 0
	cm.rawContent = ""
}

// IsActive returns true if copy mode is currently active.
func (cm *CopyMode) IsActive() bool { return cm.active }

// selectedRange returns the normalized (top, bottom) lines.
func (cm *CopyMode) selectedRange() (int, int) {
	if cm.startY < cm.endY {
		return cm.startY, cm.endY
	}
	return cm.endY, cm.startY
}

// SetContent stores the current viewport content for text extraction.
func (cm *CopyMode) SetContent(content string) {
	cm.rawContent = content
}

// Highlight wraps the viewport content with selection highlighting.
// Selected lines get a dark background color.
func (cm *CopyMode) Highlight(content string) string {
	if !cm.active || cm.startY == cm.endY {
		return content
	}

	top, bottom := cm.selectedRange()
	lines := strings.Split(content, "\n")
	if top < 0 {
		top = 0
	}
	if bottom >= len(lines) {
		bottom = len(lines) - 1
	}
	if top > bottom {
		return content
	}

	const selBg = "\x1b[48;5;236m" // dark gray background
	const selReset = "\x1b[49m"    // reset background only

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i >= top && i <= bottom {
			b.WriteString(injectBg(line, selBg))
			b.WriteString(selReset)
		} else {
			b.WriteString(line)
		}
	}
	return b.String()
}

// extractText returns the plain text of the selected region with ANSI codes stripped.
func (cm *CopyMode) extractText() string {
	top, bottom := cm.selectedRange()
	if top == bottom {
		return ""
	}

	lines := strings.Split(cm.rawContent, "\n")
	if top < 0 {
		top = 0
	}
	if bottom >= len(lines) {
		bottom = len(lines) - 1
	}
	if top > bottom {
		return ""
	}

	// Determine X bounds (normalize for start > end direction).
	startX, endX := cm.startX, cm.endX
	startY, endY := cm.startY, cm.endY
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}

	var result []string
	for i := top; i <= bottom; i++ {
		plain := stripAnsi(lines[i])
		runes := []rune(plain)
		if i == top && i == bottom {
			// Single line selection: clip to [startX, endX]
			lo := min(startX, len(runes))
			hi := min(endX+1, len(runes))
			if lo < hi {
				result = append(result, string(runes[lo:hi]))
			}
		} else if i == top {
			// First line: from startX to end
			lo := min(startX, len(runes))
			result = append(result, string(runes[lo:]))
		} else if i == bottom {
			// Last line: from start to endX
			hi := min(endX+1, len(runes))
			result = append(result, string(runes[:hi]))
		} else {
			// Middle lines: full line
			result = append(result, plain)
		}
	}
	return strings.Join(result, "\n")
}
