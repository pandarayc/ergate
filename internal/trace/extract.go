package trace

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/session"
)

const (
	maxInputLen  = 500
	maxOutputLen = 2000
)

// ExtractTaskTrace builds a TaskTrace from a session and task metadata.
// It walks messages sequentially, groups them into turns, extracts tool
// spans, and classifies failures.
func ExtractTaskTrace(sess *session.Session, taskID, instruction string) *TaskTrace {
	if sess == nil {
		return nil
	}

	tr := &TaskTrace{
		TaskID:      taskID,
		Instruction: instruction,
		Model:       sess.Model,
		CreatedAt:   time.Now(),
		Turns:       make([]TurnObs, 0),
		Scores:      make([]Score, 0),
	}

	msgs := sess.Messages
	if len(msgs) == 0 {
		return tr
	}

	// Build turn metrics index for fast lookup.
	turnByIndex := buildTurnIndex(sess.Turns)

	// Walk messages to group into turns.
	// A turn = one assistant message + the tool_result messages that follow it.
	var currentTurn *turnBuilder
	turnSeq := 0

	for i := range msgs {
		msg := &msgs[i]

		switch {
		case msg.Role == "assistant":
			// Commit previous turn.
			if currentTurn != nil {
				tr.Turns = append(tr.Turns, currentTurn.finish(turnByIndex))
			}
			turnSeq++
			currentTurn = newTurnBuilder(turnSeq, msg)

		case msg.Role == "user" && len(msg.Content) > 0:
			// Tool results come as user messages with tool_result content blocks.
			if currentTurn != nil && isToolResult(msg) {
				currentTurn.addToolResult(msg)
			}
			// Non-tool-result user messages are the initial prompt — skip.

		case msg.Role == "system":
			// Skip system messages (informational, compact, etc.)
		}
	}

	// Commit last turn.
	if currentTurn != nil {
		tr.Turns = append(tr.Turns, currentTurn.finish(turnByIndex))
	}

	// Compute summary.
	tr.summarize()
	return tr
}

// ExtractTaskTraceFromMessages builds a TaskTrace directly from messages,
// without a full session.Session. Useful for transcript files (AutoSave output).
func ExtractTaskTraceFromMessages(msgs []llm.Message, taskID, instruction, model string, turnMetrics []session.TurnMetrics) *TaskTrace {
	return ExtractTaskTrace(&session.Session{
		Messages: msgs,
		Model:    model,
		Turns:    turnMetrics,
	}, taskID, instruction)
}

// --- internal helpers ---

// turnBuilder accumulates spans and metadata for one turn during extraction.
type turnBuilder struct {
	turn   int
	model  string
	spans  []ToolSpan
	status ObsStatus
	err    *FailureDetail

	// Map from tool_use_id to span index for matching with tool_result.
	spanByID map[string]int
}

func newTurnBuilder(turn int, msg *llm.Message) *turnBuilder {
	tb := &turnBuilder{
		turn:      turn,
		model:     msg.Model,
		spans:     make([]ToolSpan, 0),
		spanByID:  make(map[string]int),
		status:    StatusSuccess,
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "tool_use":
			span := ToolSpan{
				Name:   block.Name,
				Input:  truncate(string(block.Input), maxInputLen),
				Status: StatusSuccess, // will be updated when tool_result arrives
				Level:  LevelDefault,
			}
			tb.spans = append(tb.spans, span)
			idx := len(tb.spans) - 1
			tb.spanByID[block.ID] = idx

		case "thinking":
			// Thinking is auxiliary; not a span but indicates the turn is thinking-heavy.
			// We don't create a separate span for it — it's part of the generation.

		case "text":
			// Text content in an assistant message — this is the final response.
			// If there are no tool_use blocks, the turn ended without calling tools.
			if block.Text != "" && len(tb.spans) == 0 {
				// Check if this looks like an error/abandonment response.
				if kind, ok := detectTextFailure(block.Text); ok {
					tb.status = StatusError
					tb.err = &FailureDetail{
						Kind:    kind,
						Summary: firstLine(block.Text),
						Turn:    turn,
						Output:  truncate(block.Text, maxOutputLen),
					}
				}
			}
		}
	}

	// If assistant has no tool_use AND no text, it's a degenerate turn (shouldn't happen).
	if len(tb.spans) == 0 && tb.err == nil && !hasTextContent(msg) {
		tb.status = StatusWarning
	}

	return tb
}

func (tb *turnBuilder) addToolResult(msg *llm.Message) {
	for _, block := range msg.Content {
		if block.Type != "tool_result" {
			continue
		}

		idx, ok := tb.spanByID[block.ToolUseID]
		if !ok {
			continue // orphan tool_result, shouldn't happen
		}

		span := &tb.spans[idx]
		output := extractContent(block.Content)

		if block.IsError {
			span.Status = StatusError
			span.Level = LevelError
			span.Error = firstLine(output)

			// Elevate turn-level status.
			if tb.status != StatusError {
				tb.status = StatusError
				tb.err = &FailureDetail{
					Kind:    FailToolError,
					Summary: fmt.Sprintf("%s: %s", span.Name, firstLine(output)),
					Tool:    span.Name,
					Turn:    tb.turn,
					Input:   span.Input,
					Output:  truncate(output, maxOutputLen),
				}
			}
		} else if strings.Contains(output, "[stderr]") || strings.Contains(output, "[Exit code:") {
			// Command succeeded but had stderr or non-zero exit.
			span.Level = LevelWarning
			if !containsNonZeroExit(output) {
				span.Status = StatusWarning
			} else {
				span.Status = StatusError
				span.Level = LevelError
				span.Error = firstLine(output)
				if tb.status != StatusError {
					tb.status = StatusError
					tb.err = &FailureDetail{
						Kind:    FailToolError,
						Summary: fmt.Sprintf("%s: non-zero exit", span.Name),
						Tool:    span.Name,
						Turn:    tb.turn,
						Input:   span.Input,
						Output:  truncate(output, maxOutputLen),
					}
				}
			}
		} else {
			span.Status = StatusSuccess
		}

		span.Output = truncate(output, maxOutputLen)

		// Detect timeout from output.
		if isTimeout(output) {
			span.Status = StatusTimeout
			span.Level = LevelError
			if tb.err == nil || tb.err.Kind == FailToolError {
				tb.status = StatusError
				tb.err = &FailureDetail{
					Kind:    FailToolTimeout,
					Summary: fmt.Sprintf("%s: timed out", span.Name),
					Tool:    span.Name,
					Turn:    tb.turn,
					Input:   span.Input,
					Output:  truncate(output, maxOutputLen),
				}
			}
		}
	}
}

func (tb *turnBuilder) finish(metrics map[int]session.TurnMetrics) TurnObs {
	obs := TurnObs{
		Turn:   tb.turn,
		Model:  tb.model,
		Spans:  tb.spans,
		Status: tb.status,
		Error:  tb.err,
	}

	if m, ok := metrics[tb.turn]; ok {
		obs.TokensIn = m.TokensIn
		obs.TokensOut = m.TokensOut
		obs.LatencyMS = m.LatencyMS
		if obs.Model == "" {
			obs.Model = m.Model
		}
	}

	if len(tb.spans) == 0 && obs.Status == StatusSuccess {
		// Turn with no tools — final response. Mark as success.
	}

	return obs
}

// --- summary computation ---

func (tr *TaskTrace) summarize() {
	tr.TotalTurns = len(tr.Turns)
	for i := range tr.Turns {
		turn := &tr.Turns[i]
		tr.TotalToolsRan += len(turn.Spans)

		hasSpanError := false
		for _, span := range turn.Spans {
			if span.Status == StatusError || span.Status == StatusTimeout {
				tr.TotalFailures++
				hasSpanError = true
			}
		}
		// Only count turn-level error if it's NOT already covered by span errors.
		// Span-derived errors (FailToolError, FailToolTimeout) are counted above;
		// turn-only errors (FailPrematureEnd, FailMaxTurns, FailAPIError) are counted here.
		if turn.Error != nil && !hasSpanError {
			if tr.PrimaryFailure == "" {
				tr.PrimaryFailure = turn.Error.Kind.String()
			}
			tr.TotalFailures++
		} else if turn.Error != nil && tr.PrimaryFailure == "" {
			tr.PrimaryFailure = turn.Error.Kind.String()
		}
	}
}

// --- failure detection ---

// detectTextFailure checks if an assistant text response indicates the
// model gave up or reported an error instead of completing the task.
func detectTextFailure(text string) (FailureKind, bool) {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "i cannot") || strings.Contains(lower, "i can't"):
		return FailPrematureEnd, true
	case strings.Contains(lower, "i'm unable") || strings.Contains(lower, "i am unable"):
		return FailPrematureEnd, true
	case strings.Contains(lower, "unfortunately") && strings.Contains(lower, "cannot"):
		return FailPrematureEnd, true
	default:
		return FailNone, false
	}
}

func isTimeout(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "signal: killed") ||
		strings.Contains(lower, "timed out")
}

func containsNonZeroExit(output string) bool {
	return strings.Contains(output, "[Exit code:") &&
		!strings.Contains(output, "[Exit code: 0]")
}

// --- content extraction ---

// extractContent extracts the string from a ContentBlock's Content field,
// which is JSON-encoded (a quoted string).
func extractContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		// Fallback: return raw as string.
		return string(raw)
	}
	return s
}

func isToolResult(msg *llm.Message) bool {
	for _, block := range msg.Content {
		if block.Type == "tool_result" {
			return true
		}
	}
	return false
}

func hasTextContent(msg *llm.Message) bool {
	for _, block := range msg.Content {
		if block.Type == "text" && block.Text != "" {
			return true
		}
	}
	return false
}

// --- helpers ---

func buildTurnIndex(turns []session.TurnMetrics) map[int]session.TurnMetrics {
	m := make(map[int]session.TurnMetrics, len(turns))
	for _, t := range turns {
		m[t.Turn] = t
	}
	return m
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("... [truncated, %d total chars]", len(s))
}

func firstLine(s string) string {
	if before, _, found := strings.Cut(s, "\n"); found {
		return before
	}
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
