package list

import (
	"strings"
	"testing"
)

// testItem is a mock Item for testing.
type testItem struct {
	Versioned
	content  string
	finished bool
}

func (ti *testItem) Render(width int) string { return ti.content }
func (ti *testItem) Finished() bool          { return ti.finished }

// testMouseItem implements MouseHandler.
type testMouseItem struct {
	testItem
	clicked    bool
	lastX, lastY int
}

func (tmi *testMouseItem) HandleMouseClick(btn MouseButton, x, y int) bool {
	tmi.clicked = true
	tmi.lastX = x
	tmi.lastY = y
	return true
}

func makeItems(n int, linesPerItem int) []Item {
	items := make([]Item, n)
	for i := range n {
		content := ""
		for j := range linesPerItem {
			if j > 0 {
				content += "\n"
			}
			content += "line " + string(rune('a'+j))
		}
		items[i] = &testItem{content: content, finished: true}
	}
	return items
}

func TestListRender(t *testing.T) {
	items := makeItems(5, 2) // 5 items, 2 lines each
	l := New(80, 6)
	l.SetItems(items)

	out := l.Render()
	lines := countLines(out)
	// 5 items × 2 lines = 10, but viewport height is 6, so 3 items (6 lines) visible
	if lines != 6 {
		t.Errorf("expected 6 rendered lines, got %d\noutput:\n%q", lines, out)
	}
}

func TestListRenderEmpty(t *testing.T) {
	l := New(80, 10)
	out := l.Render()
	if out != "" {
		t.Errorf("expected empty render, got: %q", out)
	}
}

func TestListScrollDown(t *testing.T) {
	items := makeItems(10, 2) // 10 items, 2 lines each = 20 lines total
	l := New(80, 4)
	l.SetItems(items)

	l.ScrollBy(5)
	if l.offsetIdx != 2 || l.offsetLine != 1 {
		t.Errorf("after scroll 5: expected offsetIdx=2 offsetLine=1, got idx=%d line=%d",
			l.offsetIdx, l.offsetLine)
	}

	out := l.Render()
	lines := countLines(out)
	if lines > 4 {
		t.Errorf("rendered lines should not exceed viewport height 4, got %d", lines)
	}
}

func TestListScrollUp(t *testing.T) {
	items := makeItems(10, 2)
	l := New(80, 4)
	l.SetItems(items)

	l.ScrollBy(6) // scroll down 6 lines (3 items)
	l.ScrollBy(-3) // scroll up 3 lines
	if l.offsetIdx != 1 || l.offsetLine != 1 {
		t.Errorf("after scroll +6 -3: expected offsetIdx=1 offsetLine=1, got idx=%d line=%d",
			l.offsetIdx, l.offsetLine)
	}
}

func TestListAtBottom(t *testing.T) {
	items := makeItems(5, 1) // 5 lines total
	l := New(80, 10)
	l.SetItems(items)

	if !l.AtBottom() {
		t.Error("should be at bottom when content shorter than viewport")
	}

	l.ScrollBy(1) // can't scroll past
	if !l.AtBottom() {
		t.Error("should still be at bottom")
	}
}

func TestListAtBottomAfterScroll(t *testing.T) {
	items := makeItems(10, 2) // 20 lines
	l := New(80, 4)
	l.SetItems(items)

	l.ScrollToBottom()
	if !l.AtBottom() {
		t.Error("should be at bottom after ScrollToBottom")
	}
}

func TestListItemAtPosition(t *testing.T) {
	items := makeItems(5, 2) // each item 2 lines
	l := New(80, 10)
	l.SetItems(items)

	// row 0 -> first item, line 0
	item, lineOff := l.ItemAtPosition(0, 0)
	if item == nil {
		t.Fatal("expected item at (0,0)")
	}
	if lineOff != 0 {
		t.Errorf("expected lineOff=0, got %d", lineOff)
	}

	// row 1 -> first item, line 1
	item, lineOff = l.ItemAtPosition(0, 1)
	if item == nil {
		t.Fatal("expected item at (0,1)")
	}
	if lineOff != 1 {
		t.Errorf("expected lineOff=1, got %d", lineOff)
	}

	// row 2 -> second item, line 0
	item, lineOff = l.ItemAtPosition(0, 2)
	if item == nil {
		t.Fatal("expected item at (0,2)")
	}
	if lineOff != 0 {
		t.Errorf("expected lineOff=0 for second item, got %d", lineOff)
	}
}

func TestListItemAtPositionOutOfBounds(t *testing.T) {
	items := makeItems(5, 2)
	l := New(80, 6)
	l.SetItems(items)

	item, _ := l.ItemAtPosition(0, -1)
	if item != nil {
		t.Error("expected nil for negative y")
	}

	item, _ = l.ItemAtPosition(0, 10)
	if item != nil {
		t.Error("expected nil for y >= height")
	}
}

func TestListItemAtPositionAfterScroll(t *testing.T) {
	items := makeItems(5, 2) // items 0..4, 2 lines each
	l := New(80, 4)
	l.SetItems(items)

	l.ScrollBy(3) // scrolled past item 0 (2 lines) + 1 line of item 1

	// row 0 should now be item 1, line 1
	item, lineOff := l.ItemAtPosition(0, 0)
	if item == nil {
		t.Fatal("expected item at (0,0) after scroll")
	}
	if lineOff != 1 {
		t.Errorf("expected lineOff=1 (line 1 of item 1), got %d", lineOff)
	}
}

func TestListMouseHandler(t *testing.T) {
	tmi := &testMouseItem{testItem: testItem{content: "hello\nworld", finished: true}}
	l := New(80, 10)
	l.SetItems([]Item{tmi})

	item, lineOff := l.ItemAtPosition(0, 1)
	if item == nil {
		t.Fatal("expected item")
	}

	handler, ok := item.(MouseHandler)
	if !ok {
		t.Fatal("testMouseItem should implement MouseHandler")
	}
	handler.HandleMouseClick(MouseButtonLeft, 3, lineOff)

	if !tmi.clicked {
		t.Error("mouse click should have been handled")
	}
	if tmi.lastX != 3 || tmi.lastY != 1 {
		t.Errorf("expected click at (3,1), got (%d,%d)", tmi.lastX, tmi.lastY)
	}
}

func TestListCacheInvalidation(t *testing.T) {
	ti := &testItem{content: "initial"}
	l := New(80, 10)
	l.SetItems([]Item{ti})

	first := l.Render()
	if trimmed := strings.TrimRight(first, "\n"); trimmed != "initial" {
		t.Errorf("expected 'initial', got %q", trimmed)
	}

	// Change content and bump version.
	ti.content = "changed"
	ti.Bump()

	second := l.Render()
	if trimmed := strings.TrimRight(second, "\n"); trimmed != "changed" {
		t.Errorf("expected 'changed' after bump, got %q", trimmed)
	}
}

func TestListSetItemsResetsScroll(t *testing.T) {
	items := makeItems(10, 2)
	l := New(80, 4)
	l.SetItems(items)
	l.ScrollBy(8)

	l.SetItems(makeItems(3, 1))
	if l.offsetIdx != 0 || l.offsetLine != 0 {
		t.Errorf("SetItems should reset scroll, got idx=%d line=%d", l.offsetIdx, l.offsetLine)
	}
}

func TestListScrollToIndex(t *testing.T) {
	items := makeItems(10, 2)
	l := New(80, 4)
	l.SetItems(items)

	l.ScrollToIndex(5)
	if l.offsetIdx != 5 || l.offsetLine != 0 {
		t.Errorf("ScrollToIndex(5): expected idx=5 line=0, got idx=%d line=%d",
			l.offsetIdx, l.offsetLine)
	}
}

func TestListVisibleItemIndices(t *testing.T) {
	items := makeItems(5, 2) // 10 lines total
	l := New(80, 6)
	l.SetItems(items)

	start, end := l.VisibleItemIndices()
	if start != 0 || end < 3 {
		t.Errorf("expected start=0 end>=3, got start=%d end=%d", start, end)
	}

	l.ScrollToBottom()
	start, end = l.VisibleItemIndices()
	if end > len(items) {
		t.Errorf("end=%d exceeds item count %d", end, len(items))
	}
}

func TestListAppendInsertRemove(t *testing.T) {
	l := New(80, 10)
	l.SetItems(makeItems(3, 1))

	if l.ItemCount() != 3 {
		t.Errorf("expected 3 items, got %d", l.ItemCount())
	}

	l.AppendItems(&testItem{content: "appended"})
	if l.ItemCount() != 4 {
		t.Errorf("expected 4 items after append, got %d", l.ItemCount())
	}

	l.InsertItem(1, &testItem{content: "inserted"})
	if l.ItemCount() != 5 {
		t.Errorf("expected 5 items after insert, got %d", l.ItemCount())
	}

	l.RemoveItem(2)
	if l.ItemCount() != 4 {
		t.Errorf("expected 4 items after remove, got %d", l.ItemCount())
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := 1
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	return n
}
