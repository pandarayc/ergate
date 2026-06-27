package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const evaluateSchema = `{
  "type": "object",
  "properties": {
    "aspect":  {"type": "string", "description": "What to evaluate: 'compile', 'test', 'output', or 'all'"},
    "command": {"type": "string", "description": "The verification command to run (e.g. 'gcc image.c -o image && ./image')"}
  },
  "required": ["aspect", "command"]
}`

// EvaluateTool lets the main agent check its own work by running a
// sub-agent that executes verification commands and returns structured
// results. This implements the "separate generation from evaluation"
// principle — the evaluator sub-agent has only Read/Bash tools and
// cannot modify files.
type EvaluateTool struct {
	BaseTool
	runSubAgent func(ctx context.Context, prompt, model string, maxTurns int) string
}

// NewEvaluateTool creates an Evaluate tool with a nil sub-agent runner.
// Call SetRunSubAgent before use.
func NewEvaluateTool() *EvaluateTool {
	return &EvaluateTool{
		BaseTool: NewBaseTool(
			"Evaluate",
			"Check your work. Spawns a read-only sub-agent that runs a verification command and returns structured pass/fail with error details. Use after writing code to see if it compiles or passes tests. The sub-agent CANNOT modify files — it only runs commands and reports results.",
			json.RawMessage(evaluateSchema),
			WithReadOnly(),
			WithConcurrencySafe(),
		),
	}
}

// SetRunSubAgent sets the sub-agent runner callback.
func (t *EvaluateTool) SetRunSubAgent(fn func(ctx context.Context, prompt, model string, maxTurns int) string) {
	t.runSubAgent = fn
}

func (t *EvaluateTool) Execute(ctx context.Context, input json.RawMessage, execCtx *ExecContext) (*ToolResult, error) {
	var in struct {
		Aspect  string `json:"aspect"`
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return &ToolResult{Success: false, Content: fmt.Sprintf("Invalid input: %v", err)}, nil
	}

	prompt := fmt.Sprintf(`You are an evaluator. Your ONLY job is to run this command and report results:

%s

Rules:
1. Run the command exactly as given using Bash
2. Capture ALL output (stdout + stderr)
3. Report: PASS if exit code is 0, FAIL if exit code is non-zero
4. If FAIL: extract the FIRST error from the output (file:line: message)
5. Do NOT modify any files — you are read-only
6. Keep your response under 300 words

Output format:
STATUS: PASS or FAIL
EXIT_CODE: <number>
FIRST_ERROR: <file:line: message, or "none">
SUMMARY: <1-2 sentences about what happened>`, in.Command)

	model := ""
	result := t.runSubAgent(ctx, prompt, model, 3)

	// Parse the structured result
	pass := strings.Contains(result, "STATUS: PASS")
	content := result
	if result == "" {
		content = "Evaluation returned no output (sub-agent may have failed)"
	}

	return &ToolResult{
		Success: true,
		Content: content,
		Metadata: map[string]any{
			"aspect":  in.Aspect,
			"command": in.Command,
			"pass":    pass,
		},
	}, nil
}
