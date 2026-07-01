package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// LoopDetector watches for consecutive same-pattern tool calls and injects
// guidance when the model appears stuck in a loop. Unlike a global max_turns
// limit, it resets when the model changes strategy — allowing long setup
// chains (apt-get series) and long-running commands (make) while still
// catching unproductive REPL loops.
//
// Patterns detected:
//   - python3 -c / python3 << 'EOF'  repeated inline execution
//   - Same command verbatim (retry without change)
//   - Same tool with trivially different input
//
// Counter resets when model switches tools or command pattern meaningfully.
type LoopDetector struct {
	threshold   int
	lastTool    string
	lastPattern string // extracted command prefix for Bash, input hash for others
	sameCount   int
	messageSent bool // avoid spamming the same message
}

// NewLoopDetector creates a hook that warns after N consecutive same-pattern calls.
func NewLoopDetector(threshold int) *LoopDetector {
	if threshold <= 0 {
		threshold = 3
	}
	return &LoopDetector{threshold: threshold}
}

// Name returns the hook identifier.
func (l *LoopDetector) Name() string { return "loop_detect" }

// Run checks for repetitive tool usage patterns.
func (l *LoopDetector) Run(ctx context.Context, event Event, data Data) (Result, error) {
	if event != PreToolUse {
		return Result{Continue: true}, nil
	}

	pattern := l.extractPattern(data.ToolName, data.Input)
	if pattern == "" {
		return Result{Continue: true}, nil
	}

	// Same tool + same pattern → increment
	if data.ToolName == l.lastTool && pattern == l.lastPattern {
		l.sameCount++
	} else {
		// Strategy changed → reset
		l.lastTool = data.ToolName
		l.lastPattern = pattern
		l.sameCount = 1
		l.messageSent = false
		return Result{Continue: true}, nil
	}

	// Below threshold → allow
	if l.sameCount < l.threshold {
		return Result{Continue: true}, nil
	}

	// Already sent message for this loop → don't spam
	if l.messageSent {
		return Result{Continue: true}, nil
	}

	l.messageSent = true
	msg := l.buildMessage(data.ToolName, pattern)
	if msg == "" {
		return Result{Continue: true}, nil
	}

	return Result{
		Continue: true, // don't block — guide
		Message:  msg,
	}, nil
}

// extractPattern extracts a comparable pattern string from tool input.
// Returns empty string if the tool call doesn't match a detectable pattern.
func (l *LoopDetector) extractPattern(toolName string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}

	switch toolName {
	case "Bash":
		return l.bashPattern(input)
	case "Read", "Write", "Edit", "Grep", "Glob":
		return toolName // same tool = same pattern for these
	default:
		return "" // don't track other tools (Agent, TaskCreate, etc.)
	}
}

// bashPattern extracts a normalized pattern from Bash input.
// Returns:
//   "python3 -c" for inline Python execution
//   "python3 <<" for heredoc Python
//   First word of the command otherwise
//   Empty string if the command can't be parsed
func (l *LoopDetector) bashPattern(input json.RawMessage) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &parsed); err != nil {
		return ""
	}

	cmd := strings.TrimSpace(parsed.Command)
	if cmd == "" {
		return ""
	}

	// Normalize: strip cd prefix
	cmd = strings.TrimPrefix(cmd, "cd /app && ")
	cmd = strings.TrimPrefix(cmd, "cd /tmp/CompCert && ")
	cmd = strings.TrimPrefix(cmd, "cd /tmp/CompCert/CompCert-3.13.1 && ")
	cmd = strings.TrimSpace(cmd)

	// Detect python3 inline execution patterns
	if strings.HasPrefix(cmd, "python3 -c") {
		return "python3 -c"
	}
	if strings.HasPrefix(cmd, "python3 <<") || strings.HasPrefix(cmd, "python <<") {
		return "python3 heredoc"
	}

	// For other commands, use first 2 words as pattern
	// "apt-get install X" → "apt-get install" (all installs are same pattern)
	// "apt-get update" → "apt-get update" (different from install)
	parts := strings.Fields(cmd)
	if len(parts) >= 2 {
		// Special case: apt-get install (normalize package name)
		if parts[0] == "apt-get" && parts[1] == "install" {
			return "apt-get install"
		}
		return parts[0] + " " + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}

// buildMessage creates a context-aware guidance message.
func (l *LoopDetector) buildMessage(toolName, pattern string) string {
	switch {
	case pattern == "python3 -c" || pattern == "python3 heredoc":
		return fmt.Sprintf(
			"You've used %s %d times in a row. "+
				"Write your code to a .py file (Write tool), run it once (Bash), "+
				"then inspect the output (Read). This is faster and allows Edit for refinements.",
			pattern, l.sameCount,
		)

	case toolName == "Bash" && strings.HasPrefix(pattern, "apt-get"):
		return fmt.Sprintf(
			"apt-get used %d consecutive times. "+
				"Batch your package installs: apt-get install -y pkg1 pkg2 pkg3 in one command.",
			l.sameCount,
		)

	case toolName == "Bash" && (pattern == "ls " || pattern == "find " || pattern == "which "):
		return fmt.Sprintf(
			"Exploration commands used %d times in a row. "+
				"Read the relevant files and start implementing.",
			l.sameCount,
		)

	case toolName == "Read":
		return fmt.Sprintf(
			"Read used %d consecutive times. "+
				"You have enough context — start implementing the change.",
			l.sameCount,
		)

	case toolName == "Write":
		return fmt.Sprintf(
			"Write used %d consecutive times without Evaluate. "+
				"Run a compile/test command (Evaluate) to verify your changes work.",
			l.sameCount,
		)

	default:
		return ""
	}
}
