package list

import (
	"strings"
)

// renderEntry is the per-item render cache, keyed by (width, version).
type renderEntry struct {
	width   int
	version uint64
	lines   []string // pre-split rendered lines
	height  int
	frozen  bool // true when item.Finished() && cache is permanent
}

// renderCache maps item index to its render entry.
type renderCache struct {
	entries map[int]*renderEntry
}

func newRenderCache() *renderCache {
	return &renderCache{entries: make(map[int]*renderEntry)}
}

// get renders the item at idx (or returns the cached result).
func (c *renderCache) get(idx int, item Item, width int) *renderEntry {
	entry, ok := c.entries[idx]
	if ok && entry.width == width && entry.version == item.Version() {
		return entry
	}

	// Cache miss or stale — re-render.
	content := item.Render(width)
	lines := strings.Split(content, "\n")
	entry = &renderEntry{
		width:   width,
		version: item.Version(),
		lines:   lines,
		height:  len(lines),
		frozen:  item.Finished(),
	}
	c.entries[idx] = entry
	return entry
}

// invalidate removes all cached entries (e.g. after SetItems).
func (c *renderCache) invalidate() {
	c.entries = make(map[int]*renderEntry)
}

// removeShift shifts entries after the removed index down by one.
func (c *renderCache) removeShift(idx int) {
	delete(c.entries, idx)
	// Shift higher entries.
	newEntries := make(map[int]*renderEntry, len(c.entries))
	for i, e := range c.entries {
		if i > idx {
			newEntries[i-1] = e
		} else {
			newEntries[i] = e
		}
	}
	c.entries = newEntries
}

// insertShift shifts entries at and after idx up by one.
func (c *renderCache) insertShift(idx int) {
	newEntries := make(map[int]*renderEntry, len(c.entries))
	for i, e := range c.entries {
		if i >= idx {
			newEntries[i+1] = e
		} else {
			newEntries[i] = e
		}
	}
	c.entries = newEntries
}
