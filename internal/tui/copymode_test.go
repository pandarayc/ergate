package tui

import (
	"strings"
	"testing"
)

func makeContent(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat("x", 40))
	}
	return b.String()
}

// TestCopyMode_TwoConsecutiveCopies tests that two drag-copies in a row both work.
func TestCopyMode_TwoConsecutiveCopies(t *testing.T) {
	cm := &CopyMode{}
	content := makeContent(20)
	cm.SetContent(content)

	// First copy: drag from (2,3) to (10,3) on the same line.
	cm.Enter(2, 3, 0)
	cm.Track(10, 3, 0)
	text1 := cm.Finish()
	if text1 == "" {
		t.Fatal("first copy returned empty string")
	}
	t.Logf("first copy: %q", text1)

	// Verify settled state.
	if !cm.IsActive() {
		t.Fatal("expected IsActive=true after Finish (settled)")
	}
	if !cm.settled {
		t.Fatal("expected settled=true after Finish")
	}

	// Second copy: drag from (5,10) to (15,12).
	cm.Enter(5, 10, 0)
	cm.Track(15, 12, 0)
	text2 := cm.Finish()
	if text2 == "" {
		t.Fatal("second copy returned empty string — BUG: second copy fails after first settled copy")
	}
	t.Logf("second copy: %q", text2)
}

// TestCopyMode_ClickThenDrag tests that a click-without-drag (Cancel) followed
// by a proper drag-copy works. This simulates accidental click then intentional copy.
func TestCopyMode_ClickThenDrag(t *testing.T) {
	cm := &CopyMode{}
	content := makeContent(20)
	cm.SetContent(content)

	// Accidental click without drag.
	cm.Enter(2, 3, 0)
	cm.Cancel()
	if cm.IsActive() {
		t.Fatal("expected IsActive=false after Cancel")
	}

	// Now proper drag-copy.
	// This simulates the real scenario where SetContent is called during View()
	// between Cancel and the next copy. Without SetContent, wrappedLines is nil.
	cm.SetContent(content) // re-populate (simulates View calling SetContent)
	cm.Enter(2, 5, 0)
	cm.Track(10, 5, 0)
	text := cm.Finish()
	if text == "" {
		t.Fatal("copy after Cancel returned empty string — BUG: wrappedLines not restored after Cancel")
	}
	t.Logf("copy after click: %q", text)
}

// TestCopyMode_ClickThenDrag_NoSetContent simulates the real TUI scenario where
// viewDirty is false after Cancel, so SetContent is not called before the next copy.
// This is the suspected root cause of the second-copy failure.
func TestCopyMode_ClickThenDrag_NoSetContent(t *testing.T) {
	cm := &CopyMode{}
	content := makeContent(20)
	cm.SetContent(content)

	// First copy works.
	cm.Enter(2, 3, 0)
	cm.Track(10, 3, 0)
	text1 := cm.Finish()
	if text1 == "" {
		t.Fatal("first copy returned empty string")
	}

	// Accidental click without drag — Cancel.
	cm.Enter(5, 10, 0)
	cm.Cancel()

	// Simulate viewDirty=false: SetContent is NOT called between Cancel and next Enter.
	// This is what happens in the real TUI when nothing triggers viewDirty=true.
	cm.Enter(2, 8, 0)
	cm.Track(15, 8, 0)
	text2 := cm.Finish()
	if text2 == "" {
		t.Fatal("BUG CONFIRMED: second copy fails when SetContent not called after Cancel")
	}
	t.Logf("second copy (no SetContent): %q", text2)
}

// TestCopyMode_ContentChangeBetweenCopies tests copy after content line count changes.
func TestCopyMode_ContentChangeBetweenCopies(t *testing.T) {
	cm := &CopyMode{}

	// Initial content.
	cm.SetContent(makeContent(20))

	// First copy.
	cm.Enter(2, 3, 0)
	cm.Track(10, 3, 0)
	text1 := cm.Finish()
	if text1 == "" {
		t.Fatal("first copy returned empty string")
	}

	// Content changes (e.g., streaming tokens arrived).
	// settled=true, line count 20 → 25: Cancel triggered.
	cm.SetContent(makeContent(25))
	if cm.IsActive() {
		t.Fatal("expected IsActive=false after content change cancels settled selection")
	}

	// Now SetContent would be called again with new content before next copy (viewDirty).
	cm.SetContent(makeContent(25))
	cm.Enter(2, 3, 0)
	cm.Track(10, 3, 0)
	text2 := cm.Finish()
	if text2 == "" {
		t.Fatal("copy after content change returned empty string")
	}
	t.Logf("copy after content change: %q", text2)
}

// TestCopyMode_AnchorResetOnSecondCopy ensures anchor coordinates are correct
// for the second copy (not stale from the first).
func TestCopyMode_AnchorResetOnSecondCopy(t *testing.T) {
	cm := &CopyMode{}
	cm.SetContent(makeContent(20))

	// First copy at (2,3) → (10,3).
	cm.Enter(2, 3, 0)
	cm.Track(10, 3, 0)
	cm.Finish()

	// Second copy at (30,15) → (35,15) — different position.
	cm.Enter(30, 15, 0)
	cm.Track(35, 15, 0)
	text := cm.Finish()

	if text == "" {
		t.Fatal("second copy returned empty string")
	}
	// The text should be from line 15, not line 3.
	t.Logf("second copy at different position: %q", text)
}

// TestCopyMode_MultiLineSelection ensures multi-line selection works.
func TestCopyMode_MultiLineSelection(t *testing.T) {
	cm := &CopyMode{}
	cm.SetContent(makeContent(20))

	// Select from line 3 col 10 to line 6 col 20.
	cm.Enter(10, 3, 0)
	cm.Track(20, 6, 0)
	text := cm.Finish()
	if text == "" {
		t.Fatal("multi-line copy returned empty string")
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 4 { // lines 3,4,5,6
		t.Fatalf("expected 4 lines, got %d: %q", len(lines), text)
	}
	t.Logf("multi-line copy: %d lines", len(lines))
}

// TestCopyMode_ReverseSelection tests selecting from bottom-right to top-left.
func TestCopyMode_ReverseSelection(t *testing.T) {
	cm := &CopyMode{}
	cm.SetContent(makeContent(20))

	// Select from (20,6) back to (10,3) — bottom-right to top-left.
	cm.Enter(20, 6, 0)
	cm.Track(10, 3, 0)
	text := cm.Finish()
	if text == "" {
		t.Fatal("reverse selection returned empty string")
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	t.Logf("reverse selection: %d lines", len(lines))
}

// TestCopyMode_HasSelectionAfterSettled verifies HasSelection state after Finish.
func TestCopyMode_HasSelectionAfterSettled(t *testing.T) {
	cm := &CopyMode{}
	cm.SetContent(makeContent(20))

	// After Enter (no drag yet), HasSelection should be false.
	cm.Enter(2, 3, 0)
	if cm.HasSelection() {
		t.Fatal("HasSelection should be false after Enter (no drag)")
	}

	// After Track, HasSelection should be true.
	cm.Track(10, 3, 0)
	if !cm.HasSelection() {
		t.Fatal("HasSelection should be true after Track")
	}

	// After Finish, HasSelection should still be true (settled).
	cm.Finish()
	if !cm.HasSelection() {
		t.Fatal("HasSelection should be true in settled state")
	}

	// After second Enter (new copy), HasSelection is false until Track.
	cm.Enter(5, 5, 0)
	if cm.HasSelection() {
		t.Fatal("HasSelection should be false after Enter for second copy")
	}
	cm.Track(15, 5, 0)
	if !cm.HasSelection() {
		t.Fatal("HasSelection should be true after Track for second copy")
	}
}
