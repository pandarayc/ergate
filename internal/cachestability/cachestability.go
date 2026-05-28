// Package cachestability monitors prefix-cache stability across API calls.
//
// It fingerprints the immutable prefix (system prompt + tool specs) with SHA-256
// and detects when the prefix drifts, causing automatic prefix caching to reset.
// The stability ratio (0.0–1.0) can be displayed in the TUI status bar to help
// diagnose "why is this getting slower?" issues.
package cachestability

import (
	"crypto/sha256"
	"fmt"
	"sort"
)

// Fingerprint is a snapshot of the immutable prefix's hashes.
type Fingerprint struct {
	SystemSHA256  string
	ToolsSHA256   string
	CombinedSHA256 string
}

// ComputeFingerprint creates a fingerprint from system prompt text and tool names.
func ComputeFingerprint(systemText string, toolNames []string) Fingerprint {
	sysHash := sha256Hex(systemText)
	sorted := make([]string, len(toolNames))
	copy(sorted, toolNames)
	sort.Strings(sorted)
	toolsHash := sha256Hex(fmt.Sprintf("%v", sorted))
	combined := sha256Hex(sysHash + ":" + toolsHash)
	return Fingerprint{
		SystemSHA256:  sysHash,
		ToolsSHA256:   toolsHash,
		CombinedSHA256: combined,
	}
}

// Change describes what changed in the prefix.
type Change struct {
	Old            Fingerprint
	New            Fingerprint
	SystemChanged  bool
	ToolsChanged   bool
}

// Description returns a human-readable summary of the change.
func (c Change) Description() string {
	if c.SystemChanged && c.ToolsChanged {
		return "system prompt + tool set"
	}
	if c.SystemChanged {
		return "system prompt"
	}
	if c.ToolsChanged {
		return "tool set"
	}
	return "unknown"
}

// Manager monitors prefix-cache stability across API calls.
//
// On each Check(), it compares the current fingerprint against the pinned
// baseline. If they match, the prefix is stable (cache-friendly). If they
// differ, the baseline is updated and the change is recorded.
type Manager struct {
	pinned      *Fingerprint
	current     *Fingerprint
	lastChange  *Change
	changeCount uint64
	checkCount  uint64
}

// New creates a Manager and immediately pins the first fingerprint.
func New(systemText string, toolNames []string) *Manager {
	fp := ComputeFingerprint(systemText, toolNames)
	pinned := fp // copy
	return &Manager{
		pinned:  &pinned,
		current: &fp,
	}
}

// Check compares the current prefix against the pinned fingerprint.
// Returns nil if stable, or a Change if the prefix drifted.
// On change, the pinned fingerprint is updated to the new baseline.
func (m *Manager) Check(systemText string, toolNames []string) *Change {
	fp := ComputeFingerprint(systemText, toolNames)
	m.current = &fp
	m.checkCount++

	if m.pinned == nil {
		// First call: pin now
		m.pinned = &fp
		return nil
	}

	if fp.CombinedSHA256 == m.pinned.CombinedSHA256 {
		return nil // stable
	}

	// Change detected
	m.changeCount++
	ch := &Change{
		Old:           *m.pinned,
		New:           fp,
		SystemChanged: fp.SystemSHA256 != m.pinned.SystemSHA256,
		ToolsChanged:  fp.ToolsSHA256 != m.pinned.ToolsSHA256,
	}
	m.lastChange = ch

	// Re-pin to new baseline
	m.pinned = &fp
	return ch
}

// StabilityRatio returns the fraction of checks that were stable (0.0–1.0).
func (m *Manager) StabilityRatio() float64 {
	if m.checkCount == 0 {
		return 1.0
	}
	stable := m.checkCount - m.changeCount
	return float64(stable) / float64(m.checkCount)
}

// RatioPercent returns the stability ratio as an integer percentage (0–100).
func (m *Manager) RatioPercent() int {
	return int(m.StabilityRatio() * 100)
}

// LastChange returns the most recent prefix change, if any.
func (m *Manager) LastChange() *Change {
	return m.lastChange
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}
