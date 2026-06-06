// Package list provides a custom scrollable item list for the TUI.
// It replaces viewport.Model + WidgetLayout with a unified scroll+render+hit-test component,
// inspired by Crush's internal/ui/list package.
package list

// Item is the core interface that every element in the list must satisfy.
// Render(width) returns the ANSI-styled string for the given available width.
// Version() returns a monotonic counter; the list cache uses it to detect mutations.
// Finished() reports whether the item has reached its final state (allowing cache freeze).
type Item interface {
	Render(width int) string
	Version() uint64
	Finished() bool
}

// Versioned is an embeddable struct that provides the Version() method.
// Mutators on items that affect rendered output must call Bump() exactly once
// per observable state change.
type Versioned struct {
	v uint64
}

// Version returns the current version counter.
func (v *Versioned) Version() uint64 { return v.v }

// Bump increments the version counter, invalidating any cached renders.
func (v *Versioned) Bump() { v.v++ }

// MouseHandler is an optional interface for items that respond to mouse clicks.
// x and y are item-relative coordinates: x is the column, y is the line offset
// within the item's rendered output (0 = first line).
// Returns true if the click was handled.
type MouseHandler interface {
	HandleMouseClick(button MouseButton, x, y int) bool
}

// KeyHandler is an optional interface for items that intercept keyboard events.
// Returns true if the key was handled, along with an optional command.
type KeyHandler interface {
	HandleKey(key Key, modifiers Modifiers) (handled bool)
}

// FocusHandler is an optional interface for items that respond to focus changes.
type FocusHandler interface {
	SetFocused(focused bool)
}

// MouseButton identifies a mouse button.
type MouseButton uint8

const (
	MouseButtonLeft MouseButton = iota
	MouseButtonRight
	MouseButtonMiddle
	MouseButtonWheelUp
	MouseButtonWheelDown
)

// Key represents a key press.
type Key struct {
	Rune rune
	Type KeyType
}

// KeyType categorizes keyboard input.
type KeyType uint8

const (
	KeyRune KeyType = iota
	KeyEnter
	KeyEsc
	KeyTab
	KeyShiftTab
	KeyCtrlC
	KeyUp
	KeyDown
	KeyPgUp
	KeyPgDn
	KeyBackspace
)

// Modifiers represents keyboard modifier keys.
type Modifiers uint8

const (
	ModNone  Modifiers = 0
	ModCtrl  Modifiers = 1 << iota
	ModAlt
	ModShift
)
