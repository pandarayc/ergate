package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"path/filepath"
)

var writeSchema = Schema(map[string]any{
	"file_path": map[string]any{"type": "string", "description": "The absolute path to the file to write (must be absolute, not relative)"},
	"content":   map[string]any{"type": "string", "description": "The content to write to the file"},
}, []string{"file_path", "content"})

const writeDescription = `Writes a file to the local filesystem. Creates parent directories automatically. Returns file path, line count, size, and first-line preview. Use for creating or overwriting files.`

// WriteTool writes file contents.
type WriteTool struct {
	BaseTool
}

// NewWriteTool creates a new WriteTool.
func NewWriteTool() *WriteTool {
	return &WriteTool{
		BaseTool: NewBaseTool(
			"Write",
			writeDescription,
			writeSchema,
		),
	}
}

type writeInput struct {
	FilePath string `json:"file_path"`
	Content  string `json:"content"`
}

func (t *WriteTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in writeInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.FilePath == "" {
		return &ToolResult{Success: false, Content: "file_path is required"}, nil
	}

	path := in.FilePath
	if !filepath.IsAbs(path) {
		return &ToolResult{Success: false, Content: "file_path must be an absolute path"}, nil
	}

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Failed to create directory: %v", err)}, nil
	}

	// Check if file exists to determine create vs update
	var action string
	if _, err := os.Stat(path); err == nil {
		action = "updated"
	} else {
		action = "created"
	}

	// Write file
	if err := os.WriteFile(path, []byte(in.Content), 0o644); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Failed to write file: %v", err)}, nil
	}

	lines := countLines(in.Content)
	firstLine := ""
	if idx := strings.IndexByte(in.Content, '\n'); idx >= 0 {
		firstLine = in.Content[:idx]
	} else if len(in.Content) > 0 {
		firstLine = in.Content
	}
	if len(firstLine) > 80 {
		firstLine = firstLine[:80] + "..."
	}

	return &ToolResult{
		Success: true,
		Content: fmt.Sprintf("[Write %s — %s: %d lines, %d bytes]\n%s",
			path, action, lines, len(in.Content), firstLine),
		Metadata: map[string]any{
			"file_path": path,
			"action":    action,
			"size":      len(in.Content),
			"lines":     lines,
		},
	}, nil
}

func countLines(s string) int {
	n := 0
	for _, c := range s {
		if c == '\n' {
			n++
		}
	}
	if len(s) > 0 && s[len(s)-1] != '\n' {
		n++
	}
	return n
}
