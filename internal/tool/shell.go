package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

var bashSchema = Schema(map[string]any{
	"command":     map[string]any{"type": "string", "description": "The bash command to execute"},
	"description": map[string]any{"type": "string", "description": "Description of what this command does (for permission review)"},
	"timeout":     map[string]any{"type": "number", "description": "Timeout in milliseconds (default 120000)"},
}, []string{"command"})

const bashDescription = "Execute a bash command in the terminal. " +
	"Use for running tests, building projects, installing dependencies, and git operations. " +
	"Returns stdout and stderr output with exit code. " +
	"IMPORTANT: Avoid using this tool to run `cat`, `head`, `tail`, `sed`, `awk`, or `echo` commands, " +
	"unless explicitly instructed or after you have verified that a dedicated tool cannot accomplish your task. " +
	"Instead, use Read to read files, Write/Edit to modify files, Glob to find files, and Grep to search file contents."

// BashTool executes shell commands.
type BashTool struct {
	BaseTool
}

// NewBashTool creates a new BashTool.
func NewBashTool() *BashTool {
	return &BashTool{
		BaseTool: NewBaseTool(
			"Bash",
			bashDescription,
			bashSchema,
		),
	}
}

type bashInput struct {
	Command     string  `json:"command"`
	Description string  `json:"description,omitempty"`
	Timeout     float64 `json:"timeout,omitempty"`
}

func (t *BashTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	if in.Command == "" {
		return &ToolResult{Success: false, Content: "command is required"}, nil
	}

	// Safety check
	if safe, reason := IsShellSafe(in.Command); !safe {
		return &ToolResult{Success: false, Content: "Command rejected: " + reason}, nil
	}

	timeout := 120 * time.Second
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Millisecond
	}

	execCtx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx2, "bash", "-c", in.Command)
	if execCtx != nil && execCtx.CWD != "" {
		cmd.Dir = execCtx.CWD
	}

	// Limit output to prevent OOM from runaway commands
	const maxOutput = 100 * 1024 // 100KB limit
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &stdout, limit: maxOutput}
	cmd.Stderr = &limitedWriter{buf: &stderr, limit: 10 * 1024}

	err := cmd.Run()

	var result strings.Builder
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return &ToolResult{Success: false, Content: fmt.Sprintf("Command execution failed: %v", err)}, nil
		}
	}

	out := stdout.String()
	errOut := stderr.String()

	if out != "" {
		// Truncate very long output
		if len(out) > 50000 {
			out = out[:50000] + fmt.Sprintf("\n... [truncated, %d total bytes]", len(out))
		}
		result.WriteString(out)
	}
	if errOut != "" {
		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString("[stderr]\n")
		if len(errOut) > 10000 {
			errOut = errOut[:10000] + fmt.Sprintf("\n... [truncated, %d total bytes]", len(errOut))
		}
		result.WriteString(errOut)
	}
	if exitCode != 0 {
		result.WriteString(fmt.Sprintf("\n[Exit code: %d]", exitCode))
	}
	if result.Len() == 0 {
		result.WriteString("[Command completed with no output]")
	}

	return &ToolResult{
		Success: exitCode == 0,
		Content: result.String(),
		Metadata: map[string]any{
			"exit_code": exitCode,
			"truncated": len(out) > 50000 || len(errOut) > 10000,
		},
	}, nil
}

// IsReadOnly checks if the command appears to be read-only.
// `cat`, `head`, `tail`, `grep`, `find`, and `echo` are intentionally
// excluded — those should use Read/Glob/Grep tools instead and will
// be rejected by IsShellSafe.
func (t *BashTool) IsReadOnly(input json.RawMessage) bool {
	var in bashInput
	if err := json.Unmarshal(input, &in); err != nil {
		return false
	}

	cmd := strings.TrimSpace(in.Command)
	readOnlyPrefixes := []string{
		"ls ", "wc ", "du ",
		"git log", "git status", "git diff", "git show", "git stash list",
		"pwd", "whoami", "which ", "type ",
		"ps ", "top ", "df ", "free ", "uname ",
		"go test", "go build", "go vet", "go list",
		"cargo test", "cargo check", "cargo build",
		"npm test", "npm run", "yarn test",
		"python -c", "python3 -c",
	}

	for _, prefix := range readOnlyPrefixes {
		if strings.HasPrefix(cmd, prefix) || cmd == strings.TrimSpace(prefix) {
			return true
		}
	}

	// Pipelines ending in read-only commands
	if strings.Contains(cmd, "|") {
		parts := strings.Split(cmd, "|")
		last := strings.TrimSpace(parts[len(parts)-1])
		for _, prefix := range readOnlyPrefixes {
			if strings.HasPrefix(last, prefix) || last == strings.TrimSpace(prefix) {
				return true
			}
		}
	}

	return false
}

// IsText checks if the output appears to be text (not binary).
func isText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	for _, b := range data[:checkLen] {
		if b == 0 {
			return false
		}
	}
	return utf8.Valid(data[:checkLen])
}

// limitedWriter limits the total bytes written to prevent OOM.
type limitedWriter struct {
	buf   *bytes.Buffer
	limit int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	return w.buf.Write(p)
}
