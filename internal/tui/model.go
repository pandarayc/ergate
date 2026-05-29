package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/session"
)

// Model is the top-level bubbletea model. (DEPRECATED — migrating to AppModel+ChatModel)
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

// saveSession persists the current conversation.
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

// Init initializes the model.
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
