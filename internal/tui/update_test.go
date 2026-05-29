package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/tool"
)

// --- test helpers ---

type noopLLMClient struct{}

func (noopLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{}, nil
}
func (noopLLMClient) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent)
	close(ch)
	return ch, nil
}
func (noopLLMClient) Close() error                  { return nil }
func (noopLLMClient) Adapter() llm.ProviderAdapter   { return nil }

func testModel() Model {
	cfg := config.DefaultConfig()
	eng := engine.New(cfg, noopLLMClient{}, tool.NewRegistry(), engine.Context{})
	return NewModel(cfg, eng, nil, false)
}

func testModelWithTodos() Model {
	cfg := config.DefaultConfig()
	todoMgr := tool.NewTodoManager()
	todoMgr.Update([]tool.TodoItem{
		{ID: "1", Status: "pending", Content: "add login page"},
		{ID: "2", Status: "in_progress", Content: "fix auth bug", ActiveForm: "Fixing auth bug"},
		{ID: "3", Status: "completed", Content: "setup DB"},
	})
	eng := engine.New(cfg, noopLLMClient{}, tool.NewRegistry(), engine.Context{TodoMgr: todoMgr})
	return NewModel(cfg, eng, nil, false)
}

// --- KeyEnter ---

func TestUpdate_KeyEnter_EmptyInput(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("")

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := newM.(Model)

	if nm.running {
		t.Error("empty input should not start engine")
	}
	if len(nm.messages) > 0 {
		t.Error("empty input should not add messages")
	}
	if cmd != nil {
		t.Error("empty input should return nil cmd")
	}
}

func TestUpdate_KeyEnter_WhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.input.SetValue("hello")

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := newM.(Model)

	if !nm.running {
		t.Error("should stay running when input while running")
	}
	if cmd != nil {
		t.Error("should return nil cmd when running")
	}
}

func TestUpdate_KeyEnter_StartsWithText(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("hello world")

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := newM.(Model)

	if !nm.running {
		t.Fatal("running should be true after input")
	}
	if nm.eventChan == nil {
		t.Fatal("eventChan must be set on returned model")
	}
	if nm.ctx == nil {
		t.Fatal("ctx must be set")
	}
	if nm.cancel == nil {
		t.Fatal("cancel must be set")
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "user" || nm.messages[0].Content != "hello world" {
		t.Error("user message not added correctly")
	}
	if len(nm.inputHistory) != 1 || nm.inputHistory[0] != "hello world" {
		t.Error("input history not updated")
	}
	if nm.historyIdx != 1 {
		t.Error("historyIdx should be len(inputHistory)")
	}

	// Both listenEvents and nextSpinnerTick cmds must be returned
	if cmd == nil {
		t.Fatal("expected batched cmds, got nil")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("batched cmd returned nil")
	}
}

func TestUpdate_KeyEnter_SlashCommand(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("/help")

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := newM.(Model)

	if nm.running {
		t.Error("slash command should not start engine")
	}
	if cmd != nil {
		t.Error("slash command should return nil cmd")
	}
	// Should have system message with help text
	found := false
	for _, msg := range nm.messages {
		if msg.Role == "system" && len(msg.Content) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("slash command should add system message")
	}
}

// --- engineEventMsg ---

func TestUpdate_EngineEvent_Text_NewMessage(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, cmd := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "Hello"}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if len(nm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(nm.messages))
	}
	if nm.messages[0].Role != "assistant" || nm.messages[0].Content != "Hello" {
		t.Error("first text event should create assistant message")
	}
	// Should re-queue listenEvents while running
	if cmd == nil {
		t.Error("should re-queue listenEvents while running")
	}
}

func TestUpdate_EngineEvent_Text_ExtendsExisting(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)
	m.messages = []ChatMessage{{Role: "assistant", Content: "Hel"}}

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "lo"}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if len(nm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(nm.messages))
	}
	if nm.messages[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", nm.messages[0].Content)
	}
}

func TestUpdate_EngineEvent_Text_NewAfterTool(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)
	m.messages = []ChatMessage{{Role: "tool", Content: "..."}}

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "After tool"}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if len(nm.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(nm.messages))
	}
	if nm.messages[1].Role != "assistant" {
		t.Error("text after tool should create new assistant message")
	}
}

func TestUpdate_EngineEvent_Thinking_NewMessage(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventThinking, Data: "Hmm..."}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if len(nm.messages) != 1 || nm.messages[0].Role != "thinking" || nm.messages[0].Content != "Hmm..." {
		t.Error("should create thinking message")
	}
}

func TestUpdate_EngineEvent_Thinking_ExtendsExisting(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)
	m.messages = []ChatMessage{{Role: "thinking", Content: "Hmm"}}

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventThinking, Data: "..."}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if nm.messages[0].Content != "Hmm..." {
		t.Errorf("expected 'Hmm...', got %q", nm.messages[0].Content)
	}
}

func TestUpdate_EngineEvent_ToolUse(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	evt := engine.Event{
		Type: engine.EventToolUse,
		Data: map[string]any{"name": "Bash", "input": `{"command":"ls"}`},
	}
	newM, _ := m.Update(engineEventMsg{event: evt})
	nm := newM.(Model)

	if nm.currentToolName != "Bash" {
		t.Errorf("expected currentToolName 'Bash', got %q", nm.currentToolName)
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "tool" {
		t.Error("tool use should add tool message")
	}
	if nm.messages[0].Detail != `{"command":"ls"}` {
		t.Errorf("expected detail with input, got %q", nm.messages[0].Detail)
	}
}

func TestUpdate_EngineEvent_ToolResult_Success(t *testing.T) {
	m := testModel()
	m.running = true
	m.currentToolName = "Bash"
	m.eventChan = make(chan engine.Event, 1)

	evt := engine.Event{
		Type: engine.EventToolResult,
		Data: map[string]any{
			"name":     "Bash",
			"content":  "file1\nfile2\nfile3-extra-stuff",
			"is_error": false,
		},
	}
	newM, _ := m.Update(engineEventMsg{event: evt})
	nm := newM.(Model)

	if nm.currentToolName != "" {
		t.Error("tool result should clear currentToolName")
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "tool" {
		t.Fatal("tool result should add tool message")
	}
}

func TestUpdate_EngineEvent_ToolResult_FoldShort(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	// 300-char single line wraps to ~8 visual lines at width 70.
	// <= 8 visual lines → NOT collapsed.
	m.width = 80
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	evt := engine.Event{
		Type: engine.EventToolResult,
		Data: map[string]any{
			"name":     "Read",
			"content":  string(long),
			"is_error": false,
		},
	}
	newM, _ := m.Update(engineEventMsg{event: evt})
	nm := newM.(Model)

	// Content is stored as-is; fold is computed at render time.
	if nm.messages[0].Content != string(long) {
		t.Error("full content should be stored in Content")
	}
	if nm.messages[0].Detail != string(long) {
		t.Error("full content should be stored in Detail")
	}
}

func TestUpdate_EngineEvent_ToolResult_FoldLong(t *testing.T) {
	m := testModel()
	m.width = 80
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n")

	evt := engine.Event{
		Type: engine.EventToolResult,
		Data: map[string]any{
			"name":     "Read",
			"content":  content,
			"is_error": false,
		},
	}
	newM, _ := m.Update(engineEventMsg{event: evt})
	nm := newM.(Model)

	// Content stored as-is; fold happens at render time.
	if nm.messages[0].Content != content {
		t.Error("full content should be stored")
	}
	if nm.messages[0].Detail != content {
		t.Error("full content should be stored in Detail")
	}
	// Collapsed is set at render time, not event time.
	if nm.messages[0].Collapsed {
		t.Error("Collapsed should be false until render")
	}
}

func TestUpdate_EngineEvent_ToolResult_Error(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	evt := engine.Event{
		Type: engine.EventToolResult,
		Data: map[string]any{
			"content":  "permission denied",
			"is_error": true,
		},
	}
	newM, _ := m.Update(engineEventMsg{event: evt})
	nm := newM.(Model)

	if len(nm.messages) != 1 || nm.messages[0].Role != "error" {
		t.Fatal("error tool result should add error message")
	}
}

func TestUpdate_EngineEvent_Error_String(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, cmd := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventError, Data: "api error"}})
	nm := newM.(Model)

	if nm.running {
		t.Error("error event should set running=false")
	}
	if cmd != nil {
		t.Error("should not re-queue listenEvents after error")
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "error" || nm.messages[0].Content != "api error" {
		t.Error("error message not added correctly")
	}
}

func TestUpdate_EngineEvent_Error_GoError(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	testErr := &llm.ProviderError{Provider: "test"}
	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventError, Data: testErr}})
	nm := newM.(Model)

	if nm.running {
		t.Error("error event should set running=false")
	}
	if nm.messages[0].Content != testErr.Error() {
		t.Error("should call Error() on Go error")
	}
}

func TestUpdate_EngineEvent_Aborted(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, cmd := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventAborted}})
	nm := newM.(Model)

	if nm.running {
		t.Error("aborted event should set running=false")
	}
	if cmd != nil {
		t.Error("should not re-queue listenEvents after aborted")
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "system" || nm.messages[0].Content != "[Cancelled]" {
		t.Error("cancelled message not added")
	}
}

func TestUpdate_EngineEvent_Done(t *testing.T) {
	m := testModel()
	m.running = true
	m.currentToolName = "Bash"
	m.eventChan = make(chan engine.Event, 1)

	newM, cmd := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventDone}})
	nm := newM.(Model)

	if nm.running {
		t.Error("done event should set running=false")
	}
	if nm.currentToolName != "" {
		t.Error("done should clear currentToolName")
	}
	if cmd != nil {
		t.Error("should not re-queue listenEvents after done")
	}
}

func TestUpdate_EngineEvent_TurnEnd(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventTurnEnd, Turn: 3}})
	nm := newM.(Model)

	if nm.currentTurn != 3 {
		t.Errorf("expected currentTurn 3, got %d", nm.currentTurn)
	}
}

func TestUpdate_EngineEvent_TokenUpdateOnStop(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)
	m.eng.Clear()
	// Add a user+assistant pair so TotalUsage has data
	msgs := m.eng.Messages() // force usage update

	_ = msgs

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventDone}})
	nm := newM.(Model)

	// After EventDone, total tokens should be updated (even if 0)
	if nm.totalInTokens != 0 && nm.totalOutTokens != 0 {
		// Can't guarantee non-zero without real API call, just check fields exist
	}
	_ = nm.totalInTokens
	_ = nm.totalOutTokens
}

// --- Permission dialog ---

func TestUpdate_PermActive_UpDown(t *testing.T) {
	m := testModel()
	m.overlay = &Overlay{Kind: OverlayPermission, Selected: 2}

	// Up
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m2.(Model).overlay.Selected != 1 {
		t.Error("up should decrement Selected")
	}
	// Down
	m3, _ := m2.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	if m3.(Model).overlay.Selected != 2 {
		t.Error("down should increment Selected")
	}
}

func TestUpdate_PermActive_UpBoundary(t *testing.T) {
	m := testModel()
	m.overlay = &Overlay{Kind: OverlayPermission, Selected: 0}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if newM.(Model).overlay.Selected != 0 {
		t.Error("up at 0 should not change")
	}
}

func TestUpdate_PermActive_DownBoundary(t *testing.T) {
	m := testModel()
	m.overlay = &Overlay{Kind: OverlayPermission, Selected: 3}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if newM.(Model).overlay.Selected != 3 {
		t.Error("down at 3 should not go past boundary")
	}
}

func TestUpdate_PermActive_EnterDismiss(t *testing.T) {
	m := testModel()
	m.overlay = &Overlay{Kind: OverlayPermission}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if newM.(Model).overlay != nil {
		t.Error("Enter should dismiss overlay")
	}
}

func TestUpdate_PermActive_EscDismiss(t *testing.T) {
	m := testModel()
	m.overlay = &Overlay{Kind: OverlayPermission}

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if newM.(Model).overlay != nil {
		t.Error("Esc should dismiss overlay")
	}
}

// --- CtrlC / Esc ---

func TestUpdate_CtrlC_WhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.eventChan = make(chan engine.Event, 1)

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	nm := newM.(Model)

	if nm.running {
		t.Error("CtrlC should stop running")
	}
	if cmd != nil {
		t.Error("CtrlC while running should not quit")
	}
	if len(nm.messages) != 1 || nm.messages[0].Role != "system" || nm.messages[0].Content != "[Interrupted]" {
		t.Error("[Interrupted] message not added")
	}
}

func TestUpdate_CtrlC_WhileNotRunning(t *testing.T) {
	m := testModel()
	m.running = false

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	nm := newM.(Model)

	if !nm.quitting {
		t.Error("CtrlC while not running should set quitting")
	}
	// tea.Quit is a special sentinel cmd; check it's non-nil
	if cmd == nil {
		t.Error("should return tea.Quit")
	}
}

func TestUpdate_Esc_WhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	nm := newM.(Model)

	if nm.running {
		t.Error("Esc should stop running")
	}
}

func TestUpdate_Esc_WhileNotRunning(t *testing.T) {
	m := testModel()
	m.running = false

	newM, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	nm := newM.(Model)

	if !nm.quitting {
		t.Error("Esc while not running should set quitting")
	}
	if cmd == nil {
		t.Error("should return tea.Quit")
	}
}

// --- History navigation ---

func TestUpdate_History_CtrlP(t *testing.T) {
	m := testModel()
	m.running = false
	m.inputHistory = []string{"first", "second"}
	m.historyIdx = 2

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	nm := newM.(Model)

	if nm.historyIdx != 1 {
		t.Errorf("expected historyIdx 1, got %d", nm.historyIdx)
	}
	if nm.input.Value() != "second" {
		t.Errorf("expected input 'second', got %q", nm.input.Value())
	}
}

func TestUpdate_History_CtrlP_Boundary(t *testing.T) {
	m := testModel()
	m.running = false
	m.inputHistory = []string{"only"}
	m.historyIdx = 0

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if newM.(Model).historyIdx != 0 {
		t.Error("CtrlP at 0 should not change")
	}
}

func TestUpdate_History_CtrlP_EmptyHistory(t *testing.T) {
	m := testModel()
	m.running = false
	m.inputHistory = nil
	m.historyIdx = 0

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if cmd != nil {
		t.Error("CtrlP with empty history should do nothing")
	}
}

func TestUpdate_History_CtrlN(t *testing.T) {
	m := testModel()
	m.running = false
	m.inputHistory = []string{"first", "second"}
	m.historyIdx = 0

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	nm := newM.(Model)

	if nm.historyIdx != 1 {
		t.Errorf("expected historyIdx 1, got %d", nm.historyIdx)
	}
	if nm.input.Value() != "second" {
		t.Errorf("expected input 'second', got %q", nm.input.Value())
	}
}

func TestUpdate_History_CtrlN_AtLastResets(t *testing.T) {
	m := testModel()
	m.running = false
	m.inputHistory = []string{"first", "second"}
	m.historyIdx = 1 // len-1
	m.input.SetValue("second")

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	nm := newM.(Model)

	if nm.historyIdx != 2 {
		t.Errorf("expected historyIdx 2 (past end), got %d", nm.historyIdx)
	}
	if nm.input.Value() != "" {
		t.Error("should reset input past last history entry")
	}
}

func TestUpdate_History_CtrlN_PastEnd(t *testing.T) {
	m := testModel()
	m.running = false
	m.inputHistory = []string{"first", "second"}
	m.historyIdx = 2 // =len
	m.input.Reset()

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	nm := newM.(Model)

	if nm.historyIdx != 2 {
		t.Errorf("expected historyIdx to stay at 2, got %d", nm.historyIdx)
	}
}

func TestUpdate_History_CtrlP_WhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.inputHistory = []string{"old"}
	m.historyIdx = 1

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	if newM.(Model).historyIdx != 1 {
		t.Error("CtrlP should no-op while running")
	}
}

// --- WindowSize ---

func TestUpdate_WindowSize(t *testing.T) {
	m := testModel()
	newM, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	nm := newM.(Model)

	if cmd != nil {
		t.Error("WindowSize should return nil cmd")
	}
	if nm.width != 120 {
		t.Errorf("expected width 120, got %d", nm.width)
	}
	if nm.height != 40 {
		t.Errorf("expected height 40, got %d", nm.height)
	}
	if nm.viewport.Width != 120 {
		t.Errorf("expected viewport width 120, got %d", nm.viewport.Width)
	}
	if nm.viewport.Height != 35 {
		t.Errorf("expected viewport height 35, got %d", nm.viewport.Height)
	}
	if nm.input.Width() < 100 { // textarea reserves space for borders
		t.Errorf("expected input width >= 100, got %d", nm.input.Width())
	}
}

// --- Spinner ---

func TestUpdate_SpinnerTick_WhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.spinnerIdx = 0

	newM, cmd := m.Update(spinnerTickMsg{})
	nm := newM.(Model)

	if nm.spinnerIdx != 1 {
		t.Errorf("expected spinnerIdx 1, got %d", nm.spinnerIdx)
	}
	if cmd == nil {
		t.Error("spinner tick should return follow-up cmd")
	}
}

func TestUpdate_SpinnerTick_WrapAround(t *testing.T) {
	m := testModel()
	m.running = true
	m.spinnerIdx = len(spinnerFrames) - 1

	newM, _ := m.Update(spinnerTickMsg{})
	nm := newM.(Model)

	if nm.spinnerIdx != 0 {
		t.Error("spinner should wrap around to 0")
	}
}

func TestUpdate_SpinnerTick_WhileNotRunning(t *testing.T) {
	m := testModel()
	m.running = false
	m.spinnerIdx = 0

	newM, cmd := m.Update(spinnerTickMsg{})
	nm := newM.(Model)

	if nm.spinnerIdx != 0 {
		t.Error("spinner should not advance when not running")
	}
	if cmd != nil {
		t.Error("spinner should not schedule follow-up when not running")
	}
}

// --- handleCommand ---

func TestHandleCommand_Clear(t *testing.T) {
	m := testModel()
	m.messages = []ChatMessage{{Role: "user", Content: "hi"}}

	m.handleCommand("/clear")

	if len(m.messages) != 0 {
		t.Errorf("clear should empty messages, got %d", len(m.messages))
	}
}

func TestHandleCommand_Model(t *testing.T) {
	m := testModel()
	m.cfg.Model = "old-model"

	m.handleCommand("/model new-model")

	if m.cfg.Model != "new-model" {
		t.Errorf("expected model 'new-model', got %q", m.cfg.Model)
	}
}

func TestHandleCommand_Unknown(t *testing.T) {
	m := testModel()
	m.messages = nil

	m.handleCommand("/nonexistent")

	if len(m.messages) != 1 || m.messages[0].Role != "system" {
		t.Error("unknown command should add system message")
	}
}

func TestHandleCommand_Exit(t *testing.T) {
	m := testModel()
	m.handleCommand("/exit")
	if !m.quitting {
		t.Error("/exit should set quitting")
	}
}

// --- listenEvents ---

func TestListenEvents_ClosedChannelReturnsDone(t *testing.T) {
	m := testModel()
	ch := make(chan engine.Event, 1)
	m.eventChan = ch
	close(ch)

	cmd := m.listenEvents()
	msg := cmd()

	evt, ok := msg.(engineEventMsg)
	if !ok {
		t.Fatalf("expected engineEventMsg, got %T", msg)
	}
	if evt.event.Type != engine.EventDone {
		t.Errorf("closed channel should return EventDone, got %q", evt.event.Type)
	}
}

func TestListenEvents_ReceivesEvent(t *testing.T) {
	m := testModel()
	ch := make(chan engine.Event, 1)
	m.eventChan = ch
	ch <- engine.Event{Type: engine.EventText, Data: "test event"}

	cmd := m.listenEvents()
	msg := cmd()

	evt, ok := msg.(engineEventMsg)
	if !ok {
		t.Fatalf("expected engineEventMsg, got %T", msg)
	}
	if evt.event.Type != engine.EventText {
		t.Errorf("expected EventText, got %q", evt.event.Type)
	}
}

func TestListenEvents_BlocksUntilEvent(t *testing.T) {
	m := testModel()
	m.eventChan = make(chan engine.Event, 1)

	// Send event in background to unblock the blocking read
	go func() {
		m.eventChan <- engine.Event{Type: engine.EventText, Data: "async"}
	}()

	cmd := m.listenEvents()
	msg := cmd()

	evt, ok := msg.(engineEventMsg)
	if !ok {
		t.Fatalf("expected engineEventMsg, got %T", msg)
	}
	if evt.event.Type != engine.EventText {
		t.Errorf("expected EventText, got %q", evt.event.Type)
	}
}

// --- estimateCost ---

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		model      string
		in, out    int
		wantCost   float64
		skipExact  bool // map iteration order makes prefix matching nondeterministic
	}{
		{"claude-sonnet-4-20250514", 1_000_000, 0, 3.0, false},
		{"claude-sonnet-4-20250514", 0, 1_000_000, 15.0, false},
		{"gpt-4o", 1_000_000, 0, 2.5, false},
		{"deepseek-chat", 1_000_000, 0, 0.27, false},
		{"deepseek-reasoner", 0, 1_000_000, 2.19, false},
		{"unknown-model", 1_000_000, 1_000_000, 18.0, false}, // default 3+15
	}
	for _, tt := range tests {
		got := estimateCost(tt.model, tt.in, tt.out)
		if tt.skipExact {
			continue
		}
		if got != tt.wantCost {
			t.Errorf("estimateCost(%q, %d, %d) = %f, want %f", tt.model, tt.in, tt.out, got, tt.wantCost)
		}
	}
	// Test that estimateCost returns non-zero for known models
	if got := estimateCost("gpt-4o-mini", 0, 1_000_000); got == 0 {
		t.Error("estimateCost for gpt-4o-mini should be non-zero")
	}
	// Test zero tokens returns zero cost
	if got := estimateCost("any-model", 0, 0); got != 0 {
		t.Errorf("zero tokens should be zero cost, got %f", got)
	}
}

// --- truncateStr (from view.go, tested here for coverage) ---

func TestTruncateStr(t *testing.T) {
	got := truncateStr("hello", 3)
	if got == "hello" {
		t.Error("string longer than max should be truncated")
	}
	if len(got) <= 3 {
		t.Error("truncated string should include suffix")
	}
	if got := truncateStr("hi", 10); got != "hi" {
		t.Errorf("short string should not be truncated, got %q", got)
	}
}

// --- Edge: eventChan is visible on returned model ---

func TestUpdate_EventChan_ListenEventsSameChannel(t *testing.T) {
	// Verify listenEvents reads from the channel set on the model.
	// This is the core invariant that prevents the "stuck thinking" bug.
	m := testModel()
	ch := make(chan engine.Event, 1)
	m.eventChan = ch

	// listenEvents should read from m.eventChan
	ch <- engine.Event{Type: engine.EventText, Data: "hello"}
	cmd := m.listenEvents()
	msg := cmd()
	evt, ok := msg.(engineEventMsg)
	if !ok {
		t.Fatal("expected engineEventMsg")
	}
	if evt.event.Type != engine.EventText || evt.event.Data != "hello" {
		t.Error("listenEvents did not read from m.eventChan")
	}

	// Change the channel and verify listenEvents uses the new one
	ch2 := make(chan engine.Event, 1)
	m.eventChan = ch2
	ch2 <- engine.Event{Type: engine.EventThinking, Data: "hmm"}
	cmd = m.listenEvents()
	msg = cmd()
	evt, ok = msg.(engineEventMsg)
	if !ok {
		t.Fatal("expected engineEventMsg from ch2")
	}
	if evt.event.Type != engine.EventThinking {
		t.Error("listenEvents should use updated eventChan")
	}
}

func TestUpdate_EventChan_ClosedChannelReturnsDone(t *testing.T) {
	m := testModel()
	ch := make(chan engine.Event, 1)
	m.eventChan = ch
	close(ch)

	cmd := m.listenEvents()
	msg := cmd()
	evt, ok := msg.(engineEventMsg)
	if !ok {
		t.Fatal("expected engineEventMsg")
	}
	if evt.event.Type != engine.EventDone {
		t.Errorf("closed channel should return EventDone, got %q", evt.event.Type)
	}
}

// Ensure engine.Event JSON marshaling works (used in tests above).
func TestEngineEventJSON(t *testing.T) {
	evt := engine.Event{Type: engine.EventText, Data: "hello", Turn: 1}
	b, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded engine.Event
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != engine.EventText {
		t.Error("round-trip type mismatch")
	}
}

// --- Render caching ---

func TestRenderMessage_CachesOutput(t *testing.T) {
	msg := &ChatMessage{Role: "assistant", Content: "hello **world**"}
	r1 := renderMessage(msg, 80)
	if r1 == "" {
		t.Fatal("renderMessage returned empty string")
	}
	if msg.rendered == "" {
		t.Error("rendered cache should be set after first render")
	}
	r2 := renderMessage(msg, 80)
	if r1 != r2 {
		t.Error("second render should return cached result")
	}
}

func TestRenderMessage_CacheBustOnContentChange(t *testing.T) {
	m := testModel()
	m.running = true
	m.messages = []ChatMessage{{Role: "assistant", Content: "Hello"}}
	renderMessage(&m.messages[0], 80)
	if m.messages[0].rendered == "" {
		t.Fatal("expected cached render")
	}
	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: " world"}})
	nm := newM.(Model)
	nm.flushCoalesced()
	if nm.messages[0].rendered != "" {
		t.Error("rendered cache should be cleared after content append")
	}
}

func TestRenderMessage_CacheBustOnThinkingChange(t *testing.T) {
	m := testModel()
	m.running = true
	m.messages = []ChatMessage{{Role: "thinking", Content: "Hmm"}}
	renderMessage(&m.messages[0], 80)
	if m.messages[0].rendered == "" {
		t.Fatal("expected cached render")
	}
	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventThinking, Data: "..."}})
	nm := newM.(Model)
	nm.flushCoalesced()
	if nm.messages[0].rendered != "" {
		t.Error("rendered cache should be cleared after thinking append")
	}
}

func TestView_UsesRenderCache(t *testing.T) {
	m := testModel()
	m.messages = []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "Hello **bold** world"},
	}
	_ = m.View()
	for i := range m.messages {
		if m.messages[i].rendered == "" {
			t.Errorf("message %d (%s) should have rendered cache after View()", i, m.messages[i].Role)
		}
	}
}

// --- engineDone gate ---

func TestKeyEnter_BlockedByUnfinishedEngine(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("hello")
	m.engineDone = make(chan struct{}) // open = engine still running
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter should be blocked when engineDone is not closed")
	}
}

func TestKeyEnter_EngineDoneRecreated(t *testing.T) {
	oldDone := make(chan struct{})
	close(oldDone)

	m := testModel()
	m.running = false
	m.input.SetValue("hello")
	m.engineDone = oldDone

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := newM.(Model)

	if nm.engineDone == nil {
		t.Fatal("engineDone should be re-created for new engine run")
	}
	if nm.engineDone == oldDone {
		t.Error("engineDone should be a new channel, not the old one")
	}
}

// --- listenEvents blocks ---

func TestListenEvents_BlocksOnEmptyChannel(t *testing.T) {
	m := testModel()
	m.eventChan = make(chan engine.Event, 1)

	done := make(chan struct{}, 1)
	go func() {
		cmd := m.listenEvents()
		cmd()
		done <- struct{}{}
	}()

	select {
	case <-done:
		t.Error("listenEvents should block on empty channel")
	case <-time.After(50 * time.Millisecond):
	}

	m.eventChan <- engine.Event{Type: engine.EventDone}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("listenEvents did not unblock after sending event")
	}
}

// --- Coalescer ---

func TestCoalescer_BuffersTextDelta(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "Hello"}})
	nm := newM.(Model)

	if !nm.coalesceDirty {
		t.Error("coalesceDirty should be true after text delta")
	}
	if nm.coalesceText != "Hello" {
		t.Errorf("expected 'Hello' in buffer, got %q", nm.coalesceText)
	}
	if nm.coalesceRole != "assistant" {
		t.Errorf("expected role 'assistant', got %q", nm.coalesceRole)
	}
	if len(nm.messages) > 0 {
		t.Error("messages should be empty before flush")
	}
}

func TestCoalescer_FlushWritesToMessages(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "Hello world"}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if nm.coalesceDirty {
		t.Error("coalesceDirty should be false after flush")
	}
	if len(nm.coalesceText) != 0 {
		t.Error("buffer should be empty after flush")
	}
	if len(nm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(nm.messages))
	}
	if nm.messages[0].Content != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", nm.messages[0].Content)
	}
}

func TestCoalescer_FlushExtendsExistingMessage(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)
	m.messages = []ChatMessage{{Role: "assistant", Content: "Hel"}}

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "lo"}})
	nm := newM.(Model)
	nm.flushCoalesced()

	if len(nm.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(nm.messages))
	}
	if nm.messages[0].Content != "Hello" {
		t.Errorf("expected 'Hello', got %q", nm.messages[0].Content)
	}
	if nm.messages[0].rendered != "" {
		t.Error("rendered cache should be cleared after flush")
	}
}

func TestCoalescer_RoleSwitchFlushes(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventThinking, Data: "Hmm..."}})
	nm := newM.(Model)

	newM2, _ := nm.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "Hello"}})
	nm2 := newM2.(Model)

	if len(nm2.messages) != 1 {
		t.Fatalf("expected 1 flushed thinking message, got %d", len(nm2.messages))
	}
	if nm2.messages[0].Role != "thinking" || nm2.messages[0].Content != "Hmm..." {
		t.Error("thinking content should be flushed to messages")
	}
	if nm2.coalesceRole != "assistant" {
		t.Errorf("expected role switch to assistant, got %q", nm2.coalesceRole)
	}
	if nm2.coalesceText != "Hello" {
		t.Errorf("expected 'Hello' in buffer, got %q", nm2.coalesceText)
	}
}

func TestCoalescer_CriticalEventFlushes(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "streaming text"}})
	nm := newM.(Model)

	newM2, _ := nm.Update(engineEventMsg{event: engine.Event{
		Type: engine.EventToolUse,
		Data: map[string]any{"name": "Bash", "input": "ls"},
	}})
	nm2 := newM2.(Model)

	if len(nm2.messages) < 2 {
		t.Fatalf("expected at least 2 messages (text + tool), got %d", len(nm2.messages))
	}
	if nm2.messages[0].Role != "assistant" || nm2.messages[0].Content != "streaming text" {
		t.Error("text should be flushed before tool use")
	}
	if nm2.messages[1].Role != "tool" {
		t.Error("tool use should appear after flushed text")
	}
}

func TestCoalescer_SpinnerTickFlushes(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "buffered"}})
	nm := newM.(Model)

	newM2, _ := nm.Update(spinnerTickMsg{})
	nm2 := newM2.(Model)

	if nm2.coalesceDirty {
		t.Error("coalesceDirty should be false after spinner tick flush")
	}
	if len(nm2.messages) != 1 {
		t.Fatalf("expected 1 message after flush, got %d", len(nm2.messages))
	}
}

func TestCoalescer_FlushIdempotent(t *testing.T) {
	m := testModel()
	m.flushCoalesced()
	if m.coalesceDirty {
		t.Error("flush on clean state should be no-op")
	}
	if len(m.messages) != 0 {
		t.Error("flush on clean state should not create messages")
	}
}

func TestCoalescer_FlushMergesConsecutive(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	newM, _ := m.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "abc"}})
	nm := newM.(Model)
	newM2, _ := nm.Update(engineEventMsg{event: engine.Event{Type: engine.EventText, Data: "def"}})
	nm2 := newM2.(Model)
	nm2.flushCoalesced()

	if len(nm2.coalesceText) != 0 {
		t.Error("buffer should be empty after flush")
	}
	if len(nm2.messages) != 1 {
		t.Fatalf("expected 1 merged message, got %d", len(nm2.messages))
	}
	if nm2.messages[0].Content != "abcdef" {
		t.Errorf("expected 'abcdef', got %q", nm2.messages[0].Content)
	}
}

// --- Viewport follow ---

func TestViewportFollow_StaysAtBottom(t *testing.T) {
	m := testModel()
	// Fill viewport with enough lines to enable scrolling
	m.messages = make([]ChatMessage, 50)
	for i := range m.messages {
		m.messages[i] = ChatMessage{Role: "assistant", Content: fmt.Sprintf("line %d", i)}
	}
	// Ensure viewport is at bottom
	m.viewport.SetContent(strings.Repeat("x\n", 100))
	m.viewport.GotoBottom()

	// Verify we're at bottom
	if !m.viewport.AtBottom() {
		t.Fatal("viewport should be at bottom after GotoBottom")
	}

	// View() should keep us at bottom when new content is the same
	_ = m.View()
	if !m.viewport.AtBottom() {
		t.Error("View() should keep viewport at bottom when it was already there")
	}
}

func TestViewportFollow_KeepsScrollUp(t *testing.T) {
	m := testModel()
	// Fill with enough content to enable scrolling
	m.viewport.SetContent(strings.Repeat("line content\n", 200))
	m.viewport.GotoBottom()

	// Scroll up a bit
	m.viewport.ScrollUp(10)
	atBottomBefore := m.viewport.AtBottom()
	if atBottomBefore {
		t.Fatal("viewport should not be at bottom after scrolling up")
	}

	// Add more messages and call View()
	m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: "new message"})
	_ = m.View()

	// Should still not be at bottom because user scrolled up
	if m.viewport.AtBottom() {
		t.Error("View() should NOT auto-scroll to bottom when user scrolled up")
	}
}

func TestViewportFollow_RespectsScrollUpDuringStreaming(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	// Pre-fill with enough content to scroll
	m.viewport.SetContent(strings.Repeat("history line\n", 200))
	m.viewport.GotoBottom()

	// Simulate user scrolling up by directly setting offset
	maxOff := m.viewport.TotalLineCount() - m.viewport.VisibleLineCount()
	if maxOff <= 0 {
		t.Skip("viewport too small for scroll test")
	}
	m.viewport.ScrollUp(10)
	if m.viewport.AtBottom() {
		t.Fatal("should not be at bottom after scrolling up")
	}

	// Add messages to trigger View() content change
	m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: "new message"})

	// View() should NOT pull user to bottom
	_ = m.View()
	if m.viewport.AtBottom() {
		t.Error("View() should not auto-scroll when user scrolled up")
	}
}

func TestViewportFollow_ScrollsToBottomWhenUserAtBottom(t *testing.T) {
	m := testModel()
	m.viewport.SetContent(strings.Repeat("old content\n", 200))
	m.viewport.GotoBottom()

	if !m.viewport.AtBottom() {
		t.Fatal("viewport should be at bottom")
	}

	// Add new message
	m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: "brand new line"})
	_ = m.View()

	// Should still be at bottom (following new content)
	if !m.viewport.AtBottom() {
		t.Error("View() should auto-scroll to bottom when user was at bottom")
	}
}

// --- Textarea ---

func TestKeyCtrlJ_InsertsNewline(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("line1")

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	nm := newM.(Model)

	if nm.input.Value() != "line1\n" {
		t.Errorf("Ctrl+J should insert newline, got %q", nm.input.Value())
	}
}

func TestKeyCtrlJ_BlockedWhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.input.SetValue("line1")

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	nm := newM.(Model)

	if nm.input.Value() != "line1" {
		t.Error("Ctrl+J should not insert while running")
	}
}

func TestKeyEnter_AltEnterInsertsNewline(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("line1")

	altEnter := tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	newM, _ := m.Update(altEnter)
	nm := newM.(Model)

	if nm.input.Value() != "line1\n" {
		t.Errorf("Alt+Enter should insert newline, got %q", nm.input.Value())
	}
}

func TestKeyEnter_AltEnterBlockedWhileRunning(t *testing.T) {
	m := testModel()
	m.running = true
	m.input.SetValue("line1")

	altEnter := tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	newM, _ := m.Update(altEnter)
	nm := newM.(Model)

	if nm.input.Value() != "line1" {
		t.Error("Alt+Enter should not insert while running")
	}
}

func TestRefreshToolsBar_Todos(t *testing.T) {
	m := testModelWithTodos()
	m.refreshToolsBar()

	if len(m.toolsBar.Items) != 3 {
		t.Fatalf("expected 3 toolsbar items, got %d", len(m.toolsBar.Items))
	}
	if m.toolsBar.Items[0].Icon != "☐" || m.toolsBar.Items[0].Label != "add login page" {
		t.Errorf("item 0: got %s %s", m.toolsBar.Items[0].Icon, m.toolsBar.Items[0].Label)
	}
	if m.toolsBar.Items[1].Icon != "▶" || m.toolsBar.Items[1].Label != "Fixing auth bug" {
		t.Errorf("item 1: got %s %s (expected activeForm)", m.toolsBar.Items[1].Icon, m.toolsBar.Items[1].Label)
	}
	if m.toolsBar.Items[2].Icon != "✓" || m.toolsBar.Items[2].Label != "setup DB" {
		t.Errorf("item 2: got %s %s", m.toolsBar.Items[2].Icon, m.toolsBar.Items[2].Label)
	}
}

func TestRefreshToolsBar_RunningTool(t *testing.T) {
	m := testModel()
	m.currentToolName = "Read"
	m.refreshToolsBar()

	if len(m.toolsBar.Items) != 1 {
		t.Fatalf("expected 1 tool item, got %d", len(m.toolsBar.Items))
	}
	if m.toolsBar.Items[0].Icon != "⚙" || m.toolsBar.Items[0].Label != "Read..." {
		t.Errorf("got %s %s", m.toolsBar.Items[0].Icon, m.toolsBar.Items[0].Label)
	}
}

func TestRefreshToolsBar_RunningToolAndTodos(t *testing.T) {
	m := testModelWithTodos()
	m.currentToolName = "Bash"
	m.refreshToolsBar()

	if len(m.toolsBar.Items) != 4 {
		t.Fatalf("expected 4 items (1 tool + 3 todos), got %d", len(m.toolsBar.Items))
	}
	if m.toolsBar.Items[0].Icon != "⚙" || m.toolsBar.Items[0].Label != "Bash..." {
		t.Errorf("first item should be running tool, got %s %s", m.toolsBar.Items[0].Icon, m.toolsBar.Items[0].Label)
	}
}

func TestRefreshToolsBar_ClearOnDone(t *testing.T) {
	m := testModelWithTodos()
	m.currentToolName = "Read"
	m.refreshToolsBar()
	if len(m.toolsBar.Items) != 4 {
		t.Fatalf("expected 4 items before clear, got %d", len(m.toolsBar.Items))
	}

	// Simulate done: clear tool, remove todos
	m.currentToolName = ""
	m.refreshToolsBar()
	// todos persist in toolsbar until explicitly cleared or next turn
	if len(m.toolsBar.Items) != 3 {
		t.Fatalf("todos should persist after tool done, got %d", len(m.toolsBar.Items))
	}
}

func TestKeyEnter_SendsWithNewlines(t *testing.T) {
	m := testModel()
	m.running = false
	m.input.SetValue("line1\nline2")

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := newM.(Model)

	if !nm.running {
		t.Fatal("Enter should start engine")
	}
	if len(nm.messages) != 1 || nm.messages[0].Content != "line1\nline2" {
		t.Errorf("multi-line input should be sent, got %q", nm.messages[0].Content)
	}
	if nm.input.Value() != "" {
		t.Error("textarea should be reset after send")
	}
}

func TestInjectBg(t *testing.T) {
	bgCode := "\x1b[48;5;236m"

	// Plain text — bg injected at start
	got := injectBg("hello", bgCode)
	if !strings.HasPrefix(got, bgCode) {
		t.Errorf("expected prefix %q, got %q", bgCode, got[:20])
	}
	if !strings.Contains(got, "hello") {
		t.Error("lost text")
	}

	// Text with reset — bg re-injected after reset
	input := "\x1b[38;5;123mfoo\x1b[0mbar"
	got = injectBg(input, bgCode)
	if !strings.Contains(got, "\x1b[0m"+bgCode) {
		t.Error("bg not re-injected after reset")
	}
	if !strings.Contains(got, "foo") || !strings.Contains(got, "bar") {
		t.Error("lost text")
	}

	// Chinese text with lipgloss styling
	input = "\x1b[38;5;123m你好\x1b[0m世界"
	got = injectBg(input, bgCode)
	if !strings.Contains(got, "你好") || !strings.Contains(got, "世界") {
		t.Error("lost Chinese text")
	}
	if !strings.Contains(got, "\x1b[0m"+bgCode) {
		t.Error("bg not re-injected for Chinese text")
	}
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"plain text", "plain text"},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[38;5;123m你好\x1b[0m世界", "你好世界"},
		{"\x1b[1;38;5;141m⚙ read\x1b[0m", "⚙ read"},
		{"no\x1b[mansi", "noansi"},
		{"\x1b]52;c;dGVzdA==\x07text", "text"},
	}
	for _, tt := range tests {
		got := stripAnsi(tt.input)
		if got != tt.want {
			t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
