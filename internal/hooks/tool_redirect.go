package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolRedirect detects when the model calls Bash with JSON params that
// belong to another tool (e.g. {"file_path": "..."} → should be Read).
// It injects a correction message so the model learns the right tool.
type ToolRedirect struct{}

// NewToolRedirect creates a hook that catches Bash param confusion.
func NewToolRedirect() *ToolRedirect { return &ToolRedirect{} }

func (t *ToolRedirect) Name() string { return "tool_redirect" }

func (t *ToolRedirect) Run(ctx context.Context, event Event, data Data) (Result, error) {
	if event != PreToolUse || data.ToolName != "Bash" {
		return Result{Continue: true}, nil
	}

	input := data.Input
	if len(input) == 0 {
		return Result{Continue: true}, nil
	}

	// Only intervene when the input is JSON — legitimate Bash commands are plain strings.
	if !strings.HasPrefix(strings.TrimSpace(string(input)), "{") {
		return Result{Continue: true}, nil
	}

	var parsed map[string]any
	if err := json.Unmarshal(input, &parsed); err != nil {
		return Result{Continue: true}, nil
	}

	// Detect which tool the params belong to.
	correctTool := ""
	switch {
	case parsed["file_path"] != nil:
		correctTool = "Read"
	case parsed["old_string"] != nil:
		correctTool = "Edit"
	case parsed["pattern"] != nil && parsed["path"] != nil:
		correctTool = "Glob"
	case parsed["pattern"] != nil:
		correctTool = "Grep"
	case parsed["content"] != nil:
		correctTool = "Write"
	case parsed["query"] != nil:
		correctTool = "WebSearch"
	case parsed["url"] != nil:
		correctTool = "WebFetch"
	}

	if correctTool == "" {
		return Result{Continue: true}, nil
	}

	return Result{
		Continue: false,
		Message: fmt.Sprintf(
			"You called Bash with %s parameters. Use the %s tool instead — Bash expects a shell command string, not JSON.",
			correctTool, correctTool,
		),
	}, nil
}
