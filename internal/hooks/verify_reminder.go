package hooks

import (
	"context"
	"fmt"
)

// VerifyReminder injects a verification hint after every Write or Edit.
// The model already knows it should verify (thinking shows "verify" keyword
// in all passing tasks), but it forgets to actually do it. A gentle reminder
// after each code change closes the loop.
type VerifyReminder struct {
	writeCount int
	lastFile   string
}

// NewVerifyReminder creates a post-Write/Edit verification reminder.
func NewVerifyReminder() *VerifyReminder { return &VerifyReminder{} }

func (v *VerifyReminder) Name() string { return "verify_reminder" }

func (v *VerifyReminder) Run(ctx context.Context, event Event, data Data) (Result, error) {
	if event != PostToolUse {
		return Result{Continue: true}, nil
	}

	if data.ToolName != "Write" && data.ToolName != "Edit" {
		return Result{Continue: true}, nil
	}

	v.writeCount++
	v.lastFile = data.ToolName

	// After every Write/Edit, remind the model to verify — but keep it short.
	// The model already knows how to verify; it just needs a nudge.
	return Result{
		Continue: true,
		Message: fmt.Sprintf(
			"Code changed (%s). Verify your change before continuing: compile and test.",
			data.ToolName,
		),
	}, nil
}
