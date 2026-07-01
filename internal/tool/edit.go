package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

var editSchema = Schema(map[string]any{
	"file_path":   map[string]any{"type": "string", "description": "The absolute path to the file to modify"},
	"old_string":  map[string]any{"type": "string", "description": "The text to replace"},
	"new_string":  map[string]any{"type": "string", "description": "The text to replace it with (must be different from old_string)"},
	"replace_all": map[string]any{"type": "boolean", "description": "Replace all occurrences of old_string (default false)"},
}, []string{"file_path", "old_string", "new_string"})

const editDescription = `Performs exact string replacement in an existing file. You must Read the file first. The edit fails if old_string is not unique — provide more surrounding context to make it unique, or use replace_all to replace every occurrence.`

// EditTool performs search-and-replace in files.
type EditTool struct {
	BaseTool
}

// NewEditTool creates a new EditTool.
func NewEditTool() *EditTool {
	return &EditTool{
		BaseTool: NewBaseTool(
			"Edit",
			editDescription,
			editSchema,
		),
	}
}

type editInput struct {
	FilePath   string `json:"file_path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (t *EditTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in editInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.FilePath == "" {
		return &ToolResult{Success: false, Content: "file_path is required"}, nil
	}
	if in.OldString == "" {
		return &ToolResult{Success: false, Content: "old_string is required"}, nil
	}
	if in.OldString == in.NewString {
		return &ToolResult{Success: false, Content: "old_string and new_string must be different"}, nil
	}

	// Check file size first
	stat, err := os.Stat(in.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("File not found: %s", in.FilePath)}, nil
		}
		return &ToolResult{Success: false, Content: fmt.Sprintf("Error accessing file: %v", err)}, nil
	}
	if stat.Size() > 1*1024*1024 {
		return &ToolResult{Success: false, Content: fmt.Sprintf("File too large (%d bytes). Maximum 1MB.", stat.Size())}, nil
	}

	// Read file
	data, err := os.ReadFile(in.FilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("File not found: %s", in.FilePath)}, nil
		}
		return &ToolResult{Success: false, Content: fmt.Sprintf("Error reading file: %v", err)}, nil
	}

	content := string(data)
	occurrences := strings.Count(content, in.OldString)

	if occurrences == 0 {
		return &ToolResult{Success: false, Content: fmt.Sprintf("old_string not found in file. Check whitespace and exact match.")}, nil
	}

	if occurrences > 1 && !in.ReplaceAll {
		return &ToolResult{
			Success: false,
			Content: fmt.Sprintf("old_string found %d times in file. Use replace_all to replace all occurrences, or provide a more specific string with more surrounding context to make it unique.", occurrences),
		}, nil
	}

	newContent := strings.ReplaceAll(content, in.OldString, in.NewString)

	if newContent == content {
		return &ToolResult{Success: false, Content: "No changes made (old_string == new_string)"}, nil
	}

	// Write file
	if err := os.WriteFile(in.FilePath, []byte(newContent), 0o644); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Failed to write file: %v", err)}, nil
	}

	replacements := occurrences
	if !in.ReplaceAll {
		replacements = 1
	}

	return &ToolResult{
		Success: true,
		Content: fmt.Sprintf("Replaced %d occurrence(s) in %s", replacements, in.FilePath),
		Metadata: map[string]any{
			"file_path":    in.FilePath,
			"occurrences":  occurrences,
			"replacements": replacements,
		},
	}, nil
}
