package iteration

import (
	"fmt"
	"sort"
	"strings"
)

// FailurePattern describes a recurring failure mode across tasks in a benchmark run.
// It answers: "what went wrong, how often, across which tasks, and is this a pattern?"
type FailurePattern struct {
	Kind    string `json:"kind"`
	Count   int    `json:"count"`
	Ratio   float64 `json:"ratio"`   // fraction of total-failed (not total tasks)
	TaskIDs []string `json:"task_ids"`

	// Flags
	Dominant  bool `json:"dominant"`  // > 50% of failures
	Recurring bool `json:"recurring"` // dominant now AND was dominant in previous run
	Stubborn  bool `json:"stubborn"`  // dominant in 3+ consecutive runs

	// Signal is a compact human-readable classification.
	// "clean" | "dominant" | "recurring" | "stubborn"
	Signal string `json:"signal"`

	// Suggestion is auto-generated advice based on the failure kind.
	Suggestion string `json:"suggestion"`
}

// dominantThreshold is the fraction of failures above which a pattern is "dominant".
const dominantThreshold = 0.50

// Patterns detects cross-task failure patterns from the run log.
// If prev is non-nil, recurring and stubborn flags are computed against it.
func (rl *RunLog) Patterns(prev *RunLog) []FailurePattern {
	if rl.Total == 0 {
		return nil
	}

	totalFailed := rl.Failed + rl.Errors
	if totalFailed == 0 {
		return nil
	}

	// Build kind → taskIDs mapping.
	kindTasks := make(map[string][]string)
	for _, t := range rl.Tasks {
		if t.Pass && t.Error == "" {
			continue
		}
		if len(t.Failures) == 0 {
			kindTasks["unknown"] = append(kindTasks["unknown"], t.TaskID)
			continue
		}
		for _, f := range t.Failures {
			kindTasks[f] = append(kindTasks[f], t.TaskID)
		}
	}

	// Build patterns sorted by count desc.
	var patterns []FailurePattern
	for kind, taskIDs := range kindTasks {
		count := len(taskIDs)
		ratio := float64(count) / float64(totalFailed)
		p := FailurePattern{
			Kind:    kind,
			Count:   count,
			Ratio:   ratio,
			TaskIDs: taskIDs,
			Dominant: ratio > dominantThreshold,
			Signal: "clean",
		}
		patterns = append(patterns, p)
	}

	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Count > patterns[j].Count
	})

	// Determine dominant and detect recurrence.
	for i := range patterns {
		p := &patterns[i]
		if p.Dominant {
			p.Signal = "dominant"

			// Check against previous run.
			if prev != nil {
				prevDominant := prev.dominantKind()
				if prevDominant == p.Kind {
					p.Recurring = true
					p.Signal = "recurring"
					// If prev was already recurring for the same kind, it's stubborn.
					if prev.RecurringKind == p.Kind {
						p.Stubborn = true
						p.Signal = "stubborn"
					}
					rl.RecurringKind = p.Kind
				}
			}
		}
		p.Suggestion = suggestForKind(p.Kind, p.Count, totalFailed)
	}

	return patterns
}

// dominantKind returns the kind of the dominant failure pattern (if any).
func (rl *RunLog) dominantKind() string {
	patterns := rl.Patterns(nil)
	for _, p := range patterns {
		if p.Dominant {
			return p.Kind
		}
	}
	return ""
}

// DominantPattern returns the single most dominant failure pattern, or nil.
func (rl *RunLog) DominantPattern(prev *RunLog) *FailurePattern {
	patterns := rl.Patterns(prev)
	for i := range patterns {
		if patterns[i].Dominant {
			return &patterns[i]
		}
	}
	return nil
}

// suggestForKind generates improvement advice for a failure kind.
func suggestForKind(kind string, count, total int) string {
	ratio := float64(count) / float64(total)
	var b strings.Builder

	switch kind {
	case "tool_timeout":
		b.WriteString("Timeout is the dominant failure")
		b.WriteString(suffix(count, total))
		b.WriteString(". ")
		b.WriteString("Consider: (1) increase per-task timeout, ")
		b.WriteString("(2) detect long-running commands early with --timeout flag, ")
		b.WriteString("(3) reduce max_turns if tasks loop before timeout.")
	case "tool_error":
		b.WriteString("Tool errors dominate")
		b.WriteString(suffix(count, total))
		b.WriteString(". ")
		b.WriteString("Check: (1) missing dependencies in container images, ")
		b.WriteString("(2) malformed Bash commands, ")
		b.WriteString("(3) model is calling tools with wrong parameters.")
	case "api_error":
		b.WriteString("API errors dominate")
		b.WriteString(suffix(count, total))
		b.WriteString(". ")
		b.WriteString("Verify: (1) API key validity and quota, ")
		b.WriteString("(2) base_url reachability (TLS/cert issues?), ")
		b.WriteString("(3) rate limit configuration.")
	case "max_turns":
		b.WriteString("Max turns reached")
		b.WriteString(suffix(count, total))
		b.WriteString(". ")
		b.WriteString("The agent runs out of turns before completing. ")
		b.WriteString("Consider: (1) increase max_turns, ")
		b.WriteString("(2) reduce task scope, ")
		b.WriteString("(3) improve turn efficiency (fewer redundant tool calls).")
	case "premature_end":
		b.WriteString("Agent gave up early")
		b.WriteString(suffix(count, total))
		b.WriteString(". ")
		b.WriteString("The model declared inability to complete. ")
		b.WriteString("Consider: (1) strengthen perseverance in system prompt, ")
		b.WriteString("(2) add 'try alternative approaches' guidance, ")
		b.WriteString("(3) reduce task difficulty or improve task description.")
	case "unknown":
		b.WriteString("Tasks fail without clear tool errors")
		b.WriteString(suffix(count, total))
		b.WriteString(". ")
		b.WriteString("Test output likely doesn't match expected. ")
		b.WriteString("Review test.sh scripts — agent may have produced correct output ")
		b.WriteString("in wrong format or wrong file location.")
	default:
		b.WriteString("Failure kind: ")
		b.WriteString(kind)
		b.WriteString(suffix(count, total))
		b.WriteString(". Review individual task logs for details.")
	}

	// Add recurrence context.
	if ratio > dominantThreshold && ratio >= 0.75 {
		b.WriteString(" This is a severe concentration — fix this before tuning anything else.")
	}

	return b.String()
}

func suffix(count, total int) string {
	return fmt.Sprintf(" (%d/%d tasks)", count, total)
}
