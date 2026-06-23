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

	"github.com/raydraw/ergate/internal/cachestability"
	"github.com/raydraw/ergate/internal/compact"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/filehistory"
	"github.com/raydraw/ergate/internal/hooks"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/log"
	"github.com/raydraw/ergate/internal/memory"
	"github.com/raydraw/ergate/internal/planmode"
	"github.com/raydraw/ergate/internal/prompt"
	"github.com/raydraw/ergate/internal/session"
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
	EventText          EventType = "text"
	EventThinking      EventType = "thinking"
	EventTodoReminder  EventType = "todo_reminder"
	EventToolUse       EventType = "tool_use"
	EventToolResult    EventType = "tool_result"
	EventToolChain     EventType = "tool_chain" // merged tool results for one turn
	EventError         EventType = "error"
	EventTurnEnd       EventType = "turn_end"
	EventDone          EventType = "done"
	EventAborted       EventType = "aborted"
)

// ToolChainItem is a single tool's execution result within a tool chain.
type ToolChainItem struct {
	Name    string `json:"name"`
	Input   string `json:"input"`   // JSON-encoded tool input
	Content string `json:"content"` // full tool output
	IsError bool   `json:"is_error"`
}

// Engine is the core query processing loop.
type Engine struct {
	client      llm.LLMClient
	tools       *tool.Registry
	cfg         *config.Config
	logger      *slog.Logger
	permissions tool.PermissionManager

	mu          sync.Mutex
	log         *log.Log
	usage       llm.Usage
	turns       []session.TurnMetrics
	memEntries  []memory.Entry
	agentEntry  *memory.Entry
	skillReg    *skill.Registry
	hookMgr     *hooks.Manager
	fileTracker *filehistory.Tracker
	planMgr     *planmode.Manager
	taskNotify     <-chan task.Notification
	todoMgr        *tool.TodoManager
	cacheStability          *cachestability.Manager
	openAIDynamicCtxInjected bool // ensures dyn ctx message injected only once for prefix cache
	permCtx                  tool.PermissionContext
	transcriptDir string
	compactFailures int // circuit breaker for AutoCompact
	compactCount     int // number of successful compactions performed
	turnCount        int // total turns across all Run calls, for periodic reminders

	sessionService session.Service
	sessionID      string
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
	PermMgr         tool.PermissionManager
	Memory          []memory.Entry
	Agent           *memory.Entry
	SessionService  session.Service
}

// New creates a new Engine.
func New(cfg *config.Config, client llm.LLMClient, tools *tool.Registry, ectx Context) *Engine {
	return &Engine{
		client:         client,
		tools:          tools,
		cfg:            cfg,
		logger:         slog.Default(),
		log:            log.New(),
		skillReg:       ectx.Skills,
		hookMgr:        ectx.Hooks,
		fileTracker:    ectx.FileTracker,
		planMgr:        ectx.PlanMgr,
		permCtx:        ectx.PermCtx,
		transcriptDir:  ectx.TranscriptDir,
		taskNotify:     ectx.TaskNotify,
		todoMgr:        ectx.TodoMgr,
		permissions:    ectx.PermMgr,
		memEntries:     ectx.Memory,
		agentEntry:     ectx.Agent,
		sessionService: ectx.SessionService,
	}
}

// checkPermRules evaluates AlwaysDeny/AlwaysAllow/AlwaysAsk rules for a tool.
func (e *Engine) checkPermRules(toolName string, input json.RawMessage) tool.PermissionBehavior {
	for _, rules := range e.permCtx.AlwaysDenyRules {
		for _, rule := range rules {
			if matchPermPattern(toolName, input, rule) {
				return tool.BehaviorDeny
			}
		}
	}
	for _, rules := range e.permCtx.AlwaysAskRules {
		for _, rule := range rules {
			if matchPermPattern(toolName, input, rule) {
				return tool.BehaviorAsk
			}
		}
	}
	for _, rules := range e.permCtx.AlwaysAllowRules {
		for _, rule := range rules {
			if matchPermPattern(toolName, input, rule) {
				return tool.BehaviorAllow
			}
		}
	}
	return tool.BehaviorAsk
}

func matchPermPattern(toolName string, input json.RawMessage, rule tool.PermissionRule) bool {
	if rule.ToolName != "" && rule.ToolName != toolName {
		return false
	}
	if rule.Pattern == "" {
		return true
	}
	return strings.Contains(string(input), rule.Pattern)
}

// Messages returns a copy of the current conversation history.
func (e *Engine) Messages() []llm.Message {
	return e.log.Messages()
}

// Clear resets the conversation history and usage counters.
func (e *Engine) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log.Clear()
	e.usage = llm.Usage{}
	e.openAIDynamicCtxInjected = false
}

// TotalUsage returns accumulated token usage.
func (e *Engine) TotalUsage() (in, out int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage.InputTokens, e.usage.OutputTokens
}

// CacheUsage returns accumulated provider cache hit/miss tokens.
func (e *Engine) CacheUsage() (hit, miss int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage.CacheHitTokens, e.usage.CacheMissTokens
}

// TodoItems returns a copy of the current todo list.
func (e *Engine) TodoItems() []tool.TodoItem {
	if e.todoMgr == nil {
		return nil
	}
	return e.todoMgr.Items()
}

// TaskCount returns the number of active background tasks.
func (e *Engine) TaskCount() (running, done int) {
	// taskNotify is a channel from Registry; we don't have direct access.
	// For now return the count from the engine context.
	return 0, 0
}

// CompactCount returns the number of successful compactions performed.
func (e *Engine) CompactCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compactCount
}

// CacheRatio returns the prefix-cache stability ratio (0-100).
// Returns 100 if the stability manager hasn't been initialized yet.
func (e *Engine) CacheRatio() int {
	if e.cacheStability == nil {
		return 100
	}
	return e.cacheStability.RatioPercent()
}

// CacheLastChange returns a description of the last prefix-cache change, or empty if stable.
func (e *Engine) CacheLastChange() string {
	if e.cacheStability == nil {
		return ""
	}
	ch := e.cacheStability.LastChange()
	if ch == nil {
		return ""
	}
	return ch.Description()
}

// Run processes a single user input through the query loop.
func (e *Engine) Run(ctx context.Context, input string, events chan<- Event) error {
	defer close(events)
	defer e.fireOnStopHook(ctx)
	defer func() {
		if e.transcriptDir != "" {
			e.AutoSave(e.transcriptDir)
		}
	}()
	defer e.SaveSession()

	e.addUserMessage(input)

	for turn := 1; turn <= e.cfg.MaxTurns; turn++ {
		select {
		case <-ctx.Done():
			events <- Event{Type: EventAborted, Data: ctx.Err()}
			return ctx.Err()
		default:
		}

		e.pollTaskNotifications(ctx, events, turn)
		e.maybeInjectConstraint()
		e.maybeCompact(ctx, events, turn)

		hasTools, err := e.singleTurn(ctx, events, turn)
		if err != nil {
			return err
		}
		if !hasTools {
			return nil
		}

		if e.todoMgr != nil {
			e.todoMgr.BumpRound()
			if e.todoMgr.ShouldRemind() {
				events <- Event{Type: EventTodoReminder, Data: e.todoMgr.ReminderText(), Turn: turn}
			}
		}

		events <- Event{Type: EventTurnEnd, Turn: turn}
	}

	events <- Event{Type: EventDone, Data: "max_turns_reached"}
	return nil
}

// singleTurn performs one API call + tool execution. Returns true if tools
// were executed (more turns needed), false if the conversation is complete.
func (e *Engine) singleTurn(ctx context.Context, events chan<- Event, turn int) (hasTools bool, err error) {
	req := e.buildRequest()

	// Per-turn metrics
	tm := session.TurnMetrics{
		Turn:      turn,
		Model:     req.Model,
		StartedAt: time.Now(),
	}
	var ttftRecorded bool

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
		tm.LatencyMS = time.Since(tm.StartedAt).Milliseconds()
		e.mu.Lock()
		e.turns = append(e.turns, tm)
		e.mu.Unlock()
		events <- Event{Type: EventError, Data: fmt.Errorf("API call: %w", err)}
		return false, fmt.Errorf("chat stream: %w", err)
	}

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
			return false, event.Error

		case llm.EventText:
			if !ttftRecorded {
				tm.TTFTMS = time.Since(tm.StartedAt).Milliseconds()
				ttftRecorded = true
			}
			var textData struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(event.Data, &textData); err == nil {
				textBuf.WriteString(textData.Text)
				events <- Event{Type: EventText, Data: textData.Text, Turn: turn}
			}

		case llm.EventThinking:
			if !ttftRecorded {
				tm.TTFTMS = time.Since(tm.StartedAt).Milliseconds()
				ttftRecorded = true
			}
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
					InputTokens        int `json:"input_tokens"`
					OutputTokens       int `json:"output_tokens"`
					CacheHitTokens     int `json:"prompt_cache_hit_tokens"`
					CacheMissTokens    int `json:"prompt_cache_miss_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(event.Data, &delta); err == nil {
				tm.TokensIn += delta.Usage.InputTokens
				tm.TokensOut += delta.Usage.OutputTokens
				tm.CacheHitTokens += delta.Usage.CacheHitTokens
				tm.CacheMissTokens += delta.Usage.CacheMissTokens
				e.mu.Lock()
				e.usage.InputTokens += delta.Usage.InputTokens
				e.usage.OutputTokens += delta.Usage.OutputTokens
				e.usage.CacheHitTokens += delta.Usage.CacheHitTokens
				e.usage.CacheMissTokens += delta.Usage.CacheMissTokens
				e.mu.Unlock()
			}

		case llm.EventDone:
		}
	}

	assistantMsg := e.buildAssistantMessage(textBuf.String(), thinkingBuf.String(), toolUseBlocks)
	e.mu.Lock()
	e.log.Append(assistantMsg)
	e.mu.Unlock()

	if len(toolUseBlocks) == 0 {
		tm.LatencyMS = time.Since(tm.StartedAt).Milliseconds()
		e.mu.Lock()
		e.turns = append(e.turns, tm)
		e.mu.Unlock()
		events <- Event{Type: EventDone, Data: textBuf.String()}
		return false, nil
	}

	tm.ToolsRan = len(toolUseBlocks)
	e.executeTools(ctx, toolUseBlocks, events, turn)
	tm.LatencyMS = time.Since(tm.StartedAt).Milliseconds()
	e.mu.Lock()
	e.turns = append(e.turns, tm)
	e.mu.Unlock()
	return true, nil
}

func (e *Engine) buildRequest() *llm.ChatRequest {
	e.mu.Lock()
	messages := e.log.Messages()
	e.mu.Unlock()

	pin := e.promptInput()
	sysPrompt := prompt.Build(pin)
	toolNames := e.tools.ToolNames()

	// For OpenAI-compatible providers (DeepSeek), split the system prompt
	// into a stable cacheable prefix and a dynamic context injected as the
	// first user message. This keeps the system message byte-identical
	// between turns, which is critical for automatic prefix caching.
	pc := e.cfg.ActiveProviderConfig()
	if pc.Compat == config.CompatOpenAI {
		// Stable system prompt for automatic prefix caching.
		// Dynamic context (env, skills, plan mode) is injected as a
		// persistent first user message — done once, then stays in history.
		sysPrompt = prompt.BuildStable(pin)
		if !e.openAIDynamicCtxInjected {
			if dyn := prompt.BuildDynamicContext(pin); dyn != "" {
				dynMsg := llm.NewUserMessage(dyn)
				messages = append([]llm.Message{dynMsg}, messages...)
			}
			e.openAIDynamicCtxInjected = true
		}
	}

	// Prefix cache stability check (first call initializes, subsequent calls compare).
	if e.cacheStability == nil {
		e.cacheStability = cachestability.New(sysPrompt, toolNames)
	} else if ch := e.cacheStability.Check(sysPrompt, toolNames); ch != nil {
		// Cache busted; could emit event here if needed.
		_ = ch
	}

	opts := e.cfg.ActiveModelOptions()
	return &llm.ChatRequest{
		Model:          e.cfg.Model,
		System:         sysPrompt,
		Messages:       messages,
		Tools:          e.tools.ToolConfigs(),
		MaxTokens:      e.cfg.MaxTokens,
		Temperature:    e.cfg.Temperature,
		ThinkingBudget: opts.ThinkingBudget,
	}
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
	e.log.Append(llm.Message{
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
	var chainItems []ToolChainItem

	for _, tu := range toolUses {
		behavior := e.checkPermRules(tu.Name, tu.Input)
		if behavior == tool.BehaviorDeny {
			err := fmt.Errorf("permission denied by rule for %s", tu.Name)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, err))
			chainItems = append(chainItems, e.makeToolChainItem(tu, nil, err))
			continue
		}

		t, ok := e.tools.Get(tu.Name)
		if !ok {
			err := fmt.Errorf("unknown tool: %q", tu.Name)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, err))
			chainItems = append(chainItems, e.makeToolChainItem(tu, nil, err))
			continue
		}

		if !t.IsReadOnly(tu.Input) && e.permissions != nil && behavior == tool.BehaviorAsk {
			if err := e.permissions.Check(ctx, tu.Name, tu.Input); err != nil {
				err := fmt.Errorf("permission denied: %w", err)
				resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, err))
				chainItems = append(chainItems, e.makeToolChainItem(tu, nil, err))
				continue
			}
		}

		if !e.firePreToolHook(ctx, tu) {
			err := fmt.Errorf("tool blocked by hook")
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, err))
			chainItems = append(chainItems, e.makeToolChainItem(tu, nil, err))
			continue
		}

		if e.planMgr != nil && e.planMgr.InPlanMode() && !t.IsReadOnly(tu.Input) && tu.Name != "ExitPlanMode" {
			err := fmt.Errorf(
				"plan mode: only read-only tools allowed. Use ExitPlanMode to approve the plan and start implementing.")
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, err))
			chainItems = append(chainItems, e.makeToolChainItem(tu, nil, err))
			continue
		}

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
			result, execErr = e.safeExecute(ctx, t, tu.Input, execCtx)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, result, execErr))
			chainItems = append(chainItems, e.makeToolChainItem(tu, result, execErr))
		} else if e.permissions != nil {
			allowed, err := e.permissions.Prompt(ctx, tu.Name, fmt.Sprintf("Run %s?", tu.Name))
			if err != nil || !allowed {
				err := fmt.Errorf("user denied permission for %s", tu.Name)
				resultBlocks = append(resultBlocks, e.handleToolResult(tu, nil, err))
				chainItems = append(chainItems, e.makeToolChainItem(tu, nil, err))
				continue
			}
			result, execErr = e.safeExecute(ctx, t, tu.Input, execCtx)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, result, execErr))
			chainItems = append(chainItems, e.makeToolChainItem(tu, result, execErr))
		} else {
			result, execErr = e.safeExecute(ctx, t, tu.Input, execCtx)
			resultBlocks = append(resultBlocks, e.handleToolResult(tu, result, execErr))
			chainItems = append(chainItems, e.makeToolChainItem(tu, result, execErr))
		}

		e.firePostToolHook(ctx, tu, result, execErr)
		e.checkSkillTriggers(tu, events, turn)
	}

	if len(resultBlocks) > 0 {
		e.mu.Lock()
		e.log.Append(llm.Message{Role: "user", Content: resultBlocks})
		e.mu.Unlock()
	}

	if len(chainItems) > 0 {
		// JSON-serialise to avoid cross-package type assertion issues
		// ([]ToolChainItem cannot be asserted as []interface{} in message package).
		itemsJSON, _ := json.Marshal(chainItems)
		events <- Event{
			Type: EventToolChain,
			Data: map[string]any{
				"items": string(itemsJSON),
			},
			Turn: turn,
		}
	}
}

// makeToolChainItem creates a ToolChainItem from a tool execution result.
func (e *Engine) makeToolChainItem(tu llm.ToolUseBlock, result *tool.ToolResult, err error) ToolChainItem {
	item := ToolChainItem{
		Name:  tu.Name,
		Input: string(tu.Input),
	}
	if err != nil {
		item.Content = fmt.Sprintf("Error: %v", err)
		item.IsError = true
	} else if result != nil {
		item.Content = result.Content
		item.IsError = !result.Success
	}
	return item
}

// maybeInjectConstraint periodically injects a constraint reminder to
// prevent rule amnesia during long sessions. Following ReCAP's pattern of
// re-injecting rules every ~10 LLM invocations.
func (e *Engine) maybeInjectConstraint() {
	interval := e.cfg.CompactConstraintInterval
	if interval <= 0 {
		return
	}
	e.turnCount++
	if e.turnCount%interval != 0 {
		return
	}

	// Build a lightweight, deterministic reminder.
	reminder := fmt.Sprintf(
		"[Constraint reminder — turn %d] Working directory: %s. "+
			"Use the available tools. Do not modify files outside the workspace.",
		e.turnCount, cwd(),
	)
	e.log.Append(llm.NewSystemMessage(llm.SysInformational, reminder, llm.LevelInfo))
}

func (e *Engine) maybeCompact(ctx context.Context, events chan<- Event, turn int) {
	e.mu.Lock()
	msgCount := e.log.Len()
	e.mu.Unlock()

	if msgCount < 10 {
		return
	}

	// Circuit breaker: stop retrying after N consecutive AutoCompact failures.
	if e.compactFailures >= compact.MaxConsecutiveFailures {
		return
	}

	// Threshold: config overrides the default 0.8 fraction of context window.
	ratio := e.cfg.CompactThreshold
	if ratio <= 0 {
		ratio = 0.8
	}
	keepTail := e.cfg.CompactKeepTail
	if keepTail <= 0 {
		keepTail = 3
	}
	threshold := 0
	opts := e.cfg.ActiveModelOptions()
	if opts.ContextWindow > 0 {
		threshold = int(float64(opts.ContextWindow) * ratio)
	}

	e.mu.Lock()
	messages := e.log.Messages()
	e.mu.Unlock()

	if !compact.ShouldCompact(messages, threshold) {
		// No compaction needed — don't touch messages at all.
		// Preserving message identity is critical for prefix-cache stability.
		return
	}

	// Layer 1: SnipCompact — clear old thinking/reasoning content.
	if _, saved := compact.SnipCompact(messages); saved > 0 {
		e.logger.Debug("snip compact freed tokens", "tokens", saved)
	}

	// Layer 2: PruneCompact — archive large tool results to disk,
	// replacing them with short pointers. Reduces summarization input size.
	pruneBytes := e.cfg.CompactPruneBytes
	if pruneBytes <= 0 {
		pruneBytes = 4096
	}
	if _, saved := compact.PruneCompact(messages, pruneBytes); saved > 0 {
		e.logger.Debug("prune compact freed bytes", "bytes", saved)
	}

	// Layer 3: FoldCompact — LLM summarization with tail preservation.
	// Keeps the most recent messages intact for context continuity.
	events <- Event{Type: EventThinking, Data: "Compacting context...", Turn: turn}

	compacted, err := compact.FoldCompact(ctx, e.client, messages, e.cfg.Model, keepTail)
	if err != nil {
		e.compactFailures++
		events <- Event{Type: EventError, Data: fmt.Errorf("compaction failed (%d/%d): %w",
			e.compactFailures, compact.MaxConsecutiveFailures, err)}
		return
	}
	messages = compacted
	e.compactFailures = 0 // reset on success
	e.compactCount++

	e.log.Import(messages)
}

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
			e.log.Append(llm.NewSystemMessage(llm.SysInformational, msg, llm.LevelInfo))
			e.mu.Unlock()
		default:
			return
		}
	}
}

func (e *Engine) checkSkillTriggers(tu llm.ToolUseBlock, events chan<- Event, turn int) {
	if e.skillReg == nil {
		return
	}
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

func (e *Engine) fireOnStopHook(ctx context.Context) {
	if e.hookMgr == nil || !e.hookMgr.HasHooks() {
		return
	}
	e.hookMgr.Fire(context.Background(), hooks.OnStop, hooks.Data{
		ToolName: "session_end",
	})
}

// AutoSave writes the current conversation to a transcript file.
func (e *Engine) AutoSave(dir string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.log.Len() == 0 {
		return
	}
	os.MkdirAll(dir, 0o700)
	fname := filepath.Join(dir, fmt.Sprintf("transcript_%d.json", time.Now().Unix()))
	data, _ := json.Marshal(e.log.Messages())
	os.WriteFile(fname, data, 0o644)
}

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
	Messages  []llm.Message         `json:"messages"`
	Usage     llm.Usage             `json:"usage"`
	Turns     []session.TurnMetrics `json:"turns,omitempty"`
	CreatedAt time.Time             `json:"-"`
}

// ExportSession returns a snapshot of the current conversation.
func (e *Engine) ExportSession() SessionData {
	e.mu.Lock()
	defer e.mu.Unlock()
	msgs := e.log.Export()
	turns := make([]session.TurnMetrics, len(e.turns))
	copy(turns, e.turns)
	return SessionData{
		Messages:  msgs,
		Usage:     e.usage,
		Turns:     turns,
		CreatedAt: time.Now(),
	}
}

// ImportSession restores a previously saved conversation.
func (e *Engine) ImportSession(data SessionData) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.log.Import(data.Messages)
	e.usage = data.Usage
	e.turns = make([]session.TurnMetrics, len(data.Turns))
	copy(e.turns, data.Turns)

	// Detect whether the imported session already has the dynamic context
	// injected as a user message (for OpenAI prefix cache stability).
	msgs := e.log.Messages()
	if len(msgs) > 0 && msgs[0].Role == "user" {
		for _, b := range msgs[0].Content {
			if b.Type == "text" && strings.Contains(b.Text, "## Environment") {
				e.openAIDynamicCtxInjected = true
				break
			}
		}
	}
}

// TurnMetrics returns a copy of per-turn metrics.
func (e *Engine) TurnMetrics() []session.TurnMetrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]session.TurnMetrics, len(e.turns))
	copy(out, e.turns)
	return out
}

// --- Session management ---

// SessionInfo is a lightweight view of a session for listing.
type SessionInfo struct {
	ID           string
	UpdatedAt    time.Time
	MessageCount int
	Model        string
}

// SetSessionID sets the current session ID.
func (e *Engine) SetSessionID(id string) {
	e.sessionID = id
}

// SessionID returns the current session ID.
func (e *Engine) SessionID() string {
	return e.sessionID
}

// LoadSession loads a session from the service and restores engine state.
func (e *Engine) LoadSession(id string) error {
	if e.sessionService == nil {
		return fmt.Errorf("session service not configured")
	}
	resp, err := e.sessionService.Get(context.Background(), &session.GetRequest{SessionID: id})
	if err != nil {
		return fmt.Errorf("load session %s: %w", id, err)
	}
	if resp == nil || resp.Session == nil {
		return fmt.Errorf("session %s not found", id)
	}
	sess := resp.Session
	e.ImportSession(SessionData{
		Messages: sess.Messages,
		Usage:    sess.Usage,
		Turns:    sess.Turns,
	})
	e.sessionID = sess.ID
	return nil
}

// ListSessions returns all sessions from the service.
func (e *Engine) ListSessions() ([]SessionInfo, error) {
	if e.sessionService == nil {
		return nil, nil
	}
	resp, err := e.sessionService.List(context.Background(), &session.ListRequest{})
	if err != nil {
		return nil, err
	}
	infos := make([]SessionInfo, len(resp.Sessions))
	for i, s := range resp.Sessions {
		infos[i] = SessionInfo{
			ID:           s.ID,
			UpdatedAt:    s.UpdatedAt,
			MessageCount: len(s.Messages),
			Model:        s.Model,
		}
	}
	return infos, nil
}

// DeleteSession removes a session from the service.
func (e *Engine) DeleteSession(id string) error {
	if e.sessionService == nil {
		return nil
	}
	return e.sessionService.Delete(context.Background(), &session.DeleteRequest{SessionID: id})
}

// HasSessions returns true if any sessions exist.
func (e *Engine) HasSessions() bool {
	infos, err := e.ListSessions()
	if err != nil {
		return false
	}
	return len(infos) > 0
}

// SaveSession persists the current engine state to the session service.
func (e *Engine) SaveSession() {
	if e.sessionService == nil {
		return
	}
	// Generate session ID on first save.
	if e.sessionID == "" {
		e.sessionID = fmt.Sprintf("session_%d", time.Now().Unix())
	}
	data := e.ExportSession()
	sess := &session.Session{
		ID:       e.sessionID,
		Model:    e.cfg.Model,
		Messages: data.Messages,
		Usage:    data.Usage,
		Turns:    data.Turns,
	}
	_ = e.sessionService.Save(context.Background(), sess)
	e.sessionService.Prune(context.Background(), 20)
}

const maxResultChars = 20_000

func (e *Engine) handleToolResult(tu llm.ToolUseBlock, result *tool.ToolResult, err error) llm.ContentBlock {
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
		}
	}

	encoded, _ := json.Marshal(content)
	block := llm.ContentBlock{
		Type:      "tool_result",
		ToolUseID: tu.ID,
		Content:   json.RawMessage(encoded),
		IsError:   isError,
	}

	return block
}

// RunSubAgent runs a limited-turn sub-agent with only read-only tools.
func (e *Engine) RunSubAgent(ctx context.Context, prompt, model string, maxTurns int, events chan<- Event) error {
	defer close(events)

	originalTools := e.tools
	originalModel := e.cfg.Model
	originalMaxTurns := e.cfg.MaxTurns

	e.tools = e.readOnlyToolRegistry()
	if model != "" {
		e.cfg.Model = model
	}
	e.cfg.MaxTurns = maxTurns

	e.addUserMessage(prompt)

	for turn := 1; turn <= maxTurns; turn++ {
		select {
		case <-ctx.Done():
			e.restoreSubAgent(originalTools, originalModel, originalMaxTurns)
			events <- Event{Type: EventAborted, Data: ctx.Err()}
			return ctx.Err()
		default:
		}

		hasTools, err := e.singleTurn(ctx, events, turn)
		if err != nil {
			e.restoreSubAgent(originalTools, originalModel, originalMaxTurns)
			return err
		}
		if !hasTools {
			e.restoreSubAgent(originalTools, originalModel, originalMaxTurns)
			return nil
		}

		events <- Event{Type: EventTurnEnd, Turn: turn}
	}

	e.restoreSubAgent(originalTools, originalModel, originalMaxTurns)
	events <- Event{Type: EventDone, Data: "max_turns_reached"}
	return nil
}

func (e *Engine) restoreSubAgent(tools *tool.Registry, model string, maxTurns int) {
	e.tools = tools
	e.cfg.Model = model
	e.cfg.MaxTurns = maxTurns
}

// readOnlyToolRegistry returns a registry containing only read-only tools.
func (e *Engine) readOnlyToolRegistry() *tool.Registry {
	sub := tool.NewRegistry()
	readOnly := map[string]bool{
		"Read": true, "Grep": true, "Glob": true,
		"WebSearch": true, "WebFetch": true, "ToolSearch": true,
		"TodoWrite": true,
	}
	for _, name := range e.tools.ToolNames() {
		if readOnly[name] {
			if t, ok := e.tools.Get(name); ok {
				sub.RegisterRaw(t)
			}
		}
	}
	return sub
}
