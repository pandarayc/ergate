package tool

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"
)

// Tool is the interface every tool must implement.
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Execute(ctx context.Context, input json.RawMessage, exec *ExecContext) (*ToolResult, error)
	IsEnabled() bool
	IsReadOnly(input json.RawMessage) bool
	IsConcurrencySafe() bool

	// ValidateInput runs pre-execution validation. Returns true if valid.
	// Runs before CheckPermissions.
	ValidateInput(ctx context.Context, input json.RawMessage) *ValidationResult

	// CheckPermissions determines whether the tool call is allowed.
	CheckPermissions(ctx context.Context, input json.RawMessage, permCtx PermissionContext) PermissionResult
}

// ExecContext provides execution context to tools.
type ExecContext struct {
	CWD           string
	Logger        *slog.Logger
	PermissionMgr PermissionManager
}

// ToolResult holds the result of a tool execution.
type ToolResult struct {
	Success  bool
	Content  string
	Metadata map[string]any
}

// PermissionManager decides whether a tool action is permitted.
type PermissionManager interface {
	Check(ctx context.Context, toolName string, input json.RawMessage) error
	Prompt(ctx context.Context, toolName string, summary string) (bool, error)
}

// ValidationResult is returned by ValidateInput.
type ValidationResult struct {
	Valid   bool
	Message string
}

// PermissionBehavior describes what to do with a tool call.
type PermissionBehavior string

const (
	BehaviorAllow PermissionBehavior = "allow"
	BehaviorDeny  PermissionBehavior = "deny"
	BehaviorAsk   PermissionBehavior = "ask"
)

// PermissionMode is the high-level permission policy.
type PermissionMode string

const (
	PermModeAcceptEdits       PermissionMode = "acceptEdits"
	PermModeBypassPermissions PermissionMode = "bypassPermissions"
	PermModeDefault           PermissionMode = "default"
	PermModeDontAsk           PermissionMode = "dontAsk"
	PermModePlan              PermissionMode = "plan"
	PermModeAuto              PermissionMode = "auto"
)

// PermissionRule is a pattern-based rule for a specific tool.
type PermissionRule struct {
	ToolName string
	Pattern  string
}

// PermissionContext carries the current permission state.
type PermissionContext struct {
	Mode                        PermissionMode
	AlwaysAllowRules            map[string][]PermissionRule
	AlwaysDenyRules             map[string][]PermissionRule
	AlwaysAskRules              map[string][]PermissionRule
	ShouldAvoidPermissionPrompts bool
}

// PermissionResult is returned by CheckPermissions.
type PermissionResult struct {
	Behavior       PermissionBehavior
	UpdatedInput   json.RawMessage
}

// AllowAll returns PermissionResult that allows execution.
func AllowAll(input json.RawMessage) PermissionResult {
	return PermissionResult{Behavior: BehaviorAllow, UpdatedInput: input}
}

// Deny returns PermissionResult that denies execution.
func Deny(reason string) PermissionResult {
	return PermissionResult{Behavior: BehaviorDeny}
}

// BaseTool provides a partial implementation of Tool with safe defaults.
type BaseTool struct {
	name           string
	description    string
	schema         json.RawMessage
	readOnly       bool
	concurrencySafe bool
	enabled        bool
}

// NewBaseTool creates a new BaseTool with given configuration.
func NewBaseTool(name, description string, schema json.RawMessage, opts ...ToolOption) BaseTool {
	bt := BaseTool{
		name:           name,
		description:    description,
		schema:         schema,
		enabled:        true,
		concurrencySafe: false,
		readOnly:       false,
	}
	for _, opt := range opts {
		opt(&bt)
	}
	return bt
}

// ToolOption configures a BaseTool.
type ToolOption func(*BaseTool)

// WithReadOnly marks the tool as read-only.
func WithReadOnly() ToolOption {
	return func(bt *BaseTool) {
		bt.readOnly = true
	}
}

// WithConcurrencySafe marks the tool as safe for concurrent execution.
func WithConcurrencySafe() ToolOption {
	return func(bt *BaseTool) {
		bt.concurrencySafe = true
	}
}

// WithDisabled marks the tool as disabled.
func WithDisabled() ToolOption {
	return func(bt *BaseTool) {
		bt.enabled = false
	}
}

func (b BaseTool) Name() string                      { return b.name }
func (b BaseTool) Description() string               { return b.description }
func (b BaseTool) InputSchema() json.RawMessage      { return b.schema }
func (b BaseTool) IsEnabled() bool                   { return b.enabled }
func (b BaseTool) IsReadOnly(json.RawMessage) bool   { return b.readOnly }
func (b BaseTool) IsConcurrencySafe() bool            { return b.concurrencySafe }
func (b BaseTool) ValidateInput(ctx context.Context, input json.RawMessage) *ValidationResult {
	return &ValidationResult{Valid: true}
}
func (b BaseTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx PermissionContext) PermissionResult {
	return PermissionResult{Behavior: BehaviorAllow, UpdatedInput: input}
}

// BuildToolFrom applies defaults and returns a complete Tool.
// Use this when you don't want to embed BaseTool.
func BuildToolFrom(def ToolDef) Tool {
	return &builtTool{def: def}
}

// ToolDef holds the required fields for building a tool.
type ToolDef struct {
	Name             string
	Description      string
	InputSchema      json.RawMessage
	Execute          func(ctx context.Context, input json.RawMessage, exec *ExecContext) (*ToolResult, error)
	IsReadOnly       func(input json.RawMessage) bool
	ValidateInput    func(ctx context.Context, input json.RawMessage) *ValidationResult
	CheckPermissions func(ctx context.Context, input json.RawMessage, permCtx PermissionContext) PermissionResult
}

type builtTool struct {
	def ToolDef
}

func (t *builtTool) Name() string             { return t.def.Name }
func (t *builtTool) Description() string      { return t.def.Description }
func (t *builtTool) InputSchema() json.RawMessage { return t.def.InputSchema }
func (t *builtTool) Execute(ctx context.Context, input json.RawMessage, exec *ExecContext) (*ToolResult, error) {
	return t.def.Execute(ctx, input, exec)
}
func (t *builtTool) IsEnabled() bool { return true }
func (t *builtTool) IsConcurrencySafe() bool { return false }
func (t *builtTool) IsReadOnly(input json.RawMessage) bool {
	if t.def.IsReadOnly != nil {
		return t.def.IsReadOnly(input)
	}
	return false
}
func (t *builtTool) ValidateInput(ctx context.Context, input json.RawMessage) *ValidationResult {
	if t.def.ValidateInput != nil {
		return t.def.ValidateInput(ctx, input)
	}
	return &ValidationResult{Valid: true}
}
func (t *builtTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx PermissionContext) PermissionResult {
	if t.def.CheckPermissions != nil {
		return t.def.CheckPermissions(ctx, input, permCtx)
	}
	return PermissionResult{Behavior: BehaviorAllow, UpdatedInput: input}
}

// Schema builds a JSON Schema from properties and required fields.
// json.Marshal produces sorted keys for deterministic output.
func Schema(props map[string]any, required []string) json.RawMessage {
	sort.Strings(required)
	schema := map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
	b, _ := json.Marshal(schema)
	return json.RawMessage(b)
}
