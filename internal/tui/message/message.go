// Package message defines chat message types that implement list.Item.
// Each message role has its own file with rendering and interaction logic.
package message

import (
	"github.com/raydraw/ergate/internal/tui/list"
)

// ChatMessage is a rendered message in the chat view.
// It implements list.Item and optionally list.MouseHandler (for foldable messages).
type ChatMessage struct {
	list.Versioned

	Role    string // "user", "assistant", "tool", "thinking", "error", "system"
	Content string // full content (never pre-truncated; fold is render-time only)
	Detail  string // full tool input/output (tool role only)

	Collapsed bool // true when folded due to overflow
	wasFolded bool // true once Collapsed was ever set — keeps widget active after unfold
	finished  bool // true when streaming has completed

	// Render cache.
	cacheWidth   int
	cacheVersion uint64
	cacheContent string
}

// New creates a ChatMessage with the given role and content.
func New(role, content string) *ChatMessage {
	return &ChatMessage{
		Role:    role,
		Content: content,
	}
}

// NewTool creates a tool ChatMessage with separate content (display line) and detail (input/output body).
func NewTool(content, detail string) *ChatMessage {
	return &ChatMessage{
		Role:    "tool",
		Content: content,
		Detail:  detail,
	}
}

// SetContent updates the content and bumps the version for cache invalidation.
func (m *ChatMessage) SetContent(s string) {
	m.Content = s
	m.Bump()
}

// AppendContent appends to the content (used during streaming).
func (m *ChatMessage) AppendContent(s string) {
	m.Content += s
	m.Bump()
}

// Finish marks the message as complete. Future renders may freeze the cache.
func (m *ChatMessage) Finish() {
	m.finished = true
	m.Bump()
}

// Finished reports whether the message content is final.
func (m *ChatMessage) Finished() bool { return m.finished }

// ToggleExpand toggles the collapsed state for foldable messages.
func (m *ChatMessage) ToggleExpand() {
	m.Collapsed = !m.Collapsed
	m.Bump()
}

// IsFoldable returns true if this message was ever folded (and thus has a click target).
func (m *ChatMessage) IsFoldable() bool { return m.wasFolded }

// Render implements list.Item. It dispatches to role-specific renderers
// and caches the result keyed by (width, version).
func (m *ChatMessage) Render(width int) string {
	if m.cacheWidth == width && m.cacheVersion == m.Version() {
		return m.cacheContent
	}
	result := m.renderContent(width)
	m.cacheWidth = width
	m.cacheVersion = m.Version()
	m.cacheContent = result
	return result
}

// renderContent dispatches to the appropriate role renderer.
func (m *ChatMessage) renderContent(width int) string {
	switch m.Role {
	case "user":
		return renderUser(m)
	case "assistant":
		return renderAssistant(m, width)
	case "tool":
		return renderTool(m, width)
	case "thinking":
		return renderThinking(m, width)
	case "error":
		return renderError(m)
	case "system":
		return renderSystem(m)
	default:
		return m.Content
	}
}

// HandleMouseClick implements list.MouseHandler for foldable messages.
// x is the column, y is the line offset within the rendered output.
func (m *ChatMessage) HandleMouseClick(btn list.MouseButton, x, y int) bool {
	if !m.wasFolded || btn != list.MouseButtonLeft {
		return false
	}
	m.ToggleExpand()
	return true
}

// Fold constants — may be overridden per render call.
const (
	DefaultMaxToolOutputLines = 8
	DefaultMaxThinkingLines   = 4
)
