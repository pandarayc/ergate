// Package iteration provides structured logging and analysis for benchmark
// iteration cycles. Each benchmark run produces a RunLog that captures scores,
// trace summaries, and failure distributions for comparison across runs.
package iteration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

)

// RunLog captures a single benchmark run's results for iteration tracking.
type RunLog struct {
	RunID     string    `json:"run_id"`
	Agent     string    `json:"agent"`
	Model     string    `json:"model"`
	Benchmark string    `json:"benchmark"`
	CreatedAt time.Time `json:"created_at"`
	Duration  string    `json:"duration"`

	// Summary stats.
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Errors  int `json:"errors"`
	Score   float64 `json:"score"`

	// Per-task results.
	Tasks []TaskResult `json:"tasks"`

	// Aggregated failure analysis.
	FailureDistribution map[string]int `json:"failure_distribution,omitempty"`

	// RecurringKind is set by Patterns() when the dominant failure kind
	// is also dominant in the previous run. Used for stubborn detection
	// across 3+ runs. Not serialized to JSON.
	RecurringKind string `json:"-"`
}

// TaskResult captures a single task's outcome for the iteration log.
type TaskResult struct {
	TaskID    string   `json:"task_id"`
	Pass      bool     `json:"pass"`
	Error     string   `json:"error,omitempty"`
	TurnCount int      `json:"turn_count"`
	ToolCount int      `json:"tool_count"`
	Failures  []string `json:"failures,omitempty"` // failure kind strings
}

// NewRunLog creates a RunLog from a set of task results.
func NewRunLog(runID, agent, model, benchmark, duration string, tasks []TaskResult) *RunLog {
	rl := &RunLog{
		RunID:               runID,
		Agent:               agent,
		Model:               model,
		Benchmark:           benchmark,
		CreatedAt:           time.Now(),
		Duration:            duration,
		Tasks:               tasks,
		FailureDistribution: make(map[string]int),
	}

	for _, t := range tasks {
		rl.Total++
		if t.Error != "" {
			rl.Errors++
			continue
		}
		if t.Pass {
			rl.Passed++
		} else {
			rl.Failed++
			for _, f := range t.Failures {
				rl.FailureDistribution[f]++
			}
			if len(t.Failures) == 0 {
				rl.FailureDistribution["unknown"]++
			}
		}
	}

	if rl.Total > 0 {
		rl.Score = float64(rl.Passed) / float64(rl.Total)
	}
	return rl
}

// TopFailures returns the most common failure kinds, sorted desc.
func (rl *RunLog) TopFailures(limit int) []FailureCount {
	var fc []FailureCount
	for kind, count := range rl.FailureDistribution {
		fc = append(fc, FailureCount{Kind: kind, Count: count})
	}
	sort.Slice(fc, func(i, j int) bool { return fc[i].Count > fc[j].Count })
	if limit > 0 && len(fc) > limit {
		fc = fc[:limit]
	}
	return fc
}

// FailureCount is a pair of failure kind + occurrence count.
type FailureCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Tips returns improvement suggestions based on failure distribution.
func (rl *RunLog) Tips() []string {
	var tips []string
	for _, fc := range rl.TopFailures(3) {
		switch fc.Kind {
		case "tool_error":
			tips = append(tips, "High tool_error rate: check Bash timeout, dependency installation patterns, tool output truncation")
		case "tool_timeout":
			tips = append(tips, "High tool_timeout rate: increase timeout, detect long-running commands early")
		case "premature_end":
			tips = append(tips, "premature_end: agent gave up early — review system prompt for perseverance, or reduce task difficulty")
		case "max_turns":
			tips = append(tips, "max_turns: increase MaxTurns or improve task decomposition efficiency")
		case "unknown":
			tips = append(tips, "unknown failures: tasks failed without tool errors — likely output mismatch, review test scripts")
		}
	}
	if len(tips) == 0 {
		tips = append(tips, "No clear failure pattern — review individual task outputs.")
	}
	return tips
}

// CompareWith compares this run against a previous run.
func (rl *RunLog) CompareWith(prev *RunLog) *RunDiff {
	if prev == nil {
		return &RunDiff{Current: rl}
	}
	return &RunDiff{
		Current:      rl,
		Previous:     prev,
		ScoreDelta:   rl.Score - prev.Score,
		PassedDelta:  rl.Passed - prev.Passed,
		FailedDelta:  rl.Failed - prev.Failed,
		ErrorsDelta:  rl.Errors - prev.Errors,
		// Regressions: tasks that passed before but fail now.
		// Improvements: tasks that failed before but pass now.
	}
}

// RunDiff shows the delta between two benchmark runs.
type RunDiff struct {
	Current      *RunLog  `json:"current"`
	Previous     *RunLog  `json:"previous,omitempty"`
	ScoreDelta   float64  `json:"score_delta"`
	PassedDelta  int      `json:"passed_delta"`
	FailedDelta  int      `json:"failed_delta"`
	ErrorsDelta  int      `json:"errors_delta"`
	Regressions  []string `json:"regressions,omitempty"`  // tasks that regressed
	Improvements []string `json:"improvements,omitempty"` // tasks that improved
}

// ComputeRegressions finds tasks that passed before but fail now.
func (rd *RunDiff) ComputeRegressions() {
	if rd.Previous == nil {
		return
	}
	prevPassed := make(map[string]bool)
	for _, t := range rd.Previous.Tasks {
		if t.Pass {
			prevPassed[t.TaskID] = true
		}
	}
	for _, t := range rd.Current.Tasks {
		if !t.Pass && prevPassed[t.TaskID] {
			rd.Regressions = append(rd.Regressions, t.TaskID)
		}
		if t.Pass && !prevPassed[t.TaskID] {
			rd.Improvements = append(rd.Improvements, t.TaskID)
		}
	}
}

// Save writes the RunLog as JSON.
func (rl *RunLog) Save(path string) error {
	os.MkdirAll(filepath.Dir(path), 0o700)
	data, err := json.MarshalIndent(rl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runlog: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadRunLog reads a RunLog from a JSON file.
func LoadRunLog(path string) (*RunLog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read runlog: %w", err)
	}
	var rl RunLog
	if err := json.Unmarshal(data, &rl); err != nil {
		return nil, fmt.Errorf("unmarshal runlog: %w", err)
	}
	return &rl, nil
}
