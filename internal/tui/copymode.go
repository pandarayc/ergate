package tui

import "strings"

// CopyMode handles text selection and clipboard copy within the viewport.
// Uses an anchor/focus model (like DOM Selection) normalized to start/end for display.
//
// States:
//  0. No selection   (active=false, settled=false)
//  1. Anchor only    (active=true, focusX=-1) — press without drag yet
//  2. Active drag    (active=true, focusX>=0) — dragging, highlight visible
//  3. Settled        (active=false, settled=true) — drag finished, highlight stays
//  4. Cleared        → Cancel() or next press
//
// Coordinates are in the pre-wrapped visual content space: each wrappedLines entry
// is one viewport row, so YOffset maps 1:1 to wrappedLines indices.
type CopyMode struct {
	active  bool
	settled bool
	anchorX int // press position (content columns, 0-indexed)
	anchorY int // press position (content rows, 0-indexed)
	focusX  int // drag position; -1 means not yet dragged
	focusY  int

	wrappedLines []string // pre-wrapped content: 1 entry = 1 visual row
	contentLines int      // line count at last SetContent; used to detect streaming changes
}

// Enter starts copy mode at the given mouse position.
// yOffset is the viewport's current vertical scroll offset.
func (cm *CopyMode) Enter(mouseX, mouseY, yOffset int) {
	cm.active = true
	cm.settled = false
	cm.anchorX = mouseX
	cm.anchorY = mouseY + yOffset
	cm.focusX = -1
	cm.focusY = -1
}

// Track updates the selection end point during drag.
func (cm *CopyMode) Track(mouseX, mouseY, yOffset int) {
	if !cm.active {
		return
	}
	cm.focusX = mouseX
	cm.focusY = mouseY + yOffset
}

// Finish ends the drag, copies text, and transitions to settled state.
// Returns the selected text for clipboard copy. Returns "" for click-without-drag.
func (cm *CopyMode) Finish() string {
	cm.active = false
	if cm.focusX < 0 {
		// Click without drag — no selection.
		cm.anchorX, cm.anchorY = 0, 0
		cm.wrappedLines = nil
		return ""
	}
	// Keep highlight visible (settled state) like iTerm2/claude.
	cm.settled = true
	return cm.extractText()
}

// Cancel aborts copy mode and clears any settled highlight.
func (cm *CopyMode) Cancel() {
	cm.active = false
	cm.settled = false
	cm.anchorX, cm.anchorY = 0, 0
	cm.focusX, cm.focusY = -1, -1
	cm.wrappedLines = nil
	cm.contentLines = 0
}

// IsActive returns true if copy mode is active (dragging or settled).
func (cm *CopyMode) IsActive() bool { return cm.active || cm.settled }

// HasSelection returns true when there is a non-degenerate selection to display.
func (cm *CopyMode) HasSelection() bool {
	return (cm.active || cm.settled) && cm.focusX >= 0
}

// selectedRange returns normalized selection bounds in reading order.
// start ≤ end (top-to-bottom, then left-to-right within same row).
func (cm *CopyMode) selectedRange() (sx, sy, ex, ey int) {
	ax, ay := cm.anchorX, cm.anchorY
	fx, fy := cm.focusX, cm.focusY
	if ay < fy || (ay == fy && ax <= fx) {
		return ax, ay, fx, fy
	}
	return fx, fy, ax, ay
}

// SetContent stores the pre-wrapped content lines for text extraction.
// If content changed while a settled selection is active (e.g. streaming tokens),
// the selection is cancelled to prevent stale coordinates highlighting wrong text.
func (cm *CopyMode) SetContent(content string) {
	cm.wrappedLines = strings.Split(content, "\n")
	if cm.settled && len(cm.wrappedLines) != cm.contentLines {
		cm.Cancel()
		return
	}
	cm.contentLines = len(cm.wrappedLines)
}

// Highlight applies selection background to the pre-wrapped content.
func (cm *CopyMode) Highlight(content string) string {
	if !cm.HasSelection() {
		return content
	}

	sx, sy, ex, ey := cm.selectedRange()
	lines := strings.Split(content, "\n")

	if sy < 0 {
		sy = 0
	}
	if ey >= len(lines) {
		ey = len(lines) - 1
	}
	if sy > ey {
		return content
	}

	const selBg = "\x1b[48;5;24m" // dark blue

	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < sy || i > ey {
			b.WriteString(line)
		} else if sy == ey {
			b.WriteString(injectBgRange(line, selBg, sx, ex))
		} else if i == sy {
			b.WriteString(injectBgRange(line, selBg, sx, -1))
		} else if i == ey {
			b.WriteString(injectBgRange(line, selBg, 0, ex))
		} else {
			b.WriteString(injectBgRange(line, selBg, 0, -1))
		}
	}
	return b.String()
}

// extractText extracts plain text from the selected region.
func (cm *CopyMode) extractText() string {
	sx, sy, ex, ey := cm.selectedRange()

	if sy >= len(cm.wrappedLines) {
		return ""
	}
	if ey >= len(cm.wrappedLines) {
		ey = len(cm.wrappedLines) - 1
	}
	if sy > ey {
		return ""
	}

	var result []string
	for i := sy; i <= ey; i++ {
		plain := stripAnsi(cm.wrappedLines[i])
		if sy == ey {
			result = append(result, sliceByCol(plain, sx, ex+1))
		} else if i == sy {
			result = append(result, sliceByCol(plain, sx, -1))
		} else if i == ey {
			result = append(result, sliceByCol(plain, 0, ex+1))
		} else {
			result = append(result, plain)
		}
	}
	return strings.Join(result, "\n")
}
