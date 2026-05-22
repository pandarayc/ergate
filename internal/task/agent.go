package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/raydraw/ergate/internal/tool"
)

const agentSchema = `{
  "type": "object",
  "properties": {
    "description": {"type": "string", "description": "Short description of what this sub-agent should do"},
    "prompt":      {"type": "string", "description": "The full prompt/task for the sub-agent"},
    "model":       {"type": "string", "description": "Optional model override"}
  },
  "required": ["description", "prompt"]
}`

// RunSubAgent is a function that runs a sub-agent with limited turns and read-only tools.
type RunSubAgent func(ctx context.Context, prompt, model string, maxTurns int) string

// AgentTool lets the model spawn sub-agents.
type AgentTool struct {
	reg         *Registry
	model       string
	runSubAgent RunSubAgent
}

// NewAgentTool creates a sub-agent tool.
func NewAgentTool(reg *Registry, model string, runSubAgent RunSubAgent) *AgentTool {
	return &AgentTool{reg: reg, model: model, runSubAgent: runSubAgent}
}

func (t *AgentTool) Name() string                { return "Agent" }
func (t *AgentTool) Description() string         { return "Spawn a sub-agent to work on a task in the background. The sub-agent has access to Read/Grep/Glob/WebSearch tools." }
func (t *AgentTool) InputSchema() json.RawMessage { return json.RawMessage(agentSchema) }
func (t *AgentTool) IsEnabled() bool             { return true }
func (t *AgentTool) IsReadOnly(input json.RawMessage) bool { return false }
func (t *AgentTool) IsConcurrencySafe() bool     { return true }

func (t *AgentTool) ValidateInput(ctx context.Context, input json.RawMessage) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: true}
}

func (t *AgentTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx tool.PermissionContext) tool.PermissionResult {
	return tool.AllowAll(input)
}

func (t *AgentTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tool.ExecContext) (*tool.ToolResult, error) {
	var in struct {
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
		Model       string `json:"model"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	model := t.model
	if in.Model != "" {
		model = in.Model
	}

	taskID := t.reg.Register(TypeLocalAgent, in.Description)
	t.reg.SetStatus(taskID, StatusRunning)

	// Run sub-agent in background
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.reg.SetStatus(taskID, StatusFailed)
			}
		}()

		result := t.runAgent(in.Prompt, model)
		os.MkdirAll(".ergate/tasks", 0o700)
			os.WriteFile(".ergate/tasks/"+taskID+".out", []byte(result), 0o644)

		if result != "" {
			t.reg.SetStatus(taskID, StatusCompleted)
		} else {
			t.reg.SetStatus(taskID, StatusFailed)
		}
	}()

	return &tool.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Sub-agent started: %s (ID: %s)", in.Description, taskID),
		Metadata: map[string]any{
			"task_id":     taskID,
			"task_type":   "local_agent",
		},
	}, nil
}

func (t *AgentTool) runAgent(prompt, model string) string {
	if t.runSubAgent == nil {
		return "Sub-agent runner not configured."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	return t.runSubAgent(ctx, prompt, model, 5)
}
