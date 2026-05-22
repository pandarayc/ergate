package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/raydraw/ergate/internal/tool"
)

const (
	taskCreateSchema = `{
  "type": "object",
  "properties": {
    "description": {"type": "string", "description": "Short description of the task"},
    "command":     {"type": "string", "description": "Bash command to run in background"},
    "kind":        {"type": "string", "enum": ["bash"]}
  },
  "required": ["description", "command"]
}`

	taskOutputSchema = `{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "description": "The task ID to read output from"}
  },
  "required": ["task_id"]
}`

	taskStopSchema = `{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "description": "The task ID to stop"}
  },
  "required": ["task_id"]
}`

	taskListSchema = `{
  "type": "object",
  "properties": {}
}`
)

// CreateTool lets the model spawn background tasks.
type CreateTool struct {
	reg *Registry
}

func NewCreateTool(reg *Registry) *CreateTool { return &CreateTool{reg: reg} }
func (t *CreateTool) Name() string            { return "TaskCreate" }
func (t *CreateTool) Description() string     { return "Create a background task (bash command)." }
func (t *CreateTool) InputSchema() json.RawMessage { return json.RawMessage(taskCreateSchema) }
func (t *CreateTool) IsEnabled() bool         { return true }
func (t *CreateTool) IsReadOnly(input json.RawMessage) bool { return false }
func (t *CreateTool) IsConcurrencySafe() bool { return true }

func (t *CreateTool) ValidateInput(ctx context.Context, input json.RawMessage) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: true}
}

func (t *CreateTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx tool.PermissionContext) tool.PermissionResult {
	return tool.AllowAll(input)
}

func (t *CreateTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tool.ExecContext) (*tool.ToolResult, error) {
	var in struct {
		Description string `json:"description"`
		Command     string `json:"command"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	// Register task
	taskID := t.reg.Register(TypeLocalBash, in.Description)
	t.reg.SetStatus(taskID, StatusRunning)

	// Run command in background
	go func() {
		timeout := 120 * time.Second
		cmdCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		cmd := exec.CommandContext(cmdCtx, "bash", "-c", in.Command)
		if execCtx != nil && execCtx.CWD != "" {
			cmd.Dir = execCtx.CWD
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			if cmdCtx.Err() == context.DeadlineExceeded {
				t.reg.SetStatus(taskID, StatusFailed)
			} else {
				t.reg.SetStatus(taskID, StatusCompleted)
			}
		} else {
			t.reg.SetStatus(taskID, StatusCompleted)
		}

		// Write output to temp file for later retrieval
			os.MkdirAll(".ergate/tasks", 0o700)
		os.WriteFile(".ergate/tasks/"+taskID+".out", output, 0o644)
	}()

	return &tool.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Task started: %s (ID: %s)", in.Description, taskID),
		Metadata: map[string]any{
			"task_id": taskID,
		},
	}, nil
}

// OutputTool lets the model read task output.
type OutputTool struct {
	reg *Registry
}

func NewOutputTool(reg *Registry) *OutputTool { return &OutputTool{reg: reg} }
func (t *OutputTool) Name() string            { return "TaskOutput" }
func (t *OutputTool) Description() string     { return "Get the output of a background task." }
func (t *OutputTool) InputSchema() json.RawMessage { return json.RawMessage(taskOutputSchema) }
func (t *OutputTool) IsEnabled() bool         { return true }
func (t *OutputTool) IsReadOnly(input json.RawMessage) bool { return true }
func (t *OutputTool) IsConcurrencySafe() bool { return true }

func (t *OutputTool) ValidateInput(ctx context.Context, input json.RawMessage) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: true}
}

func (t *OutputTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx tool.PermissionContext) tool.PermissionResult {
	return tool.AllowAll(input)
}

func (t *OutputTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tool.ExecContext) (*tool.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	task, ok := t.reg.Get(in.TaskID)
	if !ok {
		return &tool.ToolResult{Success: false, Content: fmt.Sprintf("Task not found: %s", in.TaskID)}, nil
	}

	// Try to read output file
	data, err := os.ReadFile(".ergate/tasks/" + in.TaskID + ".out")
	content := ""
	if err == nil {
		content = string(data)
	}

	status := fmt.Sprintf("Status: %s", task.Status)
	if content != "" {
		status += "\n\nOutput:\n" + content
	}

	return &tool.ToolResult{
		Success: true,
		Content: status,
		Metadata: map[string]any{
			"task_id": task.ID,
			"task_status": string(task.Status),
		},
	}, nil
}

// StopTool lets the model kill a task.
type StopTool struct {
	reg *Registry
}

func NewStopTool(reg *Registry) *StopTool { return &StopTool{reg: reg} }
func (t *StopTool) Name() string          { return "TaskStop" }
func (t *StopTool) Description() string   { return "Stop a running background task." }
func (t *StopTool) InputSchema() json.RawMessage { return json.RawMessage(taskStopSchema) }
func (t *StopTool) IsEnabled() bool       { return true }
func (t *StopTool) IsReadOnly(input json.RawMessage) bool { return false }
func (t *StopTool) IsConcurrencySafe() bool { return true }

func (t *StopTool) ValidateInput(ctx context.Context, input json.RawMessage) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: true}
}

func (t *StopTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx tool.PermissionContext) tool.PermissionResult {
	return tool.AllowAll(input)
}

func (t *StopTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tool.ExecContext) (*tool.ToolResult, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	task, ok := t.reg.Get(in.TaskID)
	if !ok {
		return &tool.ToolResult{Success: false, Content: fmt.Sprintf("Task not found: %s", in.TaskID)}, nil
	}

	t.reg.SetStatus(in.TaskID, StatusKilled)
	return &tool.ToolResult{
		Success: true,
		Content: fmt.Sprintf("Task %s (%s) stopped.", in.TaskID, task.Description),
	}, nil
}

// ListTool lets the model list all tasks.
type ListTool struct {
	reg *Registry
}

func NewListTool(reg *Registry) *ListTool { return &ListTool{reg: reg} }
func (t *ListTool) Name() string          { return "TaskList" }
func (t *ListTool) Description() string   { return "List all background tasks and their status." }
func (t *ListTool) InputSchema() json.RawMessage { return json.RawMessage(taskListSchema) }
func (t *ListTool) IsEnabled() bool       { return true }
func (t *ListTool) IsReadOnly(input json.RawMessage) bool { return true }
func (t *ListTool) IsConcurrencySafe() bool { return true }

func (t *ListTool) ValidateInput(ctx context.Context, input json.RawMessage) *tool.ValidationResult {
	return &tool.ValidationResult{Valid: true}
}

func (t *ListTool) CheckPermissions(ctx context.Context, input json.RawMessage, permCtx tool.PermissionContext) tool.PermissionResult {
	return tool.AllowAll(input)
}

func (t *ListTool) Execute(ctx context.Context, input json.RawMessage, execCtx *tool.ExecContext) (*tool.ToolResult, error) {
	return &tool.ToolResult{
		Success: true,
		Content: t.reg.FormatList(),
	}, nil
}
