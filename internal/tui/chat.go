package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/session"
)

// ChatMessage is a rendered message in the chat view.
type ChatMessage struct {
	Role      string
	Content   string // full content (never pre-truncated; fold is render-time only)
	Detail    string // full tool input/output
	Collapsed bool   // set by renderMessage on overflow; toggled by click
	wasFolded bool   // true once Collapsed was ever set
	rendered  string
	dirty     bool // true when content changed and needs re-render
}

// ChatModel manages the chat page: messages, input, viewport, toolsbar.
type ChatModel struct {
	cfg *config.Config
	eng *engine.Engine

	messages []ChatMessage
	input    InputArea
	viewport viewport.Model
	toolsBar ToolsBar

	running         bool
	currentTurn     int
	currentToolName string
	width           int
	height          int

	// Spinner state
	spinnerIdx int

	// Tracking
	totalInTokens  int
	totalOutTokens int

	// Engine communication
	eventChan  chan engine.Event
	engineDone chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc

	// Streaming delta coalescer
	coalesceText  string
	coalesceRole  string
	coalesceDirty bool

	// Scroll
	forceScrollBottom bool

	// overlayHeight is set by AppModel when an inline overlay (permission) is active.
	// This reduces viewport height. Modal overlays (detail) leave this at 0.
	overlayHeight int

	// mouseDisabled is true after a drag event disabled mouse tracking.
	// syncMouse re-enables it and resets this flag.
	mouseDisabled bool

	// copyMode handles text selection and OSC 52 clipboard copy.
	copyMode CopyMode

	// Session persistence
	sessionStore *session.Store
	sessionID    string
}

// engineEventMsg wraps an engine event as a tea.Msg.
type engineEventMsg struct {
	event engine.Event
}

// spinnerTickMsg triggers a spinner frame update.
type spinnerTickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerTickInterval = 80 * time.Millisecond

func nextSpinnerTick() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(t time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// NewChatModel creates a ChatModel.
func NewChatModel(cfg *config.Config, eng *engine.Engine, store *session.Store, resume bool) ChatModel {
	vp := viewport.New(80, 20)

	engineDone := make(chan struct{})
	close(engineDone) // start as done (no engine running)

	m := ChatModel{
		cfg:          cfg,
		eng:          eng,
		input:        NewInputArea(),
		viewport:     vp,
		messages:     make([]ChatMessage, 0),
		toolsBar:     ToolsBar{},
		engineDone:   engineDone,
		sessionStore: store,
	}
	if resume && store != nil {
		if sess, err := store.Latest(); err == nil && sess != nil {
			eng.ImportSession(engine.SessionData{
				Messages: sess.Messages,
				Usage:    sess.Usage,
			})
			m.sessionID = sess.ID
			m.messages = append(m.messages, ChatMessage{
				Role:    "system",
				Content: fmt.Sprintf("[Restored session: %s — %d messages]", sess.ID, len(sess.Messages)),
			})
			m.messages = append(m.messages, convertMessages(eng.Messages())...)
			m.forceScrollBottom = true
			in, out := eng.TotalUsage()
			m.totalInTokens = in
			m.totalOutTokens = out
		}
	}

	return m
}

// Init initializes the chat model.
func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(
		nextSpinnerTick(),
		textarea.Blink,
	)
}

// SetOverlayHeight sets the inline overlay height (e.g. 8 for permission dialog).
// 0 means no inline overlay.
func (m *ChatModel) SetOverlayHeight(h int) {
	m.overlayHeight = h
}

// convertMessages converts engine-level llm.Message to TUI ChatMessage for display.
func convertMessages(msgs []llm.Message) []ChatMessage {
	var out []ChatMessage
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			for _, b := range msg.Content {
				if b.Type == "text" && b.Text != "" {
					out = append(out, ChatMessage{Role: "user", Content: b.Text})
				}
			}
		case "assistant":
			for _, b := range msg.Content {
				switch b.Type {
				case "text":
					if b.Text != "" {
						out = append(out, ChatMessage{Role: "assistant", Content: b.Text})
					}
				case "tool_use":
					out = append(out, ChatMessage{
						Role:    "tool",
						Content: "⚙ " + b.Name,
						Detail:  string(b.Input),
					})
				case "thinking":
					out = append(out, ChatMessage{Role: "thinking", Content: b.Thinking})
				}
			}
		case "tool":
			for _, b := range msg.Content {
				if b.Type == "tool_result" {
					content := string(b.Content)
					if b.IsError {
						out = append(out, ChatMessage{Role: "error", Content: content})
					} else {
						out = append(out, ChatMessage{
							Role:    "tool",
							Content: content,
							Detail:  content,
						})
					}
				}
			}
		}
	}
	return out
}

// visualLineCount returns the number of visual lines when text is rendered at
// the given width, accounting for both newline breaks and line wrapping.
func visualLineCount(text string, width int) int {
	width = max(width, 20)
	count := 0
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(line)
		if len(runes) == 0 {
			count++
		} else {
			count += (len(runes) + width - 1) / width
		}
	}
	return count
}

// syncViewportHeight recalculates viewport height based on footer + toolsbar.
func (m *ChatModel) syncViewportHeight() {
	if m.height == 0 {
		return
	}
	header := 2 // Ergate title + model line
	spacer := 1
	stat := 1
	input := m.input.LineCount()
	if input < 1 {
		input = 1
	}
	footer := spacer + m.toolsBar.Height() + input + stat + m.overlayHeight
	m.viewport.Height = m.height - header - footer
	if m.viewport.Height < 3 {
		m.viewport.Height = 3
	}
}

// refreshToolsBar rebuilds the toolsbar from engine todo/task state.
func (m *ChatModel) refreshToolsBar() {
	var items []ToolsBarItem

	if m.currentToolName != "" {
		items = append(items, ToolsBarItem{Icon: "⚙", Label: m.currentToolName + "..."})
	}

	if m.eng != nil {
		for _, item := range m.eng.TodoItems() {
			icon := map[string]string{"pending": "☐", "in_progress": "▶", "completed": "✓"}[item.Status]
			label := item.Content
			if item.ActiveForm != "" && item.Status == "in_progress" {
				label = item.ActiveForm
			}
			items = append(items, ToolsBarItem{Icon: icon, Label: label})
		}
	}

	m.toolsBar.Set(items)
	m.syncViewportHeight()
}

// saveSession persists the current conversation.
func (m *ChatModel) saveSession() {
	if m.sessionStore == nil {
		return
	}
	data := m.eng.ExportSession()
	sess := &session.Session{
		ID:       m.sessionID,
		Model:    m.cfg.Model,
		Messages: data.Messages,
		Usage:    data.Usage,
	}
	if err := m.sessionStore.Save(sess); err == nil {
		m.sessionID = sess.ID
	}
}

func estimateCost(model string, inTokens, outTokens int) float64 {
	rates := map[string]struct{ in, out float64 }{
		"claude-sonnet-4-20250514": {3.0, 15.0},
		"claude-opus-4-20250514":   {15.0, 75.0},
		"claude-haiku-3-5":         {0.8, 4.0},
		"gpt-4o":                   {2.5, 10.0},
		"gpt-4o-mini":              {0.15, 0.6},
		"deepseek-chat":            {0.27, 1.10},
		"deepseek-reasoner":        {0.55, 2.19},
	}
	prefixes := make([]string, 0, len(rates))
	for prefix := range rates {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })
	for _, prefix := range prefixes {
		if len(model) >= len(prefix) && model[:len(prefix)] == prefix {
			rate := rates[prefix]
			return float64(inTokens)/1e6*rate.in + float64(outTokens)/1e6*rate.out
		}
	}
	return float64(inTokens)/1e6*3.0 + float64(outTokens)/1e6*15.0
}
