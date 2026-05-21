package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var grepSchema = Schema(map[string]any{
	"pattern":     map[string]any{"type": "string", "description": "The regular expression pattern to search for in file contents"},
	"path":        map[string]any{"type": "string", "description": "File or directory to search in (defaults to current directory)"},
	"glob":        map[string]any{"type": "string", "description": "Glob pattern to filter files (e.g. '*.go', '**/*.ts')"},
	"output_mode": map[string]any{"type": "string", "enum": []string{"content", "files_with_matches", "count"}, "description": "Output mode: content shows matching lines, files_with_matches shows file paths, count shows match counts"},
	"-i":          map[string]any{"type": "boolean", "description": "Case insensitive search"},
	"head_limit":  map[string]any{"type": "integer", "description": "Limit output to first N matching lines"},
}, []string{"pattern"})
const grepDescription = `Search for a regular expression pattern in file contents. Returns matching lines with file paths and line numbers. Supports full regex syntax, file filtering via glob patterns, and case-insensitive mode.`

// GrepTool searches file contents using regex.
type GrepTool struct {
	BaseTool
}

// NewGrepTool creates a new GrepTool.
func NewGrepTool() *GrepTool {
	return &GrepTool{
		BaseTool: NewBaseTool(
			"Grep",
			grepDescription,
			grepSchema,
			WithReadOnly(),
			WithConcurrencySafe(),
		),
	}
}

type grepInput struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Glob       string `json:"glob,omitempty"`
	OutputMode string `json:"output_mode,omitempty"`
	CaseInsensitive bool `json:"-i,omitempty"`
	HeadLimit  int    `json:"head_limit,omitempty"`
}

func (t *GrepTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in grepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.Pattern == "" {
		return &ToolResult{Success: false, Content: "pattern is required"}, nil
	}

	// Parse the path as a more flexible input
	var raw map[string]interface{}
	json.Unmarshal(input, &raw)
	if ci, ok := raw["-i"]; ok {
		if b, ok := ci.(bool); ok {
			in.CaseInsensitive = b
		}
	}

	// Compile regex
	pattern := in.Pattern
	if in.CaseInsensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid regex pattern: %v", err)}, nil
	}

	// Determine search path
	searchPath := "."
	if in.Path != "" {
		searchPath = in.Path
	}
	if !filepath.IsAbs(searchPath) && execCtx != nil && execCtx.CWD != "" {
		searchPath = filepath.Join(execCtx.CWD, searchPath)
	}

	if in.OutputMode == "" {
		in.OutputMode = "content"
	}

	// Collect results
	type match struct {
		file string
		line int
		text string
	}

	var matches []match
	maxMatches := 200
	if in.HeadLimit > 0 && in.HeadLimit < maxMatches {
		maxMatches = in.HeadLimit
	}

	// Check if path is a file or directory
	stat, err := os.Stat(searchPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Path not found: %s", searchPath)}, nil
		}
		return &ToolResult{Success: false, Content: fmt.Sprintf("Error accessing path: %v", err)}, nil
	}

	searchFiles := []string{searchPath}
	if stat.IsDir() {
		searchFiles = nil
		err := filepath.Walk(searchPath, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil // skip errors
			}
			// Skip directories, hidden files, binary files
			if info.IsDir() {
				base := filepath.Base(path)
				if strings.HasPrefix(base, ".") && path != searchPath {
					return filepath.SkipDir
				}
				return nil
			}
			// Skip hidden files
			if strings.HasPrefix(filepath.Base(path), ".") {
				return nil
			}
			// Filter by glob if specified
			if in.Glob != "" {
				matched, _ := filepath.Match(in.Glob, filepath.Base(path))
				if !matched {
					return nil
				}
			}
			// Skip files that are too large (> 1MB)
			if info.Size() > 1*1024*1024 {
				return nil
			}
			searchFiles = append(searchFiles, path)
			return nil
		})
		if err != nil {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Error walking directory: %v", err)}, nil
		}
	}

	// Limit file count
	if len(searchFiles) > 500 {
		searchFiles = searchFiles[:500]
	}

	for _, file := range searchFiles {
		if len(matches) >= maxMatches {
			break
		}
		if in.OutputMode == "files_with_matches" {
			// Quick check: does file contain pattern?
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			if !isText(data) {
				continue
			}
			if re.Match(data) {
				matches = append(matches, match{file: file, line: 0, text: ""})
			}
		} else {
			// Line-by-line scan
			f, err := os.Open(file)
			if err != nil {
				continue
			}

			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			lineNum := 0

			for scanner.Scan() && len(matches) < maxMatches {
				lineNum++
				line := scanner.Text()
				if re.MatchString(line) {
					matches = append(matches, match{file: file, line: lineNum, text: line})
				}
			}
			f.Close()
		}
	}

	// Format output
	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d match(es)", len(matches)))

	if in.HeadLimit > 0 && len(matches) >= in.HeadLimit {
		output.WriteString(fmt.Sprintf(" (limited to %d)", in.HeadLimit))
	}

	switch in.OutputMode {
	case "files_with_matches":
		output.WriteString(":\n")
		for _, m := range matches {
			output.WriteString(fmt.Sprintf("  %s\n", m.file))
		}

	case "count":
		counts := make(map[string]int)
		for _, m := range matches {
			counts[m.file]++
		}
		output.WriteString(":\n")
		for file, count := range counts {
			output.WriteString(fmt.Sprintf("  %s: %d\n", file, count))
		}

	default: // content
		output.WriteString(":\n")
		for _, m := range matches {
			text := m.text
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			output.WriteString(fmt.Sprintf("  %s:%d: %s\n", m.file, m.line, text))
		}
	}

	return &ToolResult{
		Success: true,
		Content: output.String(),
		Metadata: map[string]any{
			"pattern":   in.Pattern,
			"path":      searchPath,
			"num_files": len(searchFiles),
			"num_matches": len(matches),
		},
	}, nil
}
