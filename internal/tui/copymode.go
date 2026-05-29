package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
)

// CopyMode handles text selection and clipboard copy within the viewport.
type CopyMode struct {
	active     bool
	startY     int // viewport content Y coordinate
	endY       int
	viewport   *viewport.Model
	rawContent string
}

// Enter starts copy mode at the given mouse position.
// mouseY is the terminal row (0-indexed), maps directly to viewport content line
// when accounting for scroll offset. The viewport content includes the header.
func (cm *CopyMode) Enter(mouseY int) {
	cm.active = true
	cm.startY = mouseY + cm.viewport.YOffset
	cm.endY = cm.startY
}

// Track updates the selection end point during drag.
func (cm *CopyMode) Track(mouseY int) {
	if !cm.active {
		return
	}
	cm.endY = mouseY + cm.viewport.YOffset
}

// Finish ends copy mode, extracts selected text, and returns it.
func (cm *CopyMode) Finish() string {
	cm.active = false
	text := cm.extractText()
	cm.startY = 0
	cm.endY = 0
	cm.rawContent = ""
	return text
}

// Cancel aborts copy mode without copying.
func (cm *CopyMode) Cancel() {
	cm.active = false
	cm.startY = 0
	cm.endY = 0
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
			// Inject background after every SGR reset so lipgloss can't clear it.
			highlighted := selBg + strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+selBg) + selReset
			b.WriteString(highlighted)
		} else {
			b.WriteString(line)
		}
	}
	return b.String()
}

// extractText returns the plain text of the selected region.
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

	return strings.Join(lines[top:bottom+1], "\n")
}
