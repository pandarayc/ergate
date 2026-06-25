package trace

import (
	"encoding/json"
	"testing"

	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/session"
)

// --- helpers to build test data ---

func makeTextBlock(text string) llm.ContentBlock {
	return llm.ContentBlock{Type: "text", Text: text}
}

func makeToolUseBlock(id, name string, input map[string]any) llm.ContentBlock {
	raw, _ := json.Marshal(input)
	return llm.ContentBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  name,
		Input: raw,
	}
}

func makeUserMsg(text string) llm.Message {
	return llm.NewUserMessage(text)
}

func makeAssistantMsg(model string, blocks ...llm.ContentBlock) llm.Message {
	return llm.NewAssistantMessage("msg-1", model, "end_turn", blocks)
}

func makeToolResultMsg(toolUseID, content string, isError bool) llm.Message {
	return llm.NewToolResultMessage(toolUseID, content, isError)
}

// --- tests ---

func TestExtractSuccess(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("list all go files"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_1", "Bash", map[string]any{"command": "find . -name '*.go'"}),
		),
		makeToolResultMsg("call_1", "main.go\npkg/foo.go\npkg/bar.go", false),
		makeAssistantMsg("claude-sonnet-4-6",
			makeTextBlock("Found 3 Go files: main.go, pkg/foo.go, pkg/bar.go"),
		),
	}

	sess := &session.Session{
		ID:    "test_success",
		Model: "claude-sonnet-4-6",
		Messages: msgs,
		Turns: []session.TurnMetrics{
			{Turn: 1, Model: "claude-sonnet-4-6", TokensIn: 500, TokensOut: 200, ToolsRan: 1, LatencyMS: 3000},
			{Turn: 2, Model: "claude-sonnet-4-6", TokensIn: 800, TokensOut: 100, ToolsRan: 0, LatencyMS: 1500},
		},
	}

	tr := ExtractTaskTrace(sess, "find-go-files", "list all go files")

	if tr.TaskID != "find-go-files" {
		t.Errorf("TaskID: got %q, want %q", tr.TaskID, "find-go-files")
	}
	if tr.TotalTurns != 2 {
		t.Fatalf("TotalTurns: got %d, want 2", tr.TotalTurns)
	}
	if tr.TotalToolsRan != 1 {
		t.Errorf("TotalToolsRan: got %d, want 1", tr.TotalToolsRan)
	}
	if tr.TotalFailures != 0 {
		t.Errorf("TotalFailures: got %d, want 0", tr.TotalFailures)
	}
	if tr.IsFailure() {
		t.Error("expected no failure for a successful task")
	}

	// Check turn 1 — has tool span.
	turn1 := tr.Turns[0]
	if turn1.Turn != 1 {
		t.Errorf("turn1.Turn: got %d, want 1", turn1.Turn)
	}
	if turn1.Model != "claude-sonnet-4-6" {
		t.Errorf("turn1.Model: got %q", turn1.Model)
	}
	if turn1.TokensIn != 500 {
		t.Errorf("turn1.TokensIn: got %d", turn1.TokensIn)
	}
	if len(turn1.Spans) != 1 {
		t.Fatalf("turn1.Spans: got %d, want 1", len(turn1.Spans))
	}
	span := turn1.Spans[0]
	if span.Name != "Bash" {
		t.Errorf("span.Name: got %q, want Bash", span.Name)
	}
	if span.Status != StatusSuccess {
		t.Errorf("span.Status: got %q, want SUCCESS", span.Status)
	}
	if span.Level != LevelDefault {
		t.Errorf("span.Level: got %q, want DEFAULT", span.Level)
	}
	if span.Output != "main.go\npkg/foo.go\npkg/bar.go" {
		t.Errorf("span.Output: got %q", span.Output)
	}

	// Check turn 2 — final response, no tools.
	turn2 := tr.Turns[1]
	if turn2.Turn != 2 {
		t.Errorf("turn2.Turn: got %d, want 2", turn2.Turn)
	}
	if len(turn2.Spans) != 0 {
		t.Errorf("turn2.Spans: got %d, want 0", len(turn2.Spans))
	}
	if turn2.Status != StatusSuccess {
		t.Errorf("turn2.Status: got %q, want SUCCESS", turn2.Status)
	}

	// Check span counts.
	counts := tr.SpanCountByStatus()
	if counts[StatusSuccess] != 1 {
		t.Errorf("span count SUCCESS: got %d, want 1", counts[StatusSuccess])
	}
}

func TestExtractToolError(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("build the project"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_1", "Bash", map[string]any{"command": "make"}),
		),
		makeToolResultMsg("call_1", "make: *** [all] Error 1\ngcc: error: no such file: main.c", true),
	}

	sess := &session.Session{
		ID:       "test_error",
		Model:    "claude-sonnet-4-6",
		Messages: msgs,
		Turns: []session.TurnMetrics{
			{Turn: 1, Model: "claude-sonnet-4-6", TokensIn: 400, TokensOut: 150, ToolsRan: 1, LatencyMS: 5000},
		},
	}

	tr := ExtractTaskTrace(sess, "build-fail", "build the project")

	if !tr.IsFailure() {
		t.Fatal("expected failure for a failed tool execution")
	}
	if tr.TotalFailures == 0 {
		t.Error("TotalFailures should be > 0")
	}
	if tr.PrimaryFailure != "tool_error" {
		t.Errorf("PrimaryFailure: got %q, want tool_error", tr.PrimaryFailure)
	}

	turn1 := tr.Turns[0]
	if turn1.Status != StatusError {
		t.Errorf("turn1.Status: got %q, want ERROR", turn1.Status)
	}
	if turn1.Error == nil {
		t.Fatal("expected turn1.Error to be non-nil")
	}
	if turn1.Error.Kind != FailToolError {
		t.Errorf("turn1.Error.Kind: got %v, want FailToolError", turn1.Error.Kind)
	}
	if turn1.Error.Tool != "Bash" {
		t.Errorf("turn1.Error.Tool: got %q, want Bash", turn1.Error.Tool)
	}

	span := turn1.Spans[0]
	if span.Status != StatusError {
		t.Errorf("span.Status: got %q, want ERROR", span.Status)
	}
	if span.Level != LevelError {
		t.Errorf("span.Level: got %q, want ERROR", span.Level)
	}

	kinds := tr.FailureKinds()
	if len(kinds) != 1 || kinds[0] != FailToolError {
		t.Errorf("FailureKinds: got %v, want [FailToolError]", kinds)
	}
}

func TestExtractTimeout(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("run the long build"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_1", "Bash", map[string]any{"command": "sleep 999"}),
		),
		makeToolResultMsg("call_1", "Command execution failed: context deadline exceeded", true),
	}

	sess := &session.Session{
		ID:       "test_timeout",
		Model:    "claude-sonnet-4-6",
		Messages: msgs,
	}

	tr := ExtractTaskTrace(sess, "timeout-task", "run the long build")

	if !tr.IsFailure() {
		t.Fatal("expected failure for timeout")
	}
	if tr.PrimaryFailure != "tool_timeout" {
		t.Errorf("PrimaryFailure: got %q, want tool_timeout", tr.PrimaryFailure)
	}

	span := tr.Turns[0].Spans[0]
	if span.Status != StatusTimeout {
		t.Errorf("span.Status: got %q, want TIMEOUT", span.Status)
	}
}

func TestExtractStderrWarning(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("check git status"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_1", "Bash", map[string]any{"command": "git status"}),
		),
		makeToolResultMsg("call_1", "On branch main\n[stderr]\nwarning: untracked files", false),
	}

	sess := &session.Session{
		ID:       "test_warning",
		Model:    "claude-sonnet-4-6",
		Messages: msgs,
	}

	tr := ExtractTaskTrace(sess, "stderr-task", "check git status")

	// Warning is not a failure.
	if tr.IsFailure() {
		t.Error("expected no failure for stderr warning")
	}

	span := tr.Turns[0].Spans[0]
	if span.Status != StatusWarning {
		t.Errorf("span.Status: got %q, want WARNING", span.Status)
	}
	if span.Level != LevelWarning {
		t.Errorf("span.Level: got %q, want WARNING", span.Level)
	}
}

func TestExtractPrematureEnd(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("fix the nuclear reactor"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeTextBlock("I cannot fix a nuclear reactor. This task is beyond my capabilities."),
		),
	}

	sess := &session.Session{
		ID:       "test_giveup",
		Model:    "claude-sonnet-4-6",
		Messages: msgs,
	}

	tr := ExtractTaskTrace(sess, "give-up-task", "fix the nuclear reactor")

	if !tr.IsFailure() {
		t.Fatal("expected failure for premature end")
	}
	if tr.PrimaryFailure != "premature_end" {
		t.Errorf("PrimaryFailure: got %q, want premature_end", tr.PrimaryFailure)
	}

	turn1 := tr.Turns[0]
	if turn1.Error == nil {
		t.Fatal("expected turn1.Error")
	}
	if turn1.Error.Kind != FailPrematureEnd {
		t.Errorf("turn1.Error.Kind: got %v, want FailPrematureEnd", turn1.Error.Kind)
	}
}

func TestExtractMultiTurnToolChain(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("debug the failing test"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_1", "Bash", map[string]any{"command": "go test ./..."}),
			makeToolUseBlock("call_2", "Read", map[string]any{"file_path": "foo_test.go"}),
		),
		makeToolResultMsg("call_1", "FAIL: TestFoo (0.00s)\n    foo_test.go:15: expected 42, got 0", false),
		makeToolResultMsg("call_2", "func TestFoo(t *testing.T) {\n    got := Calculate()\n    assert.Equal(t, 42, got)\n}", false),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_3", "Edit", map[string]any{"file_path": "foo.go", "old_string": "return 0", "new_string": "return 42"}),
		),
		makeToolResultMsg("call_3", "Edit applied: foo.go (+1 -1)", false),
		makeAssistantMsg("claude-sonnet-4-6",
			makeTextBlock("I changed the return value from 0 to 42. The test should now pass."),
		),
	}

	sess := &session.Session{
		ID:       "test_multiturn",
		Model:    "claude-sonnet-4-6",
		Messages: msgs,
		Turns: []session.TurnMetrics{
			{Turn: 1, Model: "claude-sonnet-4-6", TokensIn: 600, TokensOut: 300, ToolsRan: 2, LatencyMS: 4000},
			{Turn: 2, Model: "claude-sonnet-4-6", TokensIn: 1200, TokensOut: 200, ToolsRan: 1, LatencyMS: 2500},
			{Turn: 3, Model: "claude-sonnet-4-6", TokensIn: 1500, TokensOut: 100, ToolsRan: 0, LatencyMS: 1000},
		},
	}

	tr := ExtractTaskTrace(sess, "debug-test", "debug the failing test")

	if tr.TotalTurns != 3 {
		t.Fatalf("TotalTurns: got %d, want 3", tr.TotalTurns)
	}
	if tr.TotalToolsRan != 3 {
		t.Errorf("TotalToolsRan: got %d, want 3", tr.TotalToolsRan)
	}
	if tr.IsFailure() {
		t.Error("expected no failure")
	}

	// Turn 1: Bash + Read.
	if len(tr.Turns[0].Spans) != 2 {
		t.Errorf("turn1.Spans: got %d, want 2", len(tr.Turns[0].Spans))
	}
	// Turn 2: Edit.
	if len(tr.Turns[1].Spans) != 1 {
		t.Errorf("turn2.Spans: got %d, want 1", len(tr.Turns[1].Spans))
	}
	// Turn 3: final text.
	if len(tr.Turns[2].Spans) != 0 {
		t.Errorf("turn3.Spans: got %d, want 0", len(tr.Turns[2].Spans))
	}

	counts := tr.SpanCountByStatus()
	if counts[StatusSuccess] != 3 {
		t.Errorf("span count SUCCESS: got %d, want 3", counts[StatusSuccess])
	}
}

func TestExtractEmptySession(t *testing.T) {
	sess := &session.Session{
		ID:       "empty",
		Messages: nil,
	}

	tr := ExtractTaskTrace(sess, "empty-task", "nothing")

	if tr.TotalTurns != 0 {
		t.Errorf("TotalTurns: got %d, want 0", tr.TotalTurns)
	}
	if tr.IsFailure() {
		t.Error("empty session should not be a failure")
	}
}

func TestExtractNilSession(t *testing.T) {
	tr := ExtractTaskTrace(nil, "nil-task", "nothing")
	if tr != nil {
		t.Error("expected nil for nil session")
	}
}

func TestFailureKindString(t *testing.T) {
	tests := []struct {
		kind FailureKind
		want string
	}{
		{FailNone, ""},
		{FailToolError, "tool_error"},
		{FailToolTimeout, "tool_timeout"},
		{FailToolPermission, "tool_permission"},
		{FailUnknownTool, "unknown_tool"},
		{FailPlanModeBlock, "plan_mode_block"},
		{FailHookBlock, "hook_block"},
		{FailPrematureEnd, "premature_end"},
		{FailMaxTurns, "max_turns"},
		{FailAPIError, "api_error"},
		{FailureKind("unknown_kind"), "unknown_kind"},
	}

	for _, tt := range tests {
		got := tt.kind.String()
		if got != tt.want {
			t.Errorf("FailureKind(%q).String(): got %q, want %q", tt.kind, tt.want, got)
		}
	}
}

func TestExtractNonZeroExit(t *testing.T) {
	msgs := []llm.Message{
		makeUserMsg("run tests"),
		makeAssistantMsg("claude-sonnet-4-6",
			makeToolUseBlock("call_1", "Bash", map[string]any{"command": "go test ./..."}),
		),
		makeToolResultMsg("call_1",
			"ok  \texample.com/pkg/a\t0.002s\nFAIL\nexample.com/pkg/b [build failed]\n[stderr]\n# example.com/pkg/b\npkg/b/b.go:3:1: syntax error\n[Exit code: 1]",
			false, // not isError but has non-zero exit
		),
	}

	sess := &session.Session{
		ID:       "test_nz_exit",
		Model:    "claude-sonnet-4-6",
		Messages: msgs,
	}

	tr := ExtractTaskTrace(sess, "nz-exit", "run tests")

	if !tr.IsFailure() {
		t.Fatal("expected failure for non-zero exit code")
	}
	span := tr.Turns[0].Spans[0]
	if span.Status != StatusError {
		t.Errorf("span.Status: got %q, want ERROR for non-zero exit", span.Status)
	}
	if span.Level != LevelError {
		t.Errorf("span.Level: got %q, want ERROR", span.Level)
	}
}

func TestTruncate(t *testing.T) {
	short := truncate("hello", 100)
	if short != "hello" {
		t.Errorf("short: got %q, want %q", short, "hello")
	}

	long := truncate("1234567890", 5)
	if !contains(long, "truncated") {
		t.Errorf("expected truncation marker in %q", long)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("hello\nworld"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
	long := "abcdefghij"
	for range 30 {
		long += "x"
	}
	if got := firstLine(long); len(got) <= len(long)-10 {
		// OK — truncated.
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchSubstring(s, sub)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
