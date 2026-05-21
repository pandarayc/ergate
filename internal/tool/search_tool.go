package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

var toolSearchSchema = Schema(map[string]any{
	"query": map[string]any{"type": "string", "description": "Keywords to search for in tool names and descriptions"},
}, []string{"query"})

// ToolSearchTool lets the model discover tools by keyword.
type ToolSearchTool struct {
	BaseTool
	reg *Registry
}

// NewToolSearchTool creates a ToolSearch tool.
func NewToolSearchTool(reg *Registry) *ToolSearchTool {
	return &ToolSearchTool{
		BaseTool: NewBaseTool(
			"ToolSearch",
			"Search available tools by keyword. Use to discover tools matching a task before loading them.",
			toolSearchSchema,
			WithReadOnly(),
			WithConcurrencySafe(),
		),
		reg: reg,
	}
}

func (t *ToolSearchTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	results := t.reg.Search(in.Query)
	if len(results) == 0 {
		return &ToolResult{Success: true, Content: fmt.Sprintf("No tools found matching %q.", in.Query)}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d tool(s) matching %q:\n\n", len(results), in.Query)
	for _, tool := range results {
		fmt.Fprintf(&b, "- **%s**: %s\n", tool.Name(), tool.Description())
	}
	return &ToolResult{
		Success:  true,
		Content:  b.String(),
		Metadata: map[string]any{"match_count": len(results)},
	}, nil
}
