package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestYTracking_MultipleThinking verifies that handleViewportClick can
// correctly identify each thinking block when scrolled into view.
func TestYTracking_MultipleThinking(t *testing.T) {
	longThinking := strings.Repeat("line of text that's long enough to overflow\n", 10)

	m := ChatModel{}
	m.viewport.Width = 80
	m.viewport.Height = 20

	m.messages = []ChatMessage{
		{Role: "user", Content: "first quest"},
		{Role: "thinking", Content: longThinking},
		{Role: "tool", Content: "read main.go"},
		{Role: "assistant", Content: "first answer."},
		{Role: "user", Content: "second quest"},
		{Role: "thinking", Content: longThinking},
		{Role: "tool", Content: "read main.go"},
		{Role: "assistant", Content: "second answer."},
		{Role: "user", Content: "third quest"},
		{Role: "thinking", Content: longThinking},
		{Role: "tool", Content: "read main.go"},
		{Role: "assistant", Content: "third answer."},
	}

	// Render once to populate wasFolded, rendered cache, and layout.
	_ = m.renderContent()

	t.Logf("layout.Content widgets:")
	for _, w := range m.layout.Content {
		if w.Index >= 0 && w.Index < len(m.messages) {
			t.Logf("  msg[%2d] role=%-10s y=%2d h=%d", w.Index, m.messages[w.Index].Role, w.Y, w.Height)
		}
	}

	// Find the widget for a message index.
	findWidget := func(i int) *Widget {
		for idx := range m.layout.Content {
			if m.layout.Content[idx].Index == i {
				return &m.layout.Content[idx]
			}
		}
		return nil
	}

	for i := range m.messages {
		if m.messages[i].Role != "thinking" {
			continue
		}

		w := findWidget(i)
		if w == nil {
			t.Fatalf("thinking msg[%d]: no widget found", i)
		}
		yrStart, yrEnd := w.Y, w.Y+w.Height

		// Scroll so the message is visible in viewport.
		if yrEnd-yrStart >= m.viewport.Height {
			m.viewport.YOffset = yrStart
		} else {
			m.viewport.YOffset = yrStart - min(yrStart, (m.viewport.Height-(yrEnd-yrStart))/2)
		}

		// Pick a click row in the middle of the viewport-visible portion.
		visStart := yrStart - m.viewport.YOffset
		visEnd := yrEnd - m.viewport.YOffset
		visEnd = min(visEnd, m.viewport.Height)
		visStart = max(visStart, 0)
		clickY := visStart + (visEnd-visStart)/2

		// Reset dirty state.
		for j := range m.messages {
			m.messages[j].dirty = false
		}

		// After renderContent(), overflowed thinking blocks are auto-folded
		// (Collapsed=true). So first click should unfold (Collapsed=false).
		if !handleViewportClickCheck(m, clickY, i, t) {
			continue
		}
		if m.messages[i].Collapsed {
			t.Errorf("thinking msg[%d]: expected Collapsed=false after first click (unfold)", i)
		}

		// Re-render to update layout for expanded layout.
		_ = m.renderContent()

		// Second click: should re-fold (Collapsed=true).
		// Recalculate widget position for the expanded layout.
		w = findWidget(i)
		if w == nil {
			t.Fatalf("thinking msg[%d]: widget disappeared after unfold", i)
		}
		yrStart, yrEnd = w.Y, w.Y+w.Height
		visStart = yrStart - m.viewport.YOffset
		visEnd = yrEnd - m.viewport.YOffset
		visEnd = min(visEnd, m.viewport.Height)
		visStart = max(visStart, 0)
		clickY2 := visStart + (visEnd-visStart)/2

		m.handleViewportClick(clickY2)
		if !m.messages[i].Collapsed {
			t.Errorf("thinking msg[%d]: expected Collapsed=true after second click (re-fold) yRange=[%d,%d) yOff=%d clickY=%d",
				i, yrStart, yrEnd, m.viewport.YOffset, clickY2)
		}

		t.Logf("thinking msg[%d]: click1 y=%d yOff=%d unfold | click2 y=%d yOff=%d re-fold — both OK",
			i, clickY, m.viewport.YOffset, clickY2, m.viewport.YOffset)
	}
}

// TestHandleViewportClick_EdgeCases verifies boundary conditions.
func TestHandleViewportClick_EdgeCases(t *testing.T) {
	m := ChatModel{}
	m.viewport.Width = 80
	m.viewport.Height = 20
	m.messages = []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	_ = m.renderContent()

	// Click outside viewport (negative mouseY).
	if m.handleViewportClick(-1) {
		t.Error("expected false for mouseY=-1")
	}
	// Click outside viewport (beyond Height).
	if m.handleViewportClick(20) {
		t.Error("expected false for mouseY=20 >= Height")
	}

	// Click beyond contentHeight.
	m.viewport.YOffset = 100
	if m.handleViewportClick(0) {
		t.Error("expected false for contentY=100 >= contentHeight")
	}
	m.viewport.YOffset = 0

	// Click on non-foldable message (user).
	if m.handleViewportClick(headerLines) {
		t.Error("expected false for user message (no widget)")
	}

	// Stale layout (ContentHeight == 0).
	m.layout.ContentHeight = 0
	if m.handleViewportClick(0) {
		t.Error("expected false when layout is stale")
	}
}

// handleViewportClickCheck calls handleViewportClick and reports errors.
func handleViewportClickCheck(m ChatModel, clickY int, msgIdx int, t *testing.T) bool {
	t.Helper()
	result := m.handleViewportClick(clickY)
	if !result {
		w := findWidgetForTest(m, msgIdx)
		wInfo := "nil"
		if w != nil {
			wInfo = fmt.Sprintf("y=%d h=%d", w.Y, w.Height)
		}
		t.Errorf("thinking msg[%d]: handleViewportClick(y=%d) returned false\n"+
			"  widget=%s YOffset=%d viewport.Height=%d wasFolded=%v",
			msgIdx, clickY, wInfo,
			m.viewport.YOffset, m.viewport.Height, m.messages[msgIdx].wasFolded)
		return false
	}
	return true
}

func findWidgetForTest(m ChatModel, msgIdx int) *Widget {
	for i := range m.layout.Content {
		if m.layout.Content[i].Index == msgIdx {
			return &m.layout.Content[i]
		}
	}
	return nil
}
