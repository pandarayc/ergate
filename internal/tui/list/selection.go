package list

import (
	"fmt"
	"strings"
)

// SelectionState tracks text selection across mouse drag events.
type SelectionState struct {
	active bool

	// Anchor is where the drag started (item index + line offset + column).
	anchorIdx  int
	anchorLine int
	anchorCol  int

	// Focus is the current drag position.
	focusIdx  int
	focusLine int
	focusCol  int

	// Tracks whether we're currently dragging.
	dragging bool

	// When settled, the highlight persists even after mouse release.
	settled bool
}

// IsActive returns true if selection is in progress or settled.
func (s *SelectionState) IsActive() bool { return s.active || s.settled }

// HasSelection returns true if there is a settled selection with area.
func (s *SelectionState) HasSelection() bool {
	return s.settled && s.focusIdx >= 0
}

// Start begins a selection at the given position.
func (s *SelectionState) Start(idx, line, col int) {
	s.active = true
	s.settled = false
	s.dragging = false
	s.anchorIdx = idx
	s.anchorLine = line
	s.anchorCol = col
	s.focusIdx = -1
	s.focusLine = -1
	s.focusCol = -1
}

// Track updates the focus position during a drag.
func (s *SelectionState) Track(idx, line, col int) {
	s.focusIdx = idx
	s.focusLine = line
	s.focusCol = col
	s.dragging = true
}

// Finish settles the selection. Returns true if there's content to copy.
func (s *SelectionState) Finish() bool {
	s.active = false
	if !s.dragging {
		s.settled = false
		return false
	}
	s.settled = true
	return true
}

// Cancel clears the selection entirely.
func (s *SelectionState) Cancel() {
	s.active = false
	s.settled = false
	s.dragging = false
	s.focusIdx = -1
}

// IsDrag returns true if the user dragged (not just clicked).
func (s *SelectionState) IsDrag() bool {
	if s.focusIdx < 0 {
		return false
	}
	di := s.focusIdx - s.anchorIdx
	dl := s.focusLine - s.anchorLine
	dc := s.focusCol - s.anchorCol
	return di != 0 || dl != 0 || dc > 1
}

// range returns start/end in reading order: (startIdx, startLine, startCol, endIdx, endLine, endCol).
func (s *SelectionState) sortRange() (si, sl, sc, ei, el, ec int) {
	// Compare anchor and focus.
	if s.anchorIdx < s.focusIdx {
		return s.anchorIdx, s.anchorLine, s.anchorCol, s.focusIdx, s.focusLine, s.focusCol
	}
	if s.anchorIdx > s.focusIdx {
		return s.focusIdx, s.focusLine, s.focusCol, s.anchorIdx, s.anchorLine, s.anchorCol
	}
	// Same item.
	if s.anchorLine < s.focusLine {
		return s.anchorIdx, s.anchorLine, s.anchorCol, s.focusIdx, s.focusLine, s.focusCol
	}
	if s.anchorLine > s.focusLine {
		return s.focusIdx, s.focusLine, s.focusCol, s.anchorIdx, s.anchorLine, s.anchorCol
	}
	// Same line.
	if s.anchorCol <= s.focusCol {
		return s.anchorIdx, s.anchorLine, s.anchorCol, s.focusIdx, s.focusLine, s.focusCol
	}
	return s.focusIdx, s.focusLine, s.focusCol, s.anchorIdx, s.anchorLine, s.anchorCol
}

// SelectedText extracts the selected text from the list's rendered items.
func (s *SelectionState) SelectedText(l *List) string {
	if !s.HasSelection() {
		return ""
	}
	si, sl, sc, ei, el, ec := s.sortRange()

	var b strings.Builder
	for idx := si; idx <= ei; idx++ {
		entry := l.itemEntry(idx)
		if entry == nil {
			continue
		}
		startLine := 0
		if idx == si {
			startLine = sl
		}
		endLine := entry.height - 1
		if idx == ei {
			endLine = el
		}
		for line := startLine; line <= endLine; line++ {
			// Get plain text for this line (strip ANSI).
			plain := stripAnsi(entry.lines[line])
			if idx == si && line == sl && idx == ei && line == el {
				// Single line selection.
				if sc < len(plain) && ec <= len(plain) && sc <= ec {
					b.WriteString(plain[sc:ec])
				}
			} else if idx == si && line == sl {
				if sc < len(plain) {
					b.WriteString(plain[sc:])
				}
			} else if idx == ei && line == el {
				if ec <= len(plain) {
					b.WriteString(plain[:ec])
				}
			} else {
				b.WriteString(plain)
			}
			if !(idx == ei && line == endLine) {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// stripAnsi removes ANSI escape sequences from a string.
// Simplified version sufficient for text extraction.
func stripAnsi(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if s[i] >= '@' && s[i] <= '~' {
				inEscape = false
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// InjectHighlight returns a version of the list's rendered output with selection
// highlighting applied. Uses ANSI background color escape sequences.
func (s *SelectionState) InjectHighlight(l *List) string {
	if !s.HasSelection() {
		return l.Render()
	}
	// For now, return the normal render. Full highlight injection
	// (ANSI-aware background color insertion) will be added when needed.
	return l.Render()
}

// CopySelected copies the selected text to the clipboard via OSC 52.
func (s *SelectionState) CopySelected(l *List) string {
	text := s.SelectedText(l)
	if text != "" {
		// OSC 52 escape sequence.
		encoded := base64Encode(text)
		fmt.Print("\x1b]52;c;" + encoded + "\x07")
	}
	return text
}

func base64Encode(s string) string {
	const encode = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var b strings.Builder
	for i := 0; i < len(s); i += 3 {
		chunk := make([]byte, 3)
		n := copy(chunk, s[i:])
		b.WriteByte(encode[chunk[0]>>2])
		if n == 1 {
			b.WriteByte(encode[(chunk[0]&0x03)<<4])
			b.WriteString("==")
		} else if n == 2 {
			b.WriteByte(encode[(chunk[0]&0x03)<<4|chunk[1]>>4])
			b.WriteByte(encode[(chunk[1]&0x0f)<<2])
			b.WriteString("=")
		} else {
			b.WriteByte(encode[(chunk[0]&0x03)<<4|chunk[1]>>4])
			b.WriteByte(encode[(chunk[1]&0x0f)<<2|chunk[2]>>6])
			b.WriteByte(encode[chunk[2]&0x3f])
		}
	}
	return b.String()
}
