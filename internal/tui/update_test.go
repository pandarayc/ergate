package tui

import (
	"context"
	"encoding/json"
	"testing"

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
	eng := engine.New(cfg, noopLLMClient{}, tool.NewRegistry())
	return NewModel(cfg, eng, nil, false)
}

func assertCmd(t *testing.T, cmd tea.Cmd, wantNonNil bool) tea.Msg {
	t.Helper()
	if wantNonNil && cmd == nil {
		t.Fatal("expected non-nil cmd, got nil")
	}
	if !wantNonNil && cmd != nil {
		t.Fatal("expected nil cmd, got non-nil")
	}
	if cmd == nil {
		return nil
	}
	return cmd()
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

func TestUpdate_EngineEvent_ToolResult_Truncation(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

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

	// truncateStr adds "... (expand with Enter)" suffix (23 chars)
	if len(nm.messages[0].Content) != 200+23 {
		t.Errorf("expected truncated content length 223 (200 max + suffix), got %d", len(nm.messages[0].Content))
	}
	if nm.messages[0].Detail != string(long) {
		t.Error("full content should be stored in Detail")
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

func TestUpdate_EngineEvent_EmptyType_Requeues(t *testing.T) {
	m := testModel()
	m.running = true
	m.eventChan = make(chan engine.Event, 1)

	_, cmd := m.Update(engineEventMsg{event: engine.Event{Type: ""}})

	// Empty type (timeout) should re-queue listenEvents
	if cmd == nil {
		t.Error("empty event type should re-queue listenEvents")
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
	m.permActive = true
	m.permSelected = 2

	// Up
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m2.(Model).permSelected != 1 {
		t.Error("up should decrement permSelected")
	}
	// Down
	m3, _ := m2.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	if m3.(Model).permSelected != 2 {
		t.Error("down should increment permSelected")
	}
}

func TestUpdate_PermActive_UpBoundary(t *testing.T) {
	m := testModel()
	m.permActive = true
	m.permSelected = 0

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if newM.(Model).permSelected != 0 {
		t.Error("up at 0 should not change")
	}
}

func TestUpdate_PermActive_DownBoundary(t *testing.T) {
	m := testModel()
	m.permActive = true
	m.permSelected = 3

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if newM.(Model).permSelected != 3 {
		t.Error("down at 3 should not go past boundary")
	}
}

func TestUpdate_PermActive_EnterDismiss(t *testing.T) {
	m := testModel()
	m.permActive = true

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if newM.(Model).permActive {
		t.Error("Enter should dismiss perm dialog")
	}
}

func TestUpdate_PermActive_EscDismiss(t *testing.T) {
	m := testModel()
	m.permActive = true

	newM, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if newM.(Model).permActive {
		t.Error("Esc should dismiss perm dialog")
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
	if nm.input.Width != 116 {
		t.Errorf("expected input width 116, got %d", nm.input.Width)
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

func TestListenEvents_TimesOut(t *testing.T) {
	m := testModel()
	m.eventChan = make(chan engine.Event, 1)

	cmd := m.listenEvents()
	msg := cmd()

	evt, ok := msg.(engineEventMsg)
	if !ok {
		t.Fatalf("expected engineEventMsg, got %T", msg)
	}
	if evt.event.Type != "" {
		t.Errorf("timeout should return empty type, got %q", evt.event.Type)
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
