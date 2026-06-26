package iteration

import (
	"testing"
)

func makeRunLog(t *testing.T, tasks []TaskResult) *RunLog {
	t.Helper()
	return NewRunLog("test", "ergate", "test-model", "test-bench", "1m", tasks)
}

func TestPatterns_Clean(t *testing.T) {
	tasks := []TaskResult{
		{TaskID: "a", Pass: false, Failures: []string{"tool_error"}},
		{TaskID: "b", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "c", Pass: false, Failures: []string{"max_turns"}},
		{TaskID: "d", Pass: false, Failures: []string{"api_error"}},
		{TaskID: "e", Pass: true},
	}
	rl := makeRunLog(t, tasks)

	patterns := rl.Patterns(nil)
	if len(patterns) != 4 {
		t.Fatalf("expected 4 pattern kinds, got %d", len(patterns))
	}
	for _, p := range patterns {
		if p.Dominant {
			t.Errorf("no pattern should be dominant with even distribution, but %q is dominant", p.Kind)
		}
		if p.Signal != "clean" {
			t.Errorf("expected signal=clean for %q, got %q", p.Kind, p.Signal)
		}
	}
}

func TestPatterns_Dominant(t *testing.T) {
	tasks := []TaskResult{
		{TaskID: "a", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "b", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "c", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "d", Pass: false, Failures: []string{"tool_error"}},
		{TaskID: "e", Pass: true},
		{TaskID: "f", Pass: true},
	}
	rl := makeRunLog(t, tasks)
	patterns := rl.Patterns(nil)

	var tp *FailurePattern
	for i := range patterns {
		if patterns[i].Kind == "tool_timeout" {
			tp = &patterns[i]
			break
		}
	}
	if tp == nil {
		t.Fatal("expected tool_timeout pattern")
	}
	if !tp.Dominant {
		t.Errorf("expected tool_timeout dominant (3/4=75%%), got dominant=%v", tp.Dominant)
	}
	if tp.Signal != "dominant" {
		t.Errorf("expected signal=dominant, got %q", tp.Signal)
	}
	if tp.Count != 3 {
		t.Errorf("expected count=3, got %d", tp.Count)
	}
	if len(tp.TaskIDs) != 3 {
		t.Errorf("expected 3 task IDs, got %d: %v", len(tp.TaskIDs), tp.TaskIDs)
	}
	if tp.Suggestion == "" {
		t.Error("expected non-empty suggestion")
	}
}

func TestPatterns_Recurring(t *testing.T) {
	curr := makeRunLog(t, []TaskResult{
		{TaskID: "a", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "b", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "c", Pass: false, Failures: []string{"tool_error"}},
	})
	prev := makeRunLog(t, []TaskResult{
		{TaskID: "x", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "y", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "z", Pass: false, Failures: []string{"tool_error"}},
	})

	patterns := curr.Patterns(prev)
	for i := range patterns {
		if patterns[i].Kind == "tool_timeout" {
			if !patterns[i].Recurring {
				t.Errorf("expected tool_timeout recurring, got recurring=%v", patterns[i].Recurring)
			}
			if patterns[i].Signal != "recurring" {
				t.Errorf("expected signal=recurring, got %q", patterns[i].Signal)
			}
			return
		}
	}
	t.Fatal("expected tool_timeout pattern not found")
}

func TestPatterns_Stubborn(t *testing.T) {
	run1 := makeRunLog(t, []TaskResult{
		{TaskID: "a", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "b", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "c", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "d", Pass: false, Failures: []string{"tool_error"}},
	})
	run2 := makeRunLog(t, []TaskResult{
		{TaskID: "e", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "f", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "g", Pass: false, Failures: []string{"tool_error"}},
	})
	run3 := makeRunLog(t, []TaskResult{
		{TaskID: "h", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "i", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "j", Pass: false, Failures: []string{"max_turns"}},
	})

	r1Patterns := run1.Patterns(nil)
	assertSignal(t, r1Patterns, "tool_timeout", "dominant", false, false)

	r2Patterns := run2.Patterns(run1)
	assertSignal(t, r2Patterns, "tool_timeout", "recurring", true, false)

	r3Patterns := run3.Patterns(run2)
	assertSignal(t, r3Patterns, "tool_timeout", "stubborn", true, true)
}

func assertSignal(t *testing.T, patterns []FailurePattern, kind, expectedSignal string, expectedRecurring, expectedStubborn bool) {
	t.Helper()
	for i := range patterns {
		if patterns[i].Kind == kind {
			p := patterns[i]
			if p.Signal != expectedSignal {
				t.Errorf("kind=%q: expected signal=%q, got %q", kind, expectedSignal, p.Signal)
			}
			if p.Recurring != expectedRecurring {
				t.Errorf("kind=%q: expected recurring=%v, got %v", kind, expectedRecurring, p.Recurring)
			}
			if p.Stubborn != expectedStubborn {
				t.Errorf("kind=%q: expected stubborn=%v, got %v", kind, expectedStubborn, p.Stubborn)
			}
			return
		}
	}
	t.Errorf("pattern kind=%q not found", kind)
}

func TestPatterns_Empty(t *testing.T) {
	rl := &RunLog{Tasks: []TaskResult{}}
	patterns := rl.Patterns(nil)
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns for empty run, got %d", len(patterns))
	}
}

func TestPatterns_AllPass(t *testing.T) {
	rl := makeRunLog(t, []TaskResult{
		{TaskID: "a", Pass: true},
		{TaskID: "b", Pass: true},
	})
	patterns := rl.Patterns(nil)
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns for all-pass run, got %d", len(patterns))
	}
}

func TestPatterns_Errored(t *testing.T) {
	rl := makeRunLog(t, []TaskResult{
		{TaskID: "a", Error: "engine: context deadline exceeded"},
		{TaskID: "b", Error: "engine: context deadline exceeded"},
		{TaskID: "c", Error: "engine: context deadline exceeded"},
		{TaskID: "d", Pass: true},
	})
	patterns := rl.Patterns(nil)

	for i := range patterns {
		if patterns[i].Kind == "unknown" {
			if patterns[i].Count != 3 {
				t.Errorf("expected count=3 for unknown, got %d", patterns[i].Count)
			}
			if !patterns[i].Dominant {
				t.Errorf("expected unknown dominant (3/3 failed), got dominant=%v", patterns[i].Dominant)
			}
			return
		}
	}
	t.Fatal("expected 'unknown' pattern not found")
}

func TestDominantPattern_Nil(t *testing.T) {
	rl := makeRunLog(t, []TaskResult{
		{TaskID: "a", Pass: false, Failures: []string{"tool_error"}},
		{TaskID: "b", Pass: false, Failures: []string{"max_turns"}},
		{TaskID: "c", Pass: true},
	})
	dp := rl.DominantPattern(nil)
	if dp != nil {
		t.Errorf("expected nil DominantPattern for even distribution, got %q", dp.Kind)
	}
}

func TestDominantPattern_Found(t *testing.T) {
	rl := makeRunLog(t, []TaskResult{
		{TaskID: "a", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "b", Pass: false, Failures: []string{"tool_timeout"}},
		{TaskID: "c", Pass: false, Failures: []string{"tool_error"}},
	})
	dp := rl.DominantPattern(nil)
	if dp == nil {
		t.Fatal("expected non-nil DominantPattern")
	}
	if dp.Kind != "tool_timeout" {
		t.Errorf("expected tool_timeout, got %q", dp.Kind)
	}
}
