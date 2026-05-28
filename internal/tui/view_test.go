package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// snapshot compares got to the golden file at testdata/<name>.
// Set UPDATE=1 to regenerate golden files.
func snapshot(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE=1 to generate)", path, err)
	}
	if got != string(want) {
		t.Errorf("snapshot mismatch in %s (-want +got):\n--- want\n%s\n--- got\n%s\n---", name, string(want), got)
	}
}

// sizedModel returns a model with fixed 80x24 terminal size applied.
func sizedModel() Model {
	m := testModel()
	m.width = 80
	m.height = 24
	m.viewport.Width = 80
	m.viewport.Height = 17 // height - 7
	m.input.SetWidth(76)   // width - 4
	return m
}

func TestView_Empty(t *testing.T) {
	m := sizedModel()
	got := m.View()
	snapshot(t, "view_empty.golden", got)
}

func TestView_WithMessages(t *testing.T) {
	m := sizedModel()
	m.messages = []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "Hi there! How can I help?"},
		{Role: "user", Content: "read main.go"},
		{Role: "tool", Content: "⚙ Read", Detail: "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}"},
		{Role: "assistant", Content: "The file contains a simple main function."},
	}
	m.totalInTokens = 500
	m.totalOutTokens = 120
	got := m.View()
	snapshot(t, "view_messages.golden", got)
}

func TestView_Thinking(t *testing.T) {
	m := sizedModel()
	m.messages = []ChatMessage{
		{Role: "user", Content: "read main.go"},
	}
	m.running = true
	m.spinnerIdx = 2
	m.currentToolName = "Read"
	m.currentTurn = 2
	m.totalInTokens = 200
	m.totalOutTokens = 80
	got := m.View()
	snapshot(t, "view_thinking.golden", got)
}

func TestView_DetailOverlay(t *testing.T) {
	m := sizedModel()
	m.overlay = &Overlay{
		Kind:         OverlayDetail,
		DetailTitle:  "⚙ Read",
		DetailContent: "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello world\")\n}\n",
	}
	got := m.View()
	snapshot(t, "view_detail.golden", got)
}

func TestView_DetailOverlaySearch(t *testing.T) {
	m := sizedModel()
	m.overlay = &Overlay{
		Kind:           OverlayDetail,
		DetailTitle:    "⚙ Read",
		DetailContent:  "line one\nline two src/main.go\nline three\nline four src/engine.go\nline five",
		DetailSearchMode: true,
		DetailSearchQ:  "src",
		DetailMatches: []DetailMatch{
			{Line: 1, Text: "line two src/main.go"},
			{Line: 3, Text: "line four src/engine.go"},
		},
		DetailMatchIdx: 0,
	}
	got := m.View()
	snapshot(t, "view_detail_search.golden", got)
}

func TestView_ToolsBarActive(t *testing.T) {
	m := sizedModel()
	m.messages = []ChatMessage{
		{Role: "user", Content: "build the app"},
		{Role: "assistant", Content: "Running tests..."},
	}
	m.toolsBar.Set([]ToolsBarItem{
		{Icon: "☐", Label: "3/10 tasks"},
		{Icon: "⚙", Label: "reading src/engine.go (3s)"},
		{Icon: "⚑", Label: "code-review: done"},
		{Icon: "⚑", Label: "test-runner: running"},
	})
	got := m.View()
	snapshot(t, "view_toolsbar.golden", got)
}

func TestView_ToolsBarOverflow(t *testing.T) {
	m := sizedModel()
	m.messages = []ChatMessage{
		{Role: "user", Content: "build all"},
	}
	items := make([]ToolsBarItem, 12)
	for i := range items {
		items[i] = ToolsBarItem{Icon: "⚙", Label: fmt.Sprintf("tool-%d running...", i+1)}
	}
	m.toolsBar.Set(items)
	got := m.View()
	snapshot(t, "view_toolsbar_overflow.golden", got)
}

func TestView_MultiLineInput(t *testing.T) {
	m := sizedModel()
	m.messages = []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "Hi!"},
	}
	m.input.SetValue("line one\nline two\nline three")
	got := m.View()
	snapshot(t, "view_multiline.golden", got)
}
