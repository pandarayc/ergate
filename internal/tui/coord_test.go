package tui

import (
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

	// Render once to populate wasFolded, rendered cache, and msgYStarts.
	_ = m.renderContent()

	t.Logf("msgYStarts:")
	for i, ys := range m.msgYStarts {
		t.Logf("  msg[%2d] role=%-10s yStart=%2d", i, m.messages[i].Role, ys)
	}

	// For each thinking message, simulate scrolling so the message is visible
	// in the viewport, then click at the center of its visible portion.
	buildMsgRange := func(i int) (int, int) {
		ys := m.msgYStarts[i]
		ye := ys + 1 // sentinel
		for j := i + 1; j < len(m.msgYStarts); j++ {
			if m.msgYStarts[j] >= 0 {
				ye = m.msgYStarts[j]
				break
			}
		}
		return ys, ye
	}

	for i := range m.messages {
		if m.messages[i].Role != "thinking" {
			continue
		}

		yrStart, yrEnd := buildMsgRange(i)

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

		// Re-render to update msgYStarts for expanded layout.
		_ = m.renderContent()

		// Second click: should re-fold (Collapsed=true).
		// Recalculate clickY for the expanded layout.
		yrStart, yrEnd = buildMsgRange(i)
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
	_ = m.renderContent() // populates msgYStarts

	// Click outside viewport (negative mouseY).
	if m.handleViewportClick(-1) {
		t.Error("expected false for mouseY=-1")
	}
	// Click outside viewport (beyond Height).
	if m.handleViewportClick(20) {
		t.Error("expected false for mouseY=20 >= Height")
	}

	// Click beyond contentHeight (user msg is ~2 lines, contentHeight ≈ 3).
	m.viewport.YOffset = 100
	if m.handleViewportClick(0) {
		t.Error("expected false for contentY=100 >= contentHeight")
	}
	m.viewport.YOffset = 0

	// Click on non-foldable message (user).
	if m.handleViewportClick(0) {
		t.Error("expected false for user message (wasFolded=false)")
	}

	// Stale msgYStarts (length mismatch).
	m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: "test"})
	if m.handleViewportClick(0) {
		t.Error("expected false when msgYStarts is stale")
	}
}

// handleViewportClickCheck calls handleViewportClick and reports errors.
func handleViewportClickCheck(m ChatModel, clickY int, msgIdx int, t *testing.T) bool {
	t.Helper()
	result := m.handleViewportClick(clickY)
	if !result {
		ys := m.msgYStarts[msgIdx]
		t.Errorf("thinking msg[%d]: handleViewportClick(y=%d) returned false\n"+
			"  msgYStart=%d YOffset=%d viewport.Height=%d wasFolded=%v",
			msgIdx, clickY, ys,
			m.viewport.YOffset, m.viewport.Height, m.messages[msgIdx].wasFolded)
		return false
	}
	return true
}
