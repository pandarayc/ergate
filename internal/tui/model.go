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
	Role     string
	Content  string // full content (never pre-truncated; fold is render-time only)
	Detail   string // full tool input/output
	Collapsed bool // set by renderMessage on overflow; toggled by click
	wasFolded bool // true once Collapsed was ever set (distinguishes "never folded" from "expanded")
	rendered  string
}

// Model is the top-level bubbletea model.
type Model struct {
	cfg *config.Config
	eng *engine.Engine

	input    textarea.Model
	viewport viewport.Model

	messages       []ChatMessage
	running        bool
	quitting       bool
	width          int
	height         int
	err            error
	currentTurn    int
	totalInTokens  int
	totalOutTokens int

	// Spinner state
	spinnerIdx      int
	currentToolName string

	// Input history
	inputHistory []string
	historyIdx   int

	// Overlay state (nil = no active overlay)
	overlay *Overlay

	// ToolsBar shows active tool/task progress between viewport and spacer.
	toolsBar          ToolsBar
	forceScrollBottom bool // true after session restore, forces viewport to bottom

	// Session persistence
	sessionStore *session.Store
	sessionID    string
	didRestore   bool

	// Engine event channel
	eventChan  chan engine.Event
	engineDone chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc

	// Streaming delta coalescer
	coalesceText  string
	coalesceRole  string
	coalesceDirty bool
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

// NewModel creates a new TUI model.
func NewModel(cfg *config.Config, eng *engine.Engine, store *session.Store, resume bool) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.ShowLineNumbers = false
	ta.MaxHeight = 5
	ta.SetPromptFunc(0, func(lineIdx int) string { return "" })
	ta.SetHeight(1)
	ta.Focus()

	vp := viewport.New(80, 20)

	engineDone := make(chan struct{})
	close(engineDone) // start as done (no engine running)

	m := Model{
		cfg:          cfg,
		eng:          eng,
		input:        ta,
		viewport:     vp,
		messages:     make([]ChatMessage, 0),
		sessionStore: store,
		engineDone:   engineDone,
	}

	// Auto-restore latest session only when -r/--resume flag is set
	if resume && store != nil {
		if sess, err := store.Latest(); err == nil && sess != nil {
			eng.ImportSession(engine.SessionData{
				Messages: sess.Messages,
				Usage:    sess.Usage,
			})
			m.didRestore = true
			m.sessionID = sess.ID
			m.messages = append(m.messages, ChatMessage{
				Role:    "system",
				Content: fmt.Sprintf("[Restored session: %s — %d messages]", sess.ID, len(sess.Messages)),
			})
			// Rebuild TUI display messages from the restored engine state.
			m.messages = append(m.messages, convertMessages(eng.Messages())...)
			m.forceScrollBottom = true
			in, out := eng.TotalUsage()
			m.totalInTokens = in
			m.totalOutTokens = out
		}
	}

	return m
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

// syncInputHeight adjusts textarea height to match visual line count (1–5).
func (m *Model) syncInputHeight() {
	lc := m.input.LineCount()
	if lc < 1 {
		lc = 1
	}
	if lc > 5 {
		lc = 5
	}
	m.input.SetHeight(lc)
}

// syncViewportHeight recalculates viewport height based on footer + toolsbar.
// Call after any layout-affecting change (input height, toolsbar items, overlay, window resize).
func (m *Model) syncViewportHeight() {
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
	overlay := 0
	if m.overlay != nil && m.overlay.Kind == OverlayPermission {
		overlay = 8 // permission dialog (inline, pushes viewport up)
	}
	// OverlayDetail is a modal — rendered over footer, doesn't affect viewport.
	footer := spacer + m.toolsBar.Height() + input + stat + overlay
	m.viewport.Height = m.height - header - footer
	if m.viewport.Height < 3 {
		m.viewport.Height = 3
	}
}

// refreshToolsBar rebuilds the toolsbar from engine todo/task state.
func (m *Model) refreshToolsBar() {
	var items []ToolsBarItem

	// Current running tool
	if m.currentToolName != "" {
		items = append(items, ToolsBarItem{Icon: "⚙", Label: m.currentToolName + "..."})
	}

	// Todo items from engine
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

// syncMouse returns a command to enable or disable mouse tracking based on
// whether interactive elements (toolsbar, overlay) are currently visible.
// Mouse is OFF by default so native terminal copy (drag-select) works.
func (m *Model) syncMouse() tea.Cmd { return nil }

// flushCoalesced writes buffered streaming deltas to messages.
// Should be called before critical events and periodically from spinner tick.
func (m *Model) flushCoalesced() {
	if !m.coalesceDirty {
		return
	}
	text := m.coalesceText
	m.coalesceText = ""
	m.coalesceDirty = false

	n := len(m.messages)
	if n > 0 && m.messages[n-1].Role == m.coalesceRole {
		m.messages[n-1].Content += text
		m.messages[n-1].rendered = ""
	} else {
		m.messages = append(m.messages, ChatMessage{Role: m.coalesceRole, Content: text})
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		nextSpinnerTick(),
		textarea.Blink,
	)
}

// saveSession persists the current conversation.
func (m *Model) saveSession() {
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
		// Anthropic
		"claude-sonnet-4-20250514": {3.0, 15.0},
		"claude-opus-4-20250514":   {15.0, 75.0},
		"claude-haiku-3-5":         {0.8, 4.0},
		// OpenAI
		"gpt-4o":      {2.5, 10.0},
		"gpt-4o-mini": {0.15, 0.6},
		// DeepSeek
		"deepseek-chat":     {0.27, 1.10},
		"deepseek-reasoner": {0.55, 2.19},
	}
	// Longest match first to prevent "gpt-4o" from matching "gpt-4o-mini".
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
