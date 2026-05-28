package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var todoWriteSchema = Schema(map[string]any{
	"items": map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":     map[string]any{"type": "string", "description": "Unique identifier for this task"},
				"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}, "description": "Current status of the task"},
				"content": map[string]any{"type": "string", "description": "Description of what needs to be done"},
			},
			"required": []string{"id", "status", "content"},
		},
		"description": "The full list of todo items (replaces all existing items)",
	},
}, []string{"items"})

const todoWriteDescription = `Use this tool to create and manage a structured task list for your current coding session. This helps you track progress, organize complex tasks, and demonstrate thoroughness to the user.

## When to Use This Tool

Use this tool proactively in these scenarios:

1. Complex multi-step tasks - When a task requires 3 or more distinct steps or actions
2. Non-trivial and complex tasks - Tasks that require careful planning or multiple operations
3. User explicitly requests todo list - When the user directly asks you to use the todo list
4. User provides multiple tasks - When users provide a list of things to be done (numbered or comma-separated)
5. After receiving new instructions - Immediately capture user requirements as tasks

## When NOT to Use

Skip using this tool when:
- There is only a single, straightforward task
- The task is trivial and tracking it provides no organizational benefit
- The task can be completed in less than 3 trivial steps

## Task States

- pending: Not yet started
- in_progress: Currently working on (ONLY ONE at a time)
- completed: Finished successfully

## Rules

- ONLY ONE task in_progress at a time. Complete or pause it before starting another.
- Pass the FULL list of items each time — this REPLACES all previous items.
- Use "activeForm" for the in_progress item to show a present-continuous form (e.g., "Fixing authentication bug").
- After finishing a task, mark it completed and start the next one.
- If you encounter errors or cannot finish, keep the task as in_progress and create a new task describing the blocker.`

// TodoItem is a single task item.
type TodoItem struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Content    string `json:"content"`
	ActiveForm string `json:"activeForm,omitempty"`
}

// TodoManager holds the current todo state.
type TodoManager struct {
	mu               sync.Mutex
	items            []TodoItem
	roundsSinceTodo  int
}

// NewTodoManager creates a new TodoManager.
func NewTodoManager() *TodoManager {
	return &TodoManager{}
}

// Update replaces all items and resets the reminder counter.
func (m *TodoManager) Update(items []TodoItem) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var inProgressCount int
	for _, item := range items {
		if item.Status == "in_progress" {
			inProgressCount++
		}
	}
	if inProgressCount > 1 {
		return "", fmt.Errorf("only one task can be in_progress at a time, got %d", inProgressCount)
	}

	m.items = make([]TodoItem, len(items))
	copy(m.items, items)
	m.roundsSinceTodo = 0

	return m.renderLocked(), nil
}

// Render formats the current todo list as a string.
func (m *TodoManager) Render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renderLocked()
}

func (m *TodoManager) renderLocked() string {
	if len(m.items) == 0 {
		return "No tasks."
	}

	var b strings.Builder
	b.WriteString("## Task List\n\n")

	// Sort: in_progress first, then pending, then completed
	sorted := make([]TodoItem, len(m.items))
	copy(sorted, m.items)
	sort.Slice(sorted, func(i, j int) bool {
		order := map[string]int{"in_progress": 0, "pending": 1, "completed": 2}
		return order[sorted[i].Status] < order[sorted[j].Status]
	})

	for _, item := range sorted {
		icon := map[string]string{
			"pending":     "  ",
			"in_progress": "▶ ",
			"completed":   "✓ ",
		}[item.Status]
		b.WriteString(fmt.Sprintf("%s[%s] %s\n", icon, item.ID, item.Content))
	}
	return b.String()
}

// ShouldRemind returns true if the model should be nagged to update todos.
func (m *TodoManager) ShouldRemind() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.items) > 0 && m.roundsSinceTodo >= 3
}

// BumpRound increments the turn counter (called each turn start if todo not used).
func (m *TodoManager) BumpRound() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roundsSinceTodo++
}

// ReminderText returns the reminder to inject into tool results.
func (m *TodoManager) ReminderText() string {
	if !m.ShouldRemind() {
		return ""
	}
	return "Consider updating your todo list with current progress."
}

// Items returns a copy of the current items.
func (m *TodoManager) Items() []TodoItem {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]TodoItem, len(m.items))
	copy(items, m.items)
	return items
}

// TodoWriteTool manages the todo list.
type TodoWriteTool struct {
	BaseTool
	mgr *TodoManager
}

// NewTodoWriteTool creates a new TodoWrite tool.
func NewTodoWriteTool(mgr *TodoManager) *TodoWriteTool {
	return &TodoWriteTool{
		BaseTool: NewBaseTool(
			"TodoWrite",
			todoWriteDescription,
			todoWriteSchema,
			WithConcurrencySafe(),
		),
		mgr: mgr,
	}
}

type todoWriteInput struct {
	Items []TodoItem `json:"items"`
}

func (t *TodoWriteTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in todoWriteInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if len(in.Items) == 0 {
		return &ToolResult{Success: false, Content: "items must contain at least one task. Use an empty items list only to clear."}, nil
	}

	rendered, err := t.mgr.Update(in.Items)
	if err != nil {
		return &ToolResult{Success: false, Content: err.Error()}, nil
	}

	return &ToolResult{
		Success: true,
		Content: rendered,
	}, nil
}
