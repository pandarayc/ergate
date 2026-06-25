// Package trace provides Langfuse-inspired structured extraction from ergate
// session data for failure analysis and benchmark evaluation.
//
// Core model:
//
//	TaskTrace        ← maps to Langfuse Trace
//	├── TurnObs      ← maps to Langfuse Generation (one per LLM call)
//	│   └── ToolSpan ← maps to Langfuse Span   (one per tool execution)
//	└── Score        ← maps to Langfuse Score  (benchmark verdict)
//
// The extractor reads session.Session (JSON on disk) and produces a TaskTrace
// with per-turn observability data, classified failure statuses, and benchmark
// scores — without requiring the Langfuse SDK.
package trace

import "time"

// ObsStatus is a structured execution status, mirroring Langfuse ObservationStatus.
type ObsStatus string

const (
	StatusSuccess ObsStatus = "SUCCESS"
	StatusWarning ObsStatus = "WARNING" // completed with caveats (e.g. stderr present)
	StatusError   ObsStatus = "ERROR"   // execution failed
	StatusTimeout ObsStatus = "TIMEOUT"
)

// ObsLevel mirrors Langfuse ObservationLevel for severity.
type ObsLevel string

const (
	LevelDebug   ObsLevel = "DEBUG"
	LevelDefault ObsLevel = "DEFAULT"
	LevelWarning ObsLevel = "WARNING"
	LevelError   ObsLevel = "ERROR"
)

// FailureKind is a string enum classifying the root cause of a failure.
// Using string so JSON serialization is human-readable without custom marshaler.
type FailureKind string

const (
	FailNone           FailureKind = ""                // no failure
	FailToolError      FailureKind = "tool_error"      // tool returned is_error=true
	FailToolTimeout    FailureKind = "tool_timeout"    // tool hit context deadline
	FailToolPermission FailureKind = "tool_permission" // tool was denied by permission system
	FailUnknownTool    FailureKind = "unknown_tool"    // LLM requested a tool that doesn't exist
	FailPlanModeBlock  FailureKind = "plan_mode_block" // tool blocked by plan mode
	FailHookBlock      FailureKind = "hook_block"      // tool blocked by pre-tool hook
	FailPrematureEnd   FailureKind = "premature_end"   // assistant ended without completing
	FailMaxTurns       FailureKind = "max_turns"       // reached max_turns before completing
	FailAPIError       FailureKind = "api_error"       // LLM API call failed
)

// String returns the string value of the failure kind.
func (k FailureKind) String() string { return string(k) }

// FailureDetail captures structured information about a single failure point.
type FailureDetail struct {
	Kind    FailureKind `json:"kind"`
	Summary string      `json:"summary"`            // human-readable one-liner
	Tool    string      `json:"tool,omitempty"`
	Turn    int         `json:"turn"`
	Input   string      `json:"input,omitempty"`    // truncated tool input
	Output  string      `json:"output,omitempty"`   // truncated tool output / error text
}

// TurnObs represents one LLM call and its tool executions, mirroring a
// Langfuse Generation with nested Spans.
type TurnObs struct {
	Turn      int            `json:"turn"`
	Model     string         `json:"model"`
	TokensIn  int            `json:"tokens_in"`
	TokensOut int            `json:"tokens_out"`
	LatencyMS int64          `json:"latency_ms"`
	Status    ObsStatus      `json:"status"`
	Spans     []ToolSpan     `json:"spans"`
	Error     *FailureDetail `json:"error,omitempty"`
}

// ToolSpan represents a single tool execution, mirroring a Langfuse Span.
type ToolSpan struct {
	Name   string   `json:"name"`
	Input  string   `json:"input"`            // JSON-encoded tool input, truncated
	Output string   `json:"output"`           // tool result content, truncated
	Status ObsStatus `json:"status"`
	Level  ObsLevel  `json:"level"`
	Error  string   `json:"error,omitempty"`  // error detail if status is ERROR
}

// Score is an external evaluation attached to a trace, mirroring Langfuse Score.
// Separated from the trace body so multiple benchmarks or manual reviews can
// attach independent scores to the same run.
type Score struct {
	Name      string    `json:"name"` // e.g. "task_success", "latency", "token_efficiency"
	Value     float64   `json:"value"`
	Comment   string    `json:"comment,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// TaskTrace is the top-level trace for one task execution.
type TaskTrace struct {
	TaskID      string    `json:"task_id"`
	Instruction string    `json:"instruction"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"created_at"`
	Turns       []TurnObs `json:"turns"`
	Scores      []Score   `json:"scores"`

	// Summary computed during extraction.
	TotalTurns     int    `json:"total_turns"`
	TotalToolsRan  int    `json:"total_tools_ran"`
	TotalFailures  int    `json:"total_failures"`
	PrimaryFailure string `json:"primary_failure,omitempty"` // first failure kind encountered
}

// IsFailure returns true if the trace contains at least one failure.
func (t *TaskTrace) IsFailure() bool {
	return t.TotalFailures > 0
}

// FailureKinds returns a deduplicated set of failure kinds found in this trace.
func (t *TaskTrace) FailureKinds() []FailureKind {
	seen := map[FailureKind]bool{}
	for _, turn := range t.Turns {
		if turn.Error != nil && turn.Error.Kind != FailNone {
			seen[turn.Error.Kind] = true
		}
		for _, span := range turn.Spans {
			if span.Status == StatusError || span.Status == StatusTimeout {
				// Span-level errors: detect kind from context.
				if span.Status == StatusTimeout {
					seen[FailToolTimeout] = true
				} else {
					seen[FailToolError] = true
				}
			}
		}
	}
	kinds := make([]FailureKind, 0, len(seen))
	for k := range seen {
		kinds = append(kinds, k)
	}
	return kinds
}

// SpanCountByStatus returns counts of spans grouped by status.
func (t *TaskTrace) SpanCountByStatus() map[ObsStatus]int {
	counts := map[ObsStatus]int{}
	for _, turn := range t.Turns {
		for _, span := range turn.Spans {
			counts[span.Status]++
		}
	}
	return counts
}
