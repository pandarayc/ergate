package tui

// OverlayKind identifies the active overlay type.
// Only one flow-scoped overlay can be active at a time.
type OverlayKind int

const (
	OverlayPermission OverlayKind = iota
	OverlayDetail
)

// DetailMatch is a search result within detail content.
type DetailMatch struct {
	Line int    // 0-based line index in content
	Text string // the matching line text (truncated for display)
}

// OverlayManager manages a single-layer overlay stack.
// nil active means no overlay is shown.
type OverlayManager struct {
	active *Overlay
}

// Show activates an overlay.
func (om *OverlayManager) Show(o *Overlay) {
	om.active = o
}

// Hide dismisses the current overlay.
func (om *OverlayManager) Hide() {
	om.active = nil
}

// IsActive returns true if an overlay is currently shown.
func (om *OverlayManager) IsActive() bool {
	return om.active != nil
}

// Active returns the current overlay, or nil.
func (om *OverlayManager) Active() *Overlay {
	return om.active
}

// Overlay represents a blocking UI element (dialog, picker, prompt).
// nil means no overlay is active.
type Overlay struct {
	Kind OverlayKind

	// Permission dialog.
	ToolName string
	Summary  string
	Selected int

	// Detail view (OverlayDetail).
	DetailTitle      string        // dialog title (e.g. tool name)
	DetailContent    string        // full content to display
	DetailScroll     int           // content scroll line offset
	DetailSearchMode bool          // search input active
	DetailSearchQ    string        // search query
	DetailMatches    []DetailMatch // fuzzy match results
	DetailMatchIdx   int           // selected match in dropdown (0 = first)
	DetailMatchOff   int           // dropdown scroll offset
}
