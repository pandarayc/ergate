package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var globSchema = Schema(map[string]any{
	"pattern": map[string]any{"type": "string", "description": "The glob pattern to match files against"},
	"path":    map[string]any{"type": "string", "description": "The directory to search in (defaults to current working directory)"},
}, []string{"pattern"})

const globDescription = `Find files matching a glob pattern. Returns a list of matching file paths. Supports standard glob syntax: * matches any characters, ** matches any directories recursively, ? matches a single character. Results are sorted and limited to 100 files.`

// GlobTool finds files matching glob patterns.
type GlobTool struct {
	BaseTool
}

// NewGlobTool creates a new GlobTool.
func NewGlobTool() *GlobTool {
	return &GlobTool{
		BaseTool: NewBaseTool(
			"Glob",
			globDescription,
			globSchema,
			WithReadOnly(),
			WithConcurrencySafe(),
		),
	}
}

type globInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

func (t *GlobTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in globInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.Pattern == "" {
		return &ToolResult{Success: false, Content: "pattern is required"}, nil
	}

	// Determine search path
	searchPath := "."
	if in.Path != "" {
		searchPath = in.Path
	}
	if !filepath.IsAbs(searchPath) && execCtx != nil && execCtx.CWD != "" {
		searchPath = filepath.Join(execCtx.CWD, searchPath)
	}

	// Check if path exists
	if _, err := os.Stat(searchPath); err != nil {
		if os.IsNotExist(err) {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Path not found: %s", searchPath)}, nil
		}
		return &ToolResult{Success: false, Content: fmt.Sprintf("Error accessing path: %v", err)}, nil
	}

	// Build the full glob pattern
	fullPattern := filepath.Join(searchPath, in.Pattern)

	// Use filepath.Glob for simple patterns, walk for ** patterns
	var matches []string

	if strings.Contains(in.Pattern, "**") {
		// Recursive glob
		matches = globRecursive(searchPath, in.Pattern)
	} else {
		var err error
		matches, err = filepath.Glob(fullPattern)
		if err != nil {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Glob error: %v", err)}, nil
		}
	}

	// Sort and limit
	sort.Strings(matches)
	truncated := false
	if len(matches) > 100 {
		matches = matches[:100]
		truncated = true
	}

	// Relativize paths for cleaner output
	relMatches := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, err := filepath.Rel(searchPath, m)
		if err != nil {
			rel = m
		}
		relMatches = append(relMatches, rel)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d file(s)", len(matches)))
	if truncated {
		output.WriteString(" (truncated from >100)")
	}
	output.WriteString(":\n")
	for _, m := range relMatches {
		output.WriteString(fmt.Sprintf("  %s\n", m))
	}

	return &ToolResult{
		Success: true,
		Content: output.String(),
		Metadata: map[string]any{
			"pattern":  in.Pattern,
			"path":     searchPath,
			"num_files": len(matches),
			"truncated": truncated,
		},
	}, nil
}

// globRecursive handles ** patterns by walking the directory tree.
func globRecursive(root, pattern string) []string {
	var results []string

	// Convert glob pattern to filepath.Walk-compatible matching
	// Split pattern by **
	parts := strings.Split(pattern, "**")

	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip hidden directories
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && path != root {
			return filepath.SkipDir
		}

		if info.IsDir() {
			return nil
		}

		// Get relative path
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// Try to match each part of the pattern against the relative path
		if matchGlobParts(rel, parts) {
			results = append(results, path)
		}

		return nil
	})

	return results
}

func matchGlobParts(path string, parts []string) bool {
	if len(parts) == 0 {
		return false
	}

	remaining := path
	for i, part := range parts {
		part = strings.TrimPrefix(part, "/")
		part = strings.TrimSuffix(part, "/")

		if part == "" {
			continue
		}

		// Find the part in remaining path
		if i == 0 {
			// First part: must match from start
			matched, _ := filepath.Match(part, remaining)
			if !matched {
				// Try matching after stripping directory prefixes
				idx := strings.Index(remaining, part)
				if idx < 0 {
					return false
				}
				remaining = remaining[idx+len(part):]
			} else {
				remaining = remaining[len(part):]
			}
		} else if i == len(parts)-1 {
			// Last part: must match at end
			base := filepath.Base(remaining)
			matched, _ := filepath.Match(part, base)
			if !matched && part != "" {
				// Try without directory
				if strings.Contains(remaining, "/") {
					idx := strings.LastIndex(remaining, "/")
					base = remaining[idx+1:]
					matched, _ = filepath.Match(part, base)
				}
			}
			return matched
		} else {
			// Middle part: find anywhere
			idx := strings.Index(remaining, part)
			if idx < 0 {
				return false
			}
			remaining = remaining[idx+len(part):]
		}
	}

	return true
}
