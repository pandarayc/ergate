package cachestability

import (
	"testing"
)

func TestFingerprint_Stable(t *testing.T) {
	a := ComputeFingerprint("hello", []string{"read", "write"})
	b := ComputeFingerprint("hello", []string{"read", "write"})
	if a.CombinedSHA256 != b.CombinedSHA256 {
		t.Error("identical inputs should produce identical fingerprints")
	}
}

func TestFingerprint_DifferentSystem(t *testing.T) {
	a := ComputeFingerprint("hello", []string{"read"})
	b := ComputeFingerprint("world", []string{"read"})
	if a.CombinedSHA256 == b.CombinedSHA256 {
		t.Error("different system prompts should produce different fingerprints")
	}
}

func TestFingerprint_DifferentTools(t *testing.T) {
	a := ComputeFingerprint("system", []string{"read"})
	b := ComputeFingerprint("system", []string{"write"})
	if a.CombinedSHA256 == b.CombinedSHA256 {
		t.Error("different tool sets should produce different fingerprints")
	}
}

func TestFingerprint_ToolOrderIgnored(t *testing.T) {
	a := ComputeFingerprint("system", []string{"read", "write"})
	b := ComputeFingerprint("system", []string{"write", "read"})
	if a.ToolsSHA256 != b.ToolsSHA256 {
		t.Error("tool order should not affect hash")
	}
}

func TestManager_StartsStable(t *testing.T) {
	m := New("system", []string{"read"})
	ch := m.Check("system", []string{"read"})
	if ch != nil {
		t.Error("first check should be stable")
	}
	if m.RatioPercent() != 100 {
		t.Errorf("expected 100%%, got %d%%", m.RatioPercent())
	}
}

func TestManager_DetectsChange(t *testing.T) {
	m := New("old", []string{"read"})
	ch := m.Check("new", []string{"read"})
	if ch == nil {
		t.Fatal("should detect system prompt change")
	}
	if !ch.SystemChanged {
		t.Error("SystemChanged should be true")
	}
	if ch.ToolsChanged {
		t.Error("ToolsChanged should be false")
	}
}

func TestManager_RepinsAfterChange(t *testing.T) {
	m := New("old", []string{"read"})
	m.Check("new", []string{"read"}) // change
	ch := m.Check("new", []string{"read"}) // should be stable now
	if ch != nil {
		t.Error("after re-pin, same input should be stable")
	}
}

func TestManager_StabilityRatio(t *testing.T) {
	m := New("sys", []string{"r"})
	m.Check("sys", []string{"r"}) // stable (1/1)
	m.Check("b", []string{"r"})   // change (1/2)
	m.Check("b", []string{"r"})   // stable (2/3)

	r := m.StabilityRatio()
	if r < 0.66 || r > 0.67 {
		t.Errorf("expected ~0.667, got %.3f", r)
	}
	if m.RatioPercent() != 66 {
		t.Errorf("expected 66%%, got %d%%", m.RatioPercent())
	}
}

func TestManager_DetectsToolChange(t *testing.T) {
	m := New("sys", []string{"read"})
	ch := m.Check("sys", []string{"write"})
	if ch == nil {
		t.Fatal("should detect tool change")
	}
	if ch.SystemChanged {
		t.Error("SystemChanged should be false for tool-only change")
	}
	if !ch.ToolsChanged {
		t.Error("ToolsChanged should be true")
	}
}

func TestChangeDescription(t *testing.T) {
	old := ComputeFingerprint("old", []string{"a"})
	new := ComputeFingerprint("new", []string{"b"})
	c := Change{Old: old, New: new, SystemChanged: true, ToolsChanged: true}
	if c.Description() != "system prompt + tool set" {
		t.Errorf("unexpected description: %s", c.Description())
	}
}
