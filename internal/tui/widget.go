package tui

// WidgetKind identifies the type of interactive region a Widget represents.
type WidgetKind uint8

const (
	WidgetMessage WidgetKind = iota // foldable message in viewport (tool/thinking)
	WidgetToolbar                   // toolbar item or fold toggle row
)

// Widget describes an interactive region produced during View() rendering.
// It is consumed by Update() for hit-test dispatch.
//
// For Content widgets: Y is in full-viewport-content space (matches contentY = mouseY + YOffset).
// For Footer widgets: Y is in terminal space (matches mouseY directly).
type Widget struct {
	Kind   WidgetKind
	Y      int
	Height int
	Index  int // WidgetMessage → messages index; WidgetToolbar → Items index, -1 for fold row
}

// WidgetLayout collects all interactive regions for the current frame.
type WidgetLayout struct {
	Content       []Widget // viewport content widgets (Y = full-content-relative)
	ContentHeight int      // total content lines (including header)
	Footer        []Widget // below-viewport widgets (Y = terminal-relative)
}
