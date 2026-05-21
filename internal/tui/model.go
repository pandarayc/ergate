package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/session"
)

// ChatMessage is a rendered message in the chat view.
type ChatMessage struct {
	Role    string
	Content string
	Detail  string
	rendered string // cached rendered output; cleared when Content changes
}

// Model is the top-level bubbletea model.
type Model struct {
	cfg *config.Config
	eng *engine.Engine

	input    textinput.Model
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

	// Permission dialog state
	permActive   bool
	permToolName string
	permSummary  string
	permSelected int

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
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Prompt = "▸ "
	ti.Focus()

	vp := viewport.New(80, 20)

	engineDone := make(chan struct{})
	close(engineDone) // start as done (no engine running)

	m := Model{
		cfg:          cfg,
		eng:          eng,
		input:        ti,
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
			in, out := eng.TotalUsage()
			m.totalInTokens = in
			m.totalOutTokens = out
		}
	}

	return m
}

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
		textinput.Blink,
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
