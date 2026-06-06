package list

import (
	"strings"
)

// List is a virtual-scrolling list of Items.
// It handles rendering (only visible items), scrolling, and hit testing.
//
// Coordinate space: all coordinates are in terminal space.
// ItemAtPosition(x, y) takes screen coordinates directly — the caller does NOT
// need to apply viewport YOffset transformations.
type List struct {
	items      []Item
	width      int
	height     int // visible height in terminal rows

	// Scroll state: offsetIdx is the first (partially) visible item.
	// offsetLine is how many lines of that item are scrolled off the top.
	offsetIdx  int
	offsetLine int

	// Gap is the number of blank lines between items (default 0).
	Gap int

	// Selection state for text selection via mouse drag.
	Sel SelectionState

	cache *renderCache
}

// New creates a new List.
func New(width, height int) *List {
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}
	return &List{
		width:  width,
		height: height,
		cache:  newRenderCache(),
	}
}

// Width returns the list's current width.
func (l *List) Width() int { return l.width }

// Height returns the list's visible height in rows.
func (l *List) Height() int { return l.height }

// SetSize updates the list dimensions. Invalidates the render cache.
func (l *List) SetSize(width, height int) {
	if width != l.width {
		l.cache.invalidate()
	}
	l.width = width
	l.height = height
}

// SetItems replaces all items. Resets scroll and cache.
func (l *List) SetItems(items []Item) {
	l.items = items
	l.offsetIdx = 0
	l.offsetLine = 0
	l.cache.invalidate()
}

// AppendItems adds items to the end.
func (l *List) AppendItems(items ...Item) {
	l.items = append(l.items, items...)
}

// InsertItem inserts an item at the given index.
func (l *List) InsertItem(idx int, item Item) {
	if idx < 0 || idx > len(l.items) {
		return
	}
	l.items = append(l.items, nil)
	copy(l.items[idx+1:], l.items[idx:])
	l.items[idx] = item
	l.cache.insertShift(idx)
}

// RemoveItem removes the item at the given index.
func (l *List) RemoveItem(idx int) {
	if idx < 0 || idx >= len(l.items) {
		return
	}
	l.items = append(l.items[:idx], l.items[idx+1:]...)
	l.cache.removeShift(idx)
}

// ItemCount returns the number of items in the list.
func (l *List) ItemCount() int { return len(l.items) }

// ItemAt returns the item at the given index.
func (l *List) ItemAt(idx int) Item {
	if idx < 0 || idx >= len(l.items) {
		return nil
	}
	return l.items[idx]
}

// UpdateItem updates the item at idx. panics if idx is out of bounds.
func (l *List) UpdateItem(idx int) {
	// Force cache invalidation for this item on next render.
	delete(l.cache.entries, idx)
}

// Render produces the visible portion of the list as an ANSI-styled string.
// Only items that intersect the visible area are rendered.
func (l *List) Render() string {
	if len(l.items) == 0 || l.height <= 0 {
		return ""
	}

	var b strings.Builder
	remaining := l.height
	currentLine := -l.offsetLine

	for i := l.offsetIdx; i < len(l.items) && remaining > 0; i++ {
		entry := l.cache.get(i, l.items[i], l.width)

		// Skip lines scrolled off the top of this item.
		start := max(0, -currentLine)

		for j := start; j < entry.height && remaining > 0; j++ {
			b.WriteString(entry.lines[j])
			remaining--
			// Add newline unless this is the absolute last visible cell.
			if remaining > 0 {
				b.WriteString("\n")
			}
		}

		// Gap between items.
		if l.Gap > 0 && remaining > 0 {
			for g := 0; g < l.Gap && remaining > 0; g++ {
				b.WriteString("\n")
				remaining--
			}
		}

		currentLine += entry.height + l.Gap
	}

	return b.String()
}

// itemEntry is a convenience wrapper for getEntry without cache side-effects.
func (l *List) itemEntry(idx int) *renderEntry {
	if idx < 0 || idx >= len(l.items) {
		return nil
	}
	return l.cache.get(idx, l.items[idx], l.width)
}

// ItemAtPosition returns the item and the line offset within that item
// for the given terminal coordinates. x is the column, y is the row.
// Returns (nil, 0) if no item is at that position.
func (l *List) ItemAtPosition(x, y int) (Item, int) {
	idx, lineOff := l.findItemAtY(x, y)
	if idx < 0 {
		return nil, 0
	}
	return l.items[idx], lineOff
}

// ItemIndexAtPosition returns the index and line offset for the given terminal
// coordinates. Returns (-1, 0) if no item is at that position.
func (l *List) ItemIndexAtPosition(x, y int) (int, int) {
	return l.findItemAtY(x, y)
}

// findItemAtY finds the item covering terminal row y.
// x is accepted but currently unused (reserved for future column-level hit testing).
func (l *List) findItemAtY(_, y int) (int, int) {
	if y < 0 || y >= l.height {
		return -1, 0
	}

	currentLine := -l.offsetLine
	for i := l.offsetIdx; i < len(l.items); i++ {
		entry := l.itemEntry(i)
		if entry == nil {
			continue
		}
		itemEndLine := currentLine + entry.height
		if y >= currentLine && y < itemEndLine {
			return i, y - currentLine
		}
		currentLine = itemEndLine + l.Gap
	}
	return -1, 0
}

// ScrollBy adjusts the scroll position by delta lines.
// Positive = scroll down, negative = scroll up.
func (l *List) ScrollBy(delta int) {
	if delta > 0 {
		l.scrollDown(delta)
	} else if delta < 0 {
		l.scrollUp(-delta)
	}
}

func (l *List) scrollDown(lines int) {
	for lines > 0 && l.offsetIdx < len(l.items) {
		entry := l.itemEntry(l.offsetIdx)
		if entry == nil {
			l.offsetIdx++
			l.offsetLine = 0
			continue
		}
		remaining := entry.height - l.offsetLine
		if lines < remaining {
			l.offsetLine += lines
			return
		}
		lines -= remaining
		l.offsetIdx++
		l.offsetLine = 0
	}
}

func (l *List) scrollUp(lines int) {
	for lines > 0 && (l.offsetIdx > 0 || l.offsetLine > 0) {
		if l.offsetLine > 0 {
			if lines <= l.offsetLine {
				l.offsetLine -= lines
				return
			}
			lines -= l.offsetLine
			l.offsetLine = 0
			continue
		}
		// Move to previous item.
		l.offsetIdx--
		entry := l.itemEntry(l.offsetIdx)
		if entry == nil {
			continue
		}
		l.offsetLine = entry.height
	}
}

// ScrollToBottom scrolls to the last visible line.
func (l *List) ScrollToBottom() {
	if len(l.items) == 0 {
		return
	}
	// Walk backwards from end to find the first item to show at the bottom.
	totalHeight := l.totalHeight()
	if totalHeight <= l.height {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}

	// Position so the last item is at the bottom.
	l.offsetIdx = len(l.items) - 1
	l.offsetLine = 0

	// Walk up until we fill the viewport.
	accum := 0
	for l.offsetIdx > 0 {
		entry := l.itemEntry(l.offsetIdx - 1)
		if entry == nil {
			l.offsetIdx--
			continue
		}
		if accum+entry.height+l.Gap > l.height {
			l.offsetLine = accum + entry.height + l.Gap - l.height
			return
		}
		accum += entry.height + l.Gap
		l.offsetIdx--
	}
}

// ScrollToIndex scrolls to make the item at idx visible at the top.
func (l *List) ScrollToIndex(idx int) {
	if idx < 0 || idx >= len(l.items) {
		return
	}
	l.offsetIdx = idx
	l.offsetLine = 0
}

// AtBottom returns true if the viewport shows the end of the list.
func (l *List) AtBottom() bool {
	if len(l.items) == 0 {
		return true
	}
	return l.totalHeight()-l.VisibleOffset() <= l.height
}

// totalHeight computes the total height of all items plus gaps.
func (l *List) totalHeight() int {
	h := 0
	for i := range l.items {
		entry := l.itemEntry(i)
		if entry != nil {
			h += entry.height
		}
	}
	if len(l.items) > 1 {
		h += l.Gap * (len(l.items) - 1)
	}
	return h
}

// VisibleOffset returns how many lines are scrolled past the top.
func (l *List) VisibleOffset() int {
	off := 0
	for i := 0; i < l.offsetIdx && i < len(l.items); i++ {
		entry := l.itemEntry(i)
		if entry != nil {
			off += entry.height + l.Gap
		}
	}
	off += l.offsetLine
	return off
}

// VisibleItemIndices returns the range [startIdx, endIdx) of items
// that are currently (partially) visible.
func (l *List) VisibleItemIndices() (startIdx, endIdx int) {
	if len(l.items) == 0 {
		return 0, 0
	}
	startIdx = l.offsetIdx
	currentLine := -l.offsetLine
	endIdx = l.offsetIdx
	for i := l.offsetIdx; i < len(l.items); i++ {
		entry := l.itemEntry(i)
		if entry == nil {
			continue
		}
		if currentLine+entry.height <= 0 {
			currentLine += entry.height + l.Gap
			startIdx = i + 1
			continue
		}
		if currentLine >= l.height {
			break
		}
		endIdx = i + 1
		currentLine += entry.height + l.Gap
	}
	return startIdx, endIdx
}

// EnsureVisible adjusts scroll so the item at idx is at least partially visible.
func (l *List) EnsureVisible(idx int) {
	if idx < 0 || idx >= len(l.items) {
		return
	}
	// First, compute the item's terminal Y range.
	itemStart := -l.offsetLine
	for i := l.offsetIdx; i < idx && i < len(l.items); i++ {
		entry := l.itemEntry(i)
		if entry != nil {
			itemStart += entry.height + l.Gap
		}
	}
	entry := l.itemEntry(idx)
	if entry == nil {
		return
	}
	itemEnd := itemStart + entry.height

	// If item is above the visible area, scroll up.
	if itemStart < 0 {
		l.offsetIdx = idx
		l.offsetLine = 0
		return
	}
	// If item is below the visible area, scroll down.
	if itemEnd > l.height {
		// Scroll so the item is at the bottom.
		l.offsetIdx = idx
		l.offsetLine = 0
		if entry.height > l.height {
			return // item is taller than viewport, show top
		}
		// Walk up to fill the space above.
		remaining := l.height - entry.height
		for l.offsetIdx > 0 && remaining > 0 {
			prev := l.itemEntry(l.offsetIdx - 1)
			if prev == nil {
				l.offsetIdx--
				continue
			}
			if prev.height+l.Gap > remaining {
				l.offsetLine = prev.height + l.Gap - remaining
				return
			}
			remaining -= prev.height + l.Gap
			l.offsetIdx--
		}
		l.offsetLine = 0
	}
}

// OffsetIndex returns the current scroll position index.
func (l *List) OffsetIndex() int { return l.offsetIdx }

// OffsetLine returns the current scroll position line offset.
func (l *List) OffsetLine() int { return l.offsetLine }
