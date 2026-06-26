package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/raydraw/ergate/internal/bench"
	"github.com/raydraw/ergate/internal/iteration"
	"github.com/raydraw/ergate/internal/llm"
	_ "github.com/raydraw/ergate/internal/llm/provider"
	"github.com/raydraw/ergate/internal/tool"
)

// benchRunCmd is the `bench run` subcommand.
var benchRunCmd = &cobra.Command{
	Use:   "run <task-dir> [task-dir...]",
	Short: "Run benchmark tasks against ergate",
	Long: `Run one or more benchmark tasks. Each task directory must contain:
  instruction.txt  — natural-language task description
  test.sh          — verification script (exit 0 = pass)

Results are written to <task-dir>/results/<task-id>/result.json.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runBench,
}

var benchFlags = struct {
	model    string
	timeout  time.Duration
	maxTurns int
	output   string // JSON output file (combined results)
}{}

// BenchCmd returns the bench subcommand tree.
func BenchCmd() *cobra.Command {
	benchCmd := &cobra.Command{
		Use:   "bench",
		Short: "Run benchmark tasks",
		Long:  `Execute benchmark tasks with ergate and capture structured results.`,
	}
	benchCmd.AddCommand(benchRunCmd)

	benchRunCmd.Flags().StringVarP(&benchFlags.model, "model", "m", "", "Model override for benchmark")
	benchRunCmd.Flags().DurationVarP(&benchFlags.timeout, "timeout", "t", 10*time.Minute, "Per-task timeout")
	benchRunCmd.Flags().IntVar(&benchFlags.maxTurns, "max-turns", 25, "Max turns per task")
	benchRunCmd.Flags().StringVarP(&benchFlags.output, "output", "o", "", "Write combined results to JSON file")

	return benchCmd
}

func runBench(cmd *cobra.Command, args []string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		return fmt.Errorf("prepare dirs: %w", err)
	}

	if benchFlags.model != "" {
		cfg.Model = benchFlags.model
	}
	if benchFlags.maxTurns > 0 {
		cfg.MaxTurns = benchFlags.maxTurns
	}

	// Set up engine components.
	pc := cfg.ActiveProviderConfig()
	client, err := llm.NewLLMClient(cfg.CompatProvider(), pc.APIKey, pc.BaseURL)
	if err != nil {
		return fmt.Errorf("create LLM client: %w", err)
	}
	defer client.Close()

	todoMgr := tool.NewTodoManager()
	reg := tool.NewRegistry()
	tool.RegisterLocalTools(reg, todoMgr)

	// Determine bench dir from first task.
	benchDir := filepath.Dir(args[0])
	runner := bench.NewRunner(cfg, client, reg, benchDir)

	var results []*bench.Result
	for _, taskDir := range args {
		task, err := bench.LoadTask(taskDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", taskDir, err)
			continue
		}

		task.Timeout = benchFlags.timeout
		task.MaxTurns = benchFlags.maxTurns

		fmt.Printf("🏃 %s ... ", task.ID)
		result := runner.Run(context.Background(), task)

		status := "✅ PASS"
		if !result.Pass {
			status = "❌ FAIL"
		}
		fmt.Printf("%s (%d turns, %d tools, %v)\n",
			status, result.Trace.TotalTurns, result.Trace.TotalToolsRan,
			result.Duration.Round(time.Millisecond))

		// Print failure summary.
		if result.Trace.IsFailure() {
			kinds := result.Trace.FailureKinds()
			var kindStrs []string
			for _, k := range kinds {
				kindStrs = append(kindStrs, k.String())
			}
			fmt.Printf("   failures: %s\n", strings.Join(kindStrs, ", "))
		}
		if result.Error != "" {
			fmt.Printf("   error: %s\n", result.Error)
		}

		// Save individual result.
		if err := runner.SaveResult(task.ID, result); err != nil {
			fmt.Fprintf(os.Stderr, "   save error: %v\n", err)
		}

		results = append(results, result)
	}

	// Write combined output if requested.
	if benchFlags.output != "" && len(results) > 0 {
		if err := writeResultsJSON(benchFlags.output, results); err != nil {
			return fmt.Errorf("write results: %w", err)
		}
		fmt.Printf("\n📊 Results written to %s\n", benchFlags.output)
	}

	// Print cross-task failure pattern analysis.
	if len(results) >= 2 {
		printPatternAnalysis(results, nil, cfg.Model)
	}

	return nil
}

func writeResultsJSON(path string, results []*bench.Result) error {
	type summary struct {
		Total   int             `json:"total"`
		Passed  int             `json:"passed"`
		Failed  int             `json:"failed"`
		Results []*bench.Result `json:"results"`
	}
	s := summary{Total: len(results), Results: results}
	for _, r := range results {
		if r.Pass {
			s.Passed++
		} else {
			s.Failed++
		}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// printPatternAnalysis builds a RunLog from bench results, detects failure
// patterns, and prints a structured analysis to stdout.
func printPatternAnalysis(results []*bench.Result, prev *iteration.RunLog, model string) {
	tasks := make([]iteration.TaskResult, len(results))
	for i, r := range results {
		tr := iteration.TaskResult{
			TaskID:    r.TaskID,
			Pass:      r.Pass,
			Error:     r.Error,
			TurnCount: r.Trace.TotalTurns,
			ToolCount: r.Trace.TotalToolsRan,
		}
		if r.Trace != nil {
			for _, k := range r.Trace.FailureKinds() {
				tr.Failures = append(tr.Failures, k.String())
			}
		}
		tasks[i] = tr
	}

	runLog := iteration.NewRunLog("", "ergate", model, "", "", tasks)
	patterns := runLog.Patterns(prev)

	fmt.Println()
	fmt.Println("📊 Failure Patterns:")

	if len(patterns) == 0 {
		fmt.Println("   (no failures to analyze)")
		return
	}

	for _, p := range patterns {
		tag := signalTag(p.Signal)
		fmt.Printf("   %s  %-16s (%d/%d, %.0f%%)\n",
			tag, p.Kind, p.Count, runLog.Failed+runLog.Errors, p.Ratio*100)

		if len(p.TaskIDs) > 0 {
			fmt.Printf("   %s  Tasks: %s\n", strings.Repeat(" ", len(tag)), strings.Join(p.TaskIDs, ", "))
		}
		if p.Suggestion != "" {
			fmt.Printf("   💡 %s\n", p.Suggestion)
		}
		fmt.Println()
	}
}

func signalTag(signal string) string {
	switch signal {
	case "stubborn":
		return "🔴"
	case "recurring":
		return "🟡"
	case "dominant":
		return "🟠"
	case "clean":
		return "🟢"
	default:
		return "⚪"
	}
}
