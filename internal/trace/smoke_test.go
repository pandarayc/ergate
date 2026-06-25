// smoke_test demonstrates the trace extraction pipeline with realistic
// multi-turn session data. Run with: go test -run TestSmoke -v ./internal/trace/
package trace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/session"
)

// TestSmoke runs a realistic multi-turn session through the extraction pipeline
// and prints the resulting trace for human inspection.
func TestSmoke(t *testing.T) {
	// --- Scenario: agent attempts to fix a failing Go build ---
	// Turn 1: Read the file, Bash the build → build fails
	// Turn 2: Edit the fix → Bash again → still fails (typo in fix)
	// Turn 3: Read again to check → Edit correctly → Bash → passes
	// Turn 4: Final response

	scenarios := []struct {
		name    string
		msgs    []llm.Message
		turns   []session.TurnMetrics
		desc    string
	}{
		{
			name: "successful_fix",
			desc: "Agent debugs and fixes a build error over 4 turns",
			msgs: []llm.Message{
				makeUserMsg("The build is failing. Fix it."),
				// Turn 1: diagnose
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_1", "Bash", map[string]any{"command": "go build ./..."}),
					makeToolUseBlock("call_2", "Read", map[string]any{"file_path": "main.go"}),
				),
				makeToolResultMsg("call_1",
					"# example.com/myapp\nmain.go:15:2: undefined: Calculate\nmain.go:22:5: cannot use result (type int) as string",
					false,
				),
				makeToolResultMsg("call_2",
					"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := Calculate()\n\tfmt.Println(x)\n}",
					false,
				),
				// Turn 2: first fix attempt (typo)
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_3", "Edit", map[string]any{
						"file_path": "main.go",
						"old_string": "x := Calculate()",
						"new_string": "x := Calculat()", // intentional typo
					}),
				),
				makeToolResultMsg("call_3", "Edit applied: main.go (+1 -1)", false),
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_4", "Bash", map[string]any{"command": "go build ./..."}),
				),
				makeToolResultMsg("call_4",
					"# example.com/myapp\nmain.go:15:2: undefined: Calculat",
					false,
				),
				// Turn 3: correct fix
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_5", "Read", map[string]any{"file_path": "main.go"}),
				),
				makeToolResultMsg("call_5",
					"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tx := Calculat()\n\tfmt.Println(x)\n}",
					false,
				),
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_6", "Edit", map[string]any{
						"file_path": "main.go",
						"old_string": "x := Calculat()",
						"new_string": "x := CalculateCorrect()",
					}),
					makeToolUseBlock("call_7", "Bash", map[string]any{"command": "go build ./..."}),
				),
				makeToolResultMsg("call_6", "Edit applied: main.go (+1 -1)", false),
				makeToolResultMsg("call_7", "Build successful. No errors.", false),
				// Turn 4: done
				makeAssistantMsg("claude-sonnet-4-6",
					makeTextBlock("Fixed! The build error was caused by a missing `Calculate` function. I renamed the call to `CalculateCorrect()`. The build now passes."),
				),
			},
			turns: []session.TurnMetrics{
				{Turn: 1, Model: "claude-sonnet-4-6", TokensIn: 1200, TokensOut: 400, ToolsRan: 2, LatencyMS: 5200},
				{Turn: 2, Model: "claude-sonnet-4-6", TokensIn: 2000, TokensOut: 350, ToolsRan: 1, LatencyMS: 3800},
				{Turn: 3, Model: "claude-sonnet-4-6", TokensIn: 2800, TokensOut: 300, ToolsRan: 1, LatencyMS: 4200},
				{Turn: 4, Model: "claude-sonnet-4-6", TokensIn: 3500, TokensOut: 500, ToolsRan: 2, LatencyMS: 5100},
				{Turn: 5, Model: "claude-sonnet-4-6", TokensIn: 4200, TokensOut: 200, ToolsRan: 0, LatencyMS: 1800},
			},
		},
		{
			name: "tool_error_chain",
			desc: "Agent hits dependency installation failures",
			msgs: []llm.Message{
				makeUserMsg("Install and run the R analysis script"),
				// Turn 1: try to run directly, fails
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_1", "Bash", map[string]any{"command": "Rscript analysis.R"}),
				),
				makeToolResultMsg("call_1",
					"Error in library(ggplot2): there is no package called 'ggplot2'\nExecution halted",
					true,
				),
				// Turn 2: try to install, permission error
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_2", "Bash", map[string]any{"command": "install.packages('ggplot2')"}),
				),
				makeToolResultMsg("call_2",
					"Warning: unable to access index for repository https://cran.r-project.org/src/contrib\ncannot open URL\nInstallation failed",
					true,
				),
				// Turn 3: give up
				makeAssistantMsg("claude-sonnet-4-6",
					makeTextBlock("I cannot install ggplot2 because the R package repository is not accessible from this environment. The task cannot be completed without network access to CRAN."),
				),
			},
			turns: []session.TurnMetrics{
				{Turn: 1, Model: "claude-sonnet-4-6", TokensIn: 800, TokensOut: 200, ToolsRan: 1, LatencyMS: 3500},
				{Turn: 2, Model: "claude-sonnet-4-6", TokensIn: 1200, TokensOut: 250, ToolsRan: 1, LatencyMS: 4200},
				{Turn: 3, Model: "claude-sonnet-4-6", TokensIn: 1600, TokensOut: 150, ToolsRan: 0, LatencyMS: 2000},
			},
		},
		{
			name: "timeout_scenario",
			desc: "Agent runs a long command that times out",
			msgs: []llm.Message{
				makeUserMsg("Compile the Linux kernel with custom config"),
				makeAssistantMsg("claude-sonnet-4-6",
					makeToolUseBlock("call_1", "Bash", map[string]any{"command": "make -j$(nproc) all"}),
				),
				makeToolResultMsg("call_1",
					"  CC      init/main.o\n  CC      arch/x86/kernel/process.o\n...\nCommand execution failed: context deadline exceeded",
					true,
				),
			},
			turns: []session.TurnMetrics{
				{Turn: 1, Model: "claude-sonnet-4-6", TokensIn: 600, TokensOut: 150, ToolsRan: 1, LatencyMS: 120500},
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			sess := &session.Session{
				ID:       "smoke_" + sc.name,
				Model:    "claude-sonnet-4-6",
				Messages: sc.msgs,
				Turns:    sc.turns,
			}

			tr := ExtractTaskTrace(sess, sc.name, sc.desc)

			// Print structured summary.
			fmt.Printf("\n%s\n", strings.Repeat("═", 70))
			fmt.Printf("Task: %s\n", sc.name)
			fmt.Printf("Desc: %s\n", sc.desc)
			fmt.Printf("%s\n", strings.Repeat("─", 70))
			fmt.Printf("Turns: %d  Tools: %d  Failures: %d  PrimaryFailure: %s\n",
				tr.TotalTurns, tr.TotalToolsRan, tr.TotalFailures, tr.PrimaryFailure)

			for _, turn := range tr.Turns {
				statusMark := "✓"
				if turn.Status == StatusError {
					statusMark = "✗"
				} else if turn.Status == StatusWarning {
					statusMark = "⚠"
				}
				fmt.Printf("  Turn %d [%s] model=%s tokens_in=%d out=%d latency=%dms\n",
					turn.Turn, statusMark, turn.Model, turn.TokensIn, turn.TokensOut, turn.LatencyMS)

				for _, span := range turn.Spans {
					sMark := "✓"
					switch span.Status {
					case StatusError, StatusTimeout:
						sMark = "✗"
					case StatusWarning:
						sMark = "⚠"
					}
					fmt.Printf("    %s %s level=%s input=%s output=%s\n",
						sMark, span.Name, span.Level,
						truncate(span.Input, 60),
						truncate(firstLine(span.Output), 80),
					)
					if span.Error != "" {
						fmt.Printf("      error: %s\n", span.Error)
					}
				}
				if turn.Error != nil {
					fmt.Printf("    ╰─ failure: kind=%s summary=%s\n",
						turn.Error.Kind.String(), turn.Error.Summary)
				}
			}

			fmt.Printf("\nFailure kinds: %v\n", tr.FailureKinds())
			fmt.Printf("Span counts: %v\n", tr.SpanCountByStatus())

			// Serialize to JSON.
			jsonBytes, _ := json.MarshalIndent(tr, "", "  ")
			fmt.Printf("\nJSON output (%d bytes):\n%s\n", len(jsonBytes), firstLines(string(jsonBytes), 40))

			// Assertions for the smoke test.
			switch sc.name {
			case "successful_fix":
				if tr.IsFailure() {
					t.Error("expected no failure for successful_fix")
				}
			case "tool_error_chain":
				if !tr.IsFailure() {
					t.Error("expected failure for tool_error_chain")
				}
				if tr.PrimaryFailure != "tool_error" {
					t.Errorf("expected tool_error, got %s", tr.PrimaryFailure)
				}
				kinds := tr.FailureKinds()
				hasPremature := false
				for _, k := range kinds {
					if k == FailPrematureEnd {
						hasPremature = true
					}
				}
				if !hasPremature {
					t.Error("expected FailPrematureEnd in failure kinds")
				}
			case "timeout_scenario":
				if !tr.IsFailure() {
					t.Error("expected failure for timeout_scenario")
				}
				if tr.PrimaryFailure != "tool_timeout" {
					t.Errorf("expected tool_timeout, got %s", tr.PrimaryFailure)
				}
			}
		})
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n..."
}
