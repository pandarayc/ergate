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

// Overlay represents a blocking UI element (dialog, picker, prompt).
// nil means no overlay is active.
type Overlay struct {
	Kind OverlayKind

	// Permission dialog
	ToolName string
	Summary  string
	Selected int

	// Detail view (OverlayDetail)
	DetailTitle      string        // dialog title (e.g. tool name)
	DetailContent    string        // full content to display
	DetailScroll     int           // content scroll line offset
	DetailSearchMode bool          // search input active
	DetailSearchQ    string        // search query
	DetailMatches    []DetailMatch // fuzzy match results
	DetailMatchIdx   int           // selected match in dropdown (0 = first)
	DetailMatchOff   int           // dropdown scroll offset
}
