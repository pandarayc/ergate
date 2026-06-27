package hooks

import (
	"context"
	"fmt"
	"sync"
)

// PhaseEnforcer enforces a two-phase execution protocol:
//   Phase 1 (first N turns): Read-only tools only (no Bash, Write, Edit)
//   Phase 2 (after N turns or explicit plan): All tools allowed
//
// This prevents the model from skipping directly to Bash execution
// without first understanding the task and producing a plan.
type PhaseEnforcer struct {
	mu           sync.Mutex
	phase1Turns  int     // how many turns for the read-only phase
	currentTurn  int     // current turn counter
}

// NewPhaseEnforcer creates a hook that enforces N turns of read-only exploration.
func NewPhaseEnforcer(phase1Turns int) *PhaseEnforcer {
	if phase1Turns <= 0 {
		phase1Turns = 3
	}
	return &PhaseEnforcer{phase1Turns: phase1Turns}
}

// Name returns the hook identifier.
func (p *PhaseEnforcer) Name() string { return "phase_enforcer" }

// Run checks tool usage against the current phase.
func (p *PhaseEnforcer) Run(ctx context.Context, event Event, data Data) (Result, error) {
	if event != PreToolUse {
		return Result{Continue: true}, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Phase 1: block write/execute tools
	if p.currentTurn < p.phase1Turns {
		switch data.ToolName {
		case "Bash", "Write", "Edit":
			p.currentTurn++ // still count blocked attempts as turns
			return Result{
				Continue: false,
				Message: fmt.Sprintf(
					"Phase 1 (turn %d/%d): Analysis phase — use Read/Glob/Grep/TodoWrite only. Do not execute yet. Create a plan first.",
					p.currentTurn+1, p.phase1Turns,
				),
			}, nil
		case "TodoWrite":
			// Allow plan creation — this is the goal of Phase 1
			p.currentTurn++
		default:
			p.currentTurn++
		}
		return Result{Continue: true}, nil
	}

	// Phase 2: allow all tools
	return Result{Continue: true}, nil
}
