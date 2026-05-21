package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/raydraw/ergate/internal/compact"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/filehistory"
	"github.com/raydraw/ergate/internal/hooks"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/planmode"
	"github.com/raydraw/ergate/internal/memory"
	"github.com/raydraw/ergate/internal/prompt"
	"github.com/raydraw/ergate/internal/skill"
	"github.com/raydraw/ergate/internal/task"
	"github.com/raydraw/ergate/internal/tool"
)

// Event represents something that happened during engine execution.
type Event struct {
	Type EventType
	Data any
	Turn int
}

// EventType indicates the kind of engine event.
type EventType string

const (
	EventText       EventType = "text"
	EventThinking   EventType = "thinking"
	EventToolUse    EventType = "tool_use"
	EventToolResult EventType = "tool_result"
	EventError      EventType = "error"
	EventTurnEnd    EventType = "turn_end"
	EventDone       EventType = "done"
	EventAborted    EventType = "aborted"
)

// Engine is the core query processing loop.
type Engine struct {
	client      llm.LLMClient
	tools       *tool.Registry
	cfg         *config.Config
	logger      *slog.Logger
	permissions tool.PermissionManager

	mu          sync.Mutex
	messages    []llm.Message
	usage       llm.Usage
	memEntries  []memory.Entry
	agentEntry  *memory.Entry
	skillReg    *skill.Registry
	hookMgr     *hooks.Manager
	fileTracker *filehistory.Tracker
	planMgr     *planmode.Manager
	taskNotify  <-chan task.Notification
	todoMgr     *tool.TodoManager
	permCtx      tool.PermissionContext
	transcriptDir string
}

// Context holds the optional subsystems available to the engine.
type Context struct {
	Skills        *skill.Registry
	Hooks         *hooks.Manager
	FileTracker   *filehistory.Tracker
	PlanMgr       *planmode.Manager
	PermCtx       tool.PermissionContext
	TranscriptDir string
	TaskNotify    <-chan task.Notification
	TodoMgr       *tool.TodoManager
	PermMgr       tool.PermissionManager
	Memory        []memory.Entry
	Agent         *memory.Entry
}

// New creates a new Engine.
func New(cfg *config.Config, client llm.LLMClient, tools *tool.Registry, ectx Context) *Engine {
	return &Engine{
		client:        client,
		tools:         tools,
		cfg:           cfg,
		logger:        slog.Default(),
		messages:      make([]llm.Message, 0),
		skillReg:      ectx.Skills,
		hookMgr:       ectx.Hooks,
		fileTracker:   ectx.FileTracker,
		planMgr:       ectx.PlanMgr,
		permCtx:       ectx.PermCtx,
		transcriptDir: ectx.TranscriptDir,
		taskNotify:    ectx.TaskNotify,
		todoMgr:       ectx.TodoMgr,
		permissions:   ectx.PermMgr,
		memEntries:    ectx.Memory,
		agentEntry:    ectx.Agent,
	}
}

// checkPermRules evaluates AlwaysDeny/AlwaysAllow/AlwaysAsk rules for a tool.
func (e *Engine) checkPermRules(toolName string, input json.RawMessage) tool.PermissionBehavior {
	// AlwaysDeny takes highest priority
	for _, rules := range e.permCtx.AlwaysDenyRules {
		for _, rule := range rules {
			if matchPermPattern(toolName, input, rule) {
				return tool.BehaviorDeny
			}
		}
	}
	// AlwaysAsk: must prompt
	for _, rules := range e.permCtx.AlwaysAskRules {
		for _, rule := range rules {
			if matchPermPattern(toolName, input, rule) {
				return tool.BehaviorAsk
			}
		}
	}
	// AlwaysAllow: skip prompt
	for _, rules := range e.permCtx.AlwaysAllowRules {
		for _, rule := range rules {
			if matchPermPattern(toolName, input, rule) {
				return tool.BehaviorAllow
			}
		}
	}
	// Default: ask
	return tool.BehaviorAsk
}

func matchPermPattern(toolName string, input json.RawMessage, rule tool.PermissionRule) bool {
	if rule.ToolName != "" && rule.ToolName != toolName {
		return false
	}
	if rule.Pattern == "" {
		return true
	}
	// Simple substring match on input JSON
	return strings.Contains(string(input), rule.Pattern)
}

// Messages returns a copy of the current conversation history.
func (e *Engine) Messages() []llm.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]llm.Message, len(e.messages))
	copy(result, e.messages)
	return result
}

// Clear resets the conversation history and usage counters.
func (e *Engine) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = make([]llm.Message, 0)
	e.usage = llm.Usage{}
}

// TotalUsage returns accumulated token usage.
func (e *Engine) TotalUsage() (in, out int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage.InputTokens, e.usage.OutputTokens
}

// Run processes a single user input through the query loop.
// Events are sent to the provided channel for UI rendering.
func (e *Engine) Run(ctx context.Context, input string, events chan<- Event) error {
	defer close(events)
	defer e.fireOnStopHook(ctx)
	defer func() {
		if e.transcriptDir != "" {
			e.AutoSave(e.transcriptDir)
		}
	}()

	e.addUserMessage(input)

	for turn := 1; turn <= e.cfg.MaxTurns; turn++ {
		select {
		case <-ctx.Done():
			events <- Event{Type: EventAborted, Data: ctx.Err()}
			return ctx.Err()
		default:
		}

		// Poll for completed tasks
		e.pollTaskNotifications(ctx, events, turn)

		// Auto-compaction check before each API call
		e.maybeCompact(ctx, events, turn)

		req := e.buildRequest()

		// Stream from LLM with retry
		stream, err := llm.RetryWithBackoff(ctx, 3,
			func() (<-chan llm.StreamEvent, error) {
				return e.client.ChatStream(ctx, req)
			},
			func(err error) bool {
				if apiErr, ok := err.(*llm.APIError); ok {
					return apiErr.IsRetryable()
				}
				return false
			},
		)
		if err != nil {
			events <- Event{Type: EventError, Data: fmt.Errorf("API call: %w", err)}
			return fmt.Errorf("chat stream: %w", err)
		}

		// Process streaming events
		var (
			textBuf       strings.Builder
			thinkingBuf   strings.Builder
			toolUseBlocks []llm.ToolUseBlock
			currentTool   *llm.ToolUseBlock
		)

		for event := range stream {
			switch event.Type {
			case llm.EventError:
				events <- Event{Type: EventError, Data: event.Error}
				return event.Error

			case llm.EventMessageStart:
				// Message metadata received, stream starting

			case llm.EventText:
				var textData struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(event.Data, &textData); err == nil {
					textBuf.WriteString(textData.Text)
					events <- Event{Type: EventText, Data: textData.Text, Turn: turn}
				}

			case llm.EventThinking:
				var thinkData struct {
					Thinking string `json:"thinking"`
				}
				if err := json.Unmarshal(event.Data, &thinkData); err == nil {
					thinkingBuf.WriteString(thinkData.Thinking)
					events <- Event{Type: EventThinking, Data: thinkData.Thinking, Turn: turn}
				}

			case llm.EventToolUseStart:
				var toolData struct {
					ID    string `json:"id"`
					Name  string `json:"name"`
					Index int    `json:"index"`
				}
				if err := json.Unmarshal(event.Data, &toolData); err == nil {
					currentTool = &llm.ToolUseBlock{
						ID:   toolData.ID,
						Name: toolData.Name,
					}
				}

			case llm.EventToolUseEnd:
				if currentTool != nil {
					var toolData struct {
						Input json.RawMessage `json:"input"`
					}
					if err := json.Unmarshal(event.Data, &toolData); err == nil && toolData.Input != nil {
						currentTool.Input = toolData.Input
					} else {
						currentTool.Input = json.RawMessage("{}")
					}

					toolUseBlocks = append(toolUseBlocks, *currentTool)

					events <- Event{
						Type: EventToolUse,
						Data: map[string]any{
							"id":    currentTool.ID,
							"name":  currentTool.Name,
							"input": string(currentTool.Input),
						},
						Turn: turn,
					}
					currentTool = nil
				}

			case llm.EventMessageDelta:
				var delta struct {
					Delta struct {
						StopReason string `json:"stop_reason"`
					} `json:"delta"`
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				}
				if err := json.Unmarshal(event.Data, &delta); err == nil {
					e.mu.Lock()
					e.usage.InputTokens += delta.Usage.InputTokens
					e.usage.OutputTokens += delta.Usage.OutputTokens
					e.mu.Unlock()
				}

			case llm.EventDone:
				// Stream complete
			}
		}

		// Build assistant message
		assistantMsg := e.buildAssistantMessage(textBuf.String(), thinkingBuf.String(), toolUseBlocks)

		e.mu.Lock()
		e.messages = append(e.messages, assistantMsg)
		e.mu.Unlock()

		// If no tool_use blocks, we're done
		if len(toolUseBlocks) == 0 {
			events <- Event{Type: EventDone, Data: textBuf.String()}
			return nil
		}

		// Execute tools with permission checks
		e.executeTools(ctx, toolUseBlocks, events, turn)

			// Todo nag reminder: bump counter and check after tool execution
			if e.todoMgr != nil {
				e.todoMgr.BumpRound()
				if e.todoMgr.ShouldRemind() {
					if !e.todoCalledThisTurn(toolUseBlocks) {
						events <- Event{Type: EventThinking, Data: e.todoMgr.ReminderText(), Turn: turn}
					}
				}
			}

		events <- Event{Type: EventTurnEnd, Turn: turn}
	}

	events <- Event{Type: EventDone, Data: "max_turns_reached"}
	return nil
}

func (e *Engine) todoCalledThisTurn(toolUses []llm.ToolUseBlock) bool {
	for _, tu := range toolUses {
		if tu.Name == "TodoWrite" {
			return true
		}
	}
	return false
}

func (e *Engine) buildRequest() *llm.ChatRequest {
	e.mu.Lock()
	messages := make([]llm.Message, len(e.messages))
	copy(messages, e.messages)
	e.mu.Unlock()

	opts := e.cfg.ActiveModelOptions()
	return &llm.ChatRequest{
		Model:          e.cfg.Model,
		System:         e.buildSystemPrompt(),
		Messages:       messages,
		Tools:          e.tools.ToolConfigs(),
		MaxTokens:      e.cfg.MaxTokens,
		Temperature:    e.cfg.Temperature,
		ThinkingBudget: opts.ThinkingBudget,
	}
}

func (e *Engine) buildSystemPrompt() string {
	return prompt.Build(e.promptInput())
}

func (e *Engine) promptInput() prompt.Input {
	in := prompt.Input{
		CWD:         cwd(),
		CurrentDate: time.Now().Format("2006-01-02"),
		Shell:       defaultShell(),
		IsGitRepo:   isGitRepo(),
		InPlanMode:  e.planMgr != nil && e.planMgr.InPlanMode(),
	}

	for _, entry := range e.memEntries {
		in.Memory = append(in.Memory, prompt.MemoryEntry{
			Name:        entry.Name,
			Description: entry.Description,
			Content:     entry.Content,
		})
	}
	if e.agentEntry != nil {
		in.Agent = &prompt.MemoryEntry{
			Name:    e.agentEntry.Name,
			Content: e.agentEntry.Content,
		}
	}
	if e.skillReg != nil {
		for _, name := range e.skillReg.List() {
			if s, ok := e.skillReg.Get(name); ok {
				in.Skills = append(in.Skills, prompt.SkillInfo{
					Name:        s.Name,
					Description: s.Description,
				})
			}
		}
	}

	return in
}

func cwd() string {
	d, _ := os.Getwd()
	return d
}

func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

func isGitRepo() bool {
	_, err := os.Stat(".git")
	return err == nil
}

func (e *Engine) addUserMessage(content string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.messages = append(e.messages, llm.Message{
		Role:    "user",
		Content: []llm.ContentBlock{{Type: "text", Text: content}},
	})
}

func (e *Engine) buildAssistantMessage(text, thinking string, toolUses []llm.ToolUseBlock) llm.Message {
	var blocks []llm.ContentBlock

	if thinking != "" {
		blocks = append(blocks, llm.ContentBlock{Type: "thinking", Thinking: thinking})
	}
	if text != "" {
		blocks = append(blocks, llm.ContentBlock{Type: "text", Text: text})
	}

	for _, tu := range toolUses {
		blocks = append(blocks, llm.ContentBlock{
			Type:  "tool_use",
			ID:    tu.ID,
			Name:  tu.Name,
			Input: tu.Input,
		})
	}

	return llm.Message{
		Role:    "assistant",
		Content: blocks,
	}
}

func (e *Engine) executeTools(ctx context.Context, toolUses []llm.ToolUseBlock, events chan<- Event, turn int) {
	execCtx := &tool.ExecContext{
		CWD:           ".",
		Logger:        e.logger,
		PermissionMgr: e.permissions,
	}

	var resultBlocks []llm.ContentBlock

	for _, tu := range toolUses {
		// Rule-based permission check
		behavior := e.checkPermRules(tu.Name, tu.Input)
		if behavior == tool.BehaviorDeny {
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, fmt.Errorf("permission denied by rule for %s", tu.Name), events, turn))
			continue
		}

		t, ok := e.tools.Get(tu.Name)
		if !ok {
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, fmt.Errorf("unknown tool: %q", tu.Name), events, turn))
			continue
		}

		// Read-only tools skip interactive permission check in headless mode
		if !t.IsReadOnly(tu.Input) && e.permissions != nil && behavior == tool.BehaviorAsk {
			if err := e.permissions.Check(ctx, tu.Name, tu.Input); err != nil {
				resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, fmt.Errorf("permission denied: %w", err), events, turn))
				continue
			}
		}

		// Pre-tool hook
		if !e.firePreToolHook(ctx, tu) {
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, fmt.Errorf("tool blocked by hook"), events, turn))
			continue
		}

		// Plan mode: block write operations
		if e.planMgr != nil && e.planMgr.InPlanMode() && !t.IsReadOnly(tu.Input) && tu.Name != "ExitPlanMode" {
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, fmt.Errorf(
				"plan mode: only read-only tools allowed. Use ExitPlanMode to approve the plan and start implementing."), events, turn))
			continue
		}

		// Save file backup before write operations
		if e.fileTracker != nil && (tu.Name == "Write" || tu.Name == "Edit") {
			var fileInput struct {
				FilePath string `json:"file_path"`
			}
			if json.Unmarshal(tu.Input, &fileInput) == nil && fileInput.FilePath != "" {
				if snap, err := e.fileTracker.SaveBackup(fileInput.FilePath); err == nil {
					events <- Event{Type: EventThinking, Data: fmt.Sprintf("Backup saved: v%d", snap.Version), Turn: turn}
				}
			}
		}

		var result *tool.ToolResult
		var execErr error

		if t.IsReadOnly(tu.Input) {
			// Read-only tools execute directly (no confirmation needed)
			result, execErr = e.safeExecute(ctx, t, tu.Input, execCtx)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, result, execErr, events, turn))
		} else if e.permissions != nil {
			// Write tools need permission
			allowed, err := e.permissions.Prompt(ctx, tu.Name, fmt.Sprintf("Run %s?", tu.Name))
			if err != nil || !allowed {
				resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, fmt.Errorf("user denied permission for %s", tu.Name), events, turn))
				continue
			}
			result, execErr = e.safeExecute(ctx, t, tu.Input, execCtx)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, result, execErr, events, turn))
		} else {
			result, execErr = e.safeExecute(ctx, t, tu.Input, execCtx)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, result, execErr, events, turn))
		}

		// Post-tool hook
		e.firePostToolHook(ctx, tu, result, execErr)

		// Skill auto-triggering: check file paths against conditional skills
		e.checkSkillTriggers(tu, events, turn)
	}

	// Batch all tool results into a single user message (Anthropic API requirement)
	if len(resultBlocks) > 0 {
		e.mu.Lock()
		e.messages = append(e.messages, llm.Message{Role: "user", Content: resultBlocks})
		e.mu.Unlock()
	}
}

	// maybeCompact checks token count and applies compaction if needed.
	func (e *Engine) maybeCompact(ctx context.Context, events chan<- Event, turn int) {
		e.mu.Lock()
		msgCount := len(e.messages)
		e.mu.Unlock()

		// Don't compact early in conversation
		if msgCount < 10 {
			return
		}

		e.mu.Lock()
		messages := make([]llm.Message, len(e.messages))
		copy(messages, e.messages)
		e.mu.Unlock()

		if !compact.ShouldCompact(messages) {
			return
		}

		events <- Event{Type: EventThinking, Data: "Compacting context...", Turn: turn}

		// Step 1: MicroCompact
		messages = compact.MicroCompact(messages)

		// Step 2: If still over threshold, do full compaction
		if compact.ShouldCompact(messages) {
			compacted, err := compact.AutoCompact(ctx, e.client, messages, e.cfg.Model)
			if err != nil {
				events <- Event{Type: EventError, Data: fmt.Errorf("compaction failed: %w", err)}
				return
			}
			messages = compacted
		}

		e.mu.Lock()
		e.messages = messages
		e.mu.Unlock()
	}

	// firePreToolHook runs the pre-tool-use hooks.
	func (e *Engine) firePreToolHook(ctx context.Context, tu llm.ToolUseBlock) bool {
		if e.hookMgr == nil || !e.hookMgr.HasHooks() {
			return true
		}
		result, err := e.hookMgr.Fire(ctx, hooks.PreToolUse, hooks.Data{
			ToolName: tu.Name,
			Input:    tu.Input,
		})
		if err != nil || !result.Continue {
			return false
		}
		return true
	}

	// firePostToolHook runs the post-tool-use hooks.
	func (e *Engine) firePostToolHook(ctx context.Context, tu llm.ToolUseBlock, result *tool.ToolResult, execErr error) {
		if e.hookMgr == nil || !e.hookMgr.HasHooks() {
			return
		}
		output := ""
		isError := false
		if result != nil {
			output = result.Content
			isError = !result.Success
		}
		if execErr != nil {
			output = execErr.Error()
			isError = true
		}
		e.hookMgr.Fire(ctx, hooks.PostToolUse, hooks.Data{
			ToolName: tu.Name,
			Input:    tu.Input,
			Output:   output,
			IsError:  isError,
		})
	}

	// pollTaskNotifications drains the task notification channel and injects system messages.
	func (e *Engine) pollTaskNotifications(ctx context.Context, events chan<- Event, turn int) {
		if e.taskNotify == nil {
			return
		}
		for {
			select {
			case notif, ok := <-e.taskNotify:
				if !ok {
					e.taskNotify = nil
					return
				}
				msg := fmt.Sprintf("Background task [%s] %s (%s): %s", notif.TaskID, notif.Description, notif.Type, notif.Status)
				events <- Event{Type: EventThinking, Data: msg, Turn: turn}
				e.mu.Lock()
				e.messages = append(e.messages, llm.NewSystemMessage(llm.SysInformational, msg, llm.LevelInfo))
				e.mu.Unlock()
			default:
				return
			}
		}
	}

	// checkSkillTriggers checks if tool input references paths that match pending conditional skills.
	func (e *Engine) checkSkillTriggers(tu llm.ToolUseBlock, events chan<- Event, turn int) {
		if e.skillReg == nil {
			return
		}
		// Extract path from input for file-reading tools
		fileTools := map[string]bool{"Read": true, "Edit": true, "Write": true, "Glob": true, "Grep": true}
		if !fileTools[tu.Name] {
			return
		}

		var fileInput struct {
			FilePath string `json:"file_path"`
			Path     string `json:"path"`
			Pattern  string `json:"pattern"`
		}
		if json.Unmarshal(tu.Input, &fileInput) != nil {
			return
		}

		var paths []string
		for _, p := range []string{fileInput.FilePath, fileInput.Path, fileInput.Pattern} {
			if p != "" {
				paths = append(paths, p)
			}
		}
		if len(paths) == 0 {
			return
		}

		activated := e.skillReg.CheckAndActivate(paths)
		for _, s := range activated {
			msg := fmt.Sprintf("Skill auto-activated: %s — %s", s.Name, s.Description)
			events <- Event{Type: EventThinking, Data: msg, Turn: turn}
		}
	}

	// fireOnStopHook fires the OnStop event when the session ends.
	func (e *Engine) fireOnStopHook(ctx context.Context) {
		if e.hookMgr == nil || !e.hookMgr.HasHooks() {
			return
		}
		// Use background context so hooks aren't cancelled by the request context
		e.hookMgr.Fire(context.Background(), hooks.OnStop, hooks.Data{
			ToolName: "session_end",
		})
	}

	// AutoSave writes the current conversation to a transcript file.
	func (e *Engine) AutoSave(dir string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if len(e.messages) == 0 {
			return
		}
		os.MkdirAll(dir, 0o700)
		fname := filepath.Join(dir, fmt.Sprintf("transcript_%d.json", time.Now().Unix()))
		data, _ := json.Marshal(e.messages)
		os.WriteFile(fname, data, 0o644)
	}

	// safeExecute runs tool execution with panic recovery.
	func (e *Engine) safeExecute(ctx context.Context, t tool.Tool, input json.RawMessage, execCtx *tool.ExecContext) (result *tool.ToolResult, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("tool %s panicked: %v\n%s", t.Name(), r, debug.Stack())
			}
		}()
		return t.Execute(ctx, input, execCtx)
	}

	// SessionData is the serializable engine state for persistence.
	type SessionData struct {
		Messages  []llm.Message
		Usage     llm.Usage
		CreatedAt time.Time
	}

	// ExportSession returns a snapshot of the current conversation.
	func (e *Engine) ExportSession() SessionData {
		e.mu.Lock()
		defer e.mu.Unlock()
		msgs := make([]llm.Message, len(e.messages))
		copy(msgs, e.messages)
		return SessionData{
			Messages:  msgs,
			Usage:     e.usage,
			CreatedAt: time.Now(),
		}
	}

	// ImportSession restores a previously saved conversation.
	func (e *Engine) ImportSession(data SessionData) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.messages = make([]llm.Message, len(data.Messages))
		copy(e.messages, data.Messages)
		e.usage = data.Usage
	}

	const maxResultChars = 20_000

	func (e *Engine) handleToolResult(tu llm.ToolUseBlock, result *tool.ToolResult, err error, events chan<- Event, turn int) llm.ContentBlock {
		var content string
		var isError bool

		if err != nil {
			content = fmt.Sprintf("Tool execution failed: %v", err)
			isError = true
		} else if result != nil {
			content = result.Content
			isError = !result.Success
		} else {
			content = "Tool returned no result"
		}

		// Offload large results to disk
		if len(content) > maxResultChars && !isError {
			resultDir := filepath.Join(".ergate", "tool-results")
			os.MkdirAll(resultDir, 0o700)
			fname := filepath.Join(resultDir, fmt.Sprintf("%s_%d.txt", tu.Name, time.Now().UnixNano()))
			if err := os.WriteFile(fname, []byte(content), 0o644); err == nil {
				summary := content[:1000]
				content = fmt.Sprintf(
					"[Tool result saved to %s (%d bytes)]\n\nFirst 1000 chars:\n%s\n\nUse Read with file_path=%q to view the full result or Grep to search it.",
					fname, len(content), summary, fname,
				)
				events <- Event{Type: EventThinking, Data: fmt.Sprintf("Large result offloaded to %s", fname), Turn: turn}
			}
		}

		encoded, _ := json.Marshal(content)
		block := llm.ContentBlock{
			Type:      "tool_result",
			ToolUseID: tu.ID,
			Content:   json.RawMessage(encoded),
			IsError:   isError,
		}

		events <- Event{
			Type: EventToolResult,
			Data: map[string]any{
				"tool_use_id": tu.ID,
				"name":        tu.Name,
				"content":     content,
				"is_error":    isError,
			},
			Turn: turn,
		}

		return block
	}
