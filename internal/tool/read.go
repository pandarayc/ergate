package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var readSchema = Schema(map[string]any{
	"file_path": map[string]any{"type": "string", "description": "The absolute path to the file to read"},
	"offset":    map[string]any{"type": "integer", "description": "Line number to start reading from (1-based)"},
	"limit":     map[string]any{"type": "integer", "description": "Maximum number of lines to read"},
}, []string{"file_path"})

const readDescription = `Read a file from the local filesystem. Returns the file content with line numbers. Supports reading specific line ranges with offset and limit parameters. Supports text files and displays images (PNG, JPG).`

// ReadTool reads file contents.
type ReadTool struct {
	BaseTool
	fileCache map[string]string // path -> cached content
}

// NewReadTool creates a new ReadTool.
func NewReadTool() *ReadTool {
	return &ReadTool{
		BaseTool: NewBaseTool(
			"Read",
			readDescription,
			readSchema,
			WithReadOnly(),
			WithConcurrencySafe(),
		),
		fileCache: make(map[string]string),
	}
}

type readInput struct {
	FilePath string `json:"file_path"`
	Offset   int    `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in readInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.FilePath == "" {
		return &ToolResult{Success: false, Content: "file_path is required"}, nil
	}

	// Resolve path
	path := in.FilePath
	if !filepath.IsAbs(path) && execCtx != nil && execCtx.CWD != "" {
		path = filepath.Join(execCtx.CWD, path)
	}

	// Security: check for device files
	if isDevicePath(path) {
		return &ToolResult{Success: false, Content: "Reading device files is not allowed"}, nil
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("File not found: %s", path)}, nil
		}
		if os.IsPermission(err) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Permission denied: %s", path)}, nil
		}
		return &ToolResult{Success: false, Content: fmt.Sprintf("Error reading file: %v", err)}, nil
	}

	// Check file size
	if len(data) > 1*1024*1024 { // 1MB limit
		return &ToolResult{Success: false, Content: fmt.Sprintf("File too large (%d bytes). Use offset/limit to read specific parts.", len(data))}, nil
	}

	// Check if it's a binary file
	if !isText(data) {
		return &ToolResult{Success: false, Content: "Cannot read binary file. Only text files are supported."}, nil
	}

	content := string(data)
	lines := strings.Split(content, "\n")
	// Remove trailing empty line from split
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Apply offset and limit
	start := 0
	if in.Offset > 0 {
		start = in.Offset - 1 // convert to 0-indexed
		if start >= len(lines) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Offset %d exceeds file length (%d lines)", in.Offset, len(lines))}, nil
		}
	}

	end := len(lines)
	if in.Limit > 0 {
		end = start + in.Limit
		if end > len(lines) {
			end = len(lines)
		}
	}

	// Format output with line numbers
	var output strings.Builder
	totalLines := end - start
	lineNumWidth := len(fmt.Sprintf("%d", end))

	for i := start; i < end; i++ {
		fmt.Fprintf(&output, "%*d\t%s\n", lineNumWidth, i+1, lines[i])
	}

	if len(output.String()) > 50000 {
		truncated := output.String()[:50000]
		output.Reset()
		output.WriteString(truncated)
		output.WriteString(fmt.Sprintf("\n... [truncated at 50000 chars, %d lines, %d bytes total]", len(lines), len(content)))
	}

	return &ToolResult{
		Success: true,
		Content: output.String(),
		Metadata: map[string]any{
			"file_path":     path,
			"total_lines":   len(lines),
			"shown_lines":   totalLines,
			"file_size":     len(data),
			"start_line":    start + 1,
		},
	}, nil
}

func isDevicePath(path string) bool {
	clean := filepath.Clean(path)
	devicePrefixes := []string{"/dev/zero", "/dev/random", "/dev/urandom", "/dev/null", "/dev/stdin"}
	for _, prefix := range devicePrefixes {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}
