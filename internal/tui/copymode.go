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
// Selected cells get a dark background color with character-level precision.
func (cm *CopyMode) Highlight(content string) string {
	if !cm.active {
		return content
	}

	top, bottom := cm.selectedRange()
	// Normalize X bounds.
	startX, endX := cm.startX, cm.endX
	startY, endY := cm.startY, cm.endY
	if startY > endY || (startY == endY && startX > endX) {
		startX, endX = endX, startX
		startY, endY = endY, startY
	}
	_ = startY
	_ = endY

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

	const selBg = "\x1b[48;5;24m" // dark blue, visible on dark backgrounds

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < top || i > bottom {
			b.WriteString(line)
		} else if top == bottom {
			b.WriteString(injectBgRange(line, selBg, startX, endX))
		} else if i == top {
			b.WriteString(injectBgRange(line, selBg, startX, -1))
		} else if i == bottom {
			b.WriteString(injectBgRange(line, selBg, 0, endX))
		} else {
			b.WriteString(injectBgRange(line, selBg, 0, -1))
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
		if i == top && i == bottom {
			result = append(result, sliceByCol(plain, startX, endX+1))
		} else if i == top {
			result = append(result, sliceByCol(plain, startX, -1))
		} else if i == bottom {
			result = append(result, sliceByCol(plain, 0, endX+1))
		} else {
			result = append(result, plain)
		}
	}
	return strings.Join(result, "\n")
}
