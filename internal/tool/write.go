package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var writeSchema = Schema(map[string]any{
	"file_path": map[string]any{"type": "string", "description": "The absolute path to the file to write (must be absolute, not relative)"},
	"content":   map[string]any{"type": "string", "description": "The content to write to the file"},
}, []string{"file_path", "content"})

const writeDescription = `Write a file to the local filesystem. Creates a new file or overwrites an existing one with the provided content. The file path must be absolute. Parent directories are created automatically.`

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

	return &ToolResult{
		Success: true,
		Content: fmt.Sprintf("File %s (%d bytes, %d lines)", action, len(in.Content), countLines(in.Content)),
		Metadata: map[string]any{
			"file_path": path,
			"action":    action,
			"size":      len(in.Content),
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
