package engine

// TurnReason describes why the engine continued or stopped after a turn.
// Mirrors Claude Code's Continue / Terminal pattern.
type TurnReason string

const (
	// Continue reasons — engine keeps running.
	ContinueNextTurn       TurnReason = "next_turn"        // normal continuation
	ContinueBudgetWarning  TurnReason = "budget_warning"   // approaching turn limit, injected reminder
	ContinueTokenBudget    TurnReason = "token_budget"     // token budget auto-continue
	ContinueLoopWarning    TurnReason = "loop_warning"     // loop detected, injected guidance

	// Terminal reasons — engine stops.
	TerminalCompleted      TurnReason = "completed"         // model returned no tools (natural stop)
	TerminalMaxTurns       TurnReason = "max_turns"         // reached hard turn limit
	TerminalAborted        TurnReason = "aborted"           // context cancelled
	TerminalError          TurnReason = "error"             // unrecoverable error
	TerminalLoopExhausted  TurnReason = "loop_exhausted"    // loop detected 5+ times, forced stop
)

// Transition records why the engine continued or stopped.
type Transition struct {
	Reason TurnReason
	Turn   int
	Detail string // human-readable explanation
}

// TurnBudget tracks soft limits and injects warnings before hard stop.
// maxTurns is the hard limit; warnings start at warningThreshold% of maxTurns.
type TurnBudget struct {
	maxTurns         int
	warningThreshold float64 // default 0.8 (warn at 80% of maxTurns)
	warningSent      bool
	finalWarningSent bool
}

// NewTurnBudget creates a turn budget tracker.
func NewTurnBudget(maxTurns int) *TurnBudget {
	return &TurnBudget{
		maxTurns:         maxTurns,
		warningThreshold: 0.8,
	}
}

// Check evaluates the current turn against the budget.
// Returns nil if we can continue normally, or a Transition if action is needed.
func (tb *TurnBudget) Check(turn int) *Transition {
	if tb.maxTurns <= 0 {
		return nil // no limit
	}

	pct := float64(turn) / float64(tb.maxTurns)

	// Hard stop
	if turn > tb.maxTurns {
		return &Transition{Reason: TerminalMaxTurns, Turn: turn, Detail: "hard turn limit reached"}
	}

	// Final warning at maxTurns
	if turn == tb.maxTurns && !tb.finalWarningSent {
		tb.finalWarningSent = true
		return &Transition{
			Reason: ContinueBudgetWarning,
			Turn:   turn,
			Detail: "This is your last turn. Produce a final answer now.",
		}
	}

	// Early warning at threshold
	if pct >= tb.warningThreshold && !tb.warningSent {
		tb.warningSent = true
		remaining := tb.maxTurns - turn
		return &Transition{
			Reason: ContinueBudgetWarning,
			Turn:   turn,
			Detail: budgetWarningMessage(turn, tb.maxTurns, remaining),
		}
	}

	return nil // continue normally
}

func budgetWarningMessage(turn, maxTurns, remaining int) string {
	if remaining <= 1 {
		return "This is your last turn. Wrap up and produce a final answer."
	}
	return "You have used " + itoa(turn) + " of " + itoa(maxTurns) +
		" turns (" + itoa(remaining) + " remaining). " +
		"Prioritize completing the task over further exploration."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
