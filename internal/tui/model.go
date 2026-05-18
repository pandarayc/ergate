package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/session"
	"github.com/raydraw/ergate/internal/util"
)

// ChatMessage is a rendered message in the chat view.
type ChatMessage struct {
	Role    string
	Content string
	Detail  string
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
	spinnerIdx     int
	currentToolName string

	// Input history
	inputHistory []string
	historyIdx    int

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
	eventChan chan engine.Event
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewModel creates a new TUI model.
func NewModel(cfg *config.Config, eng *engine.Engine, store *session.Store, resume bool) Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message..."
	ti.Prompt = "▸ "
	ti.Focus()

	vp := viewport.New(80, 20)

	m := Model{
		cfg:          cfg,
		eng:          eng,
		input:        ti,
		viewport:     vp,
		messages:     make([]ChatMessage, 0),
		eventChan:    make(chan engine.Event, 128),
		sessionStore: store,
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

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		func() tea.Msg { return spinnerTickMsg{} },
		textinput.Blink,
	)
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 5
		m.input.Width = msg.Width - 4
		return m, nil

	case tea.KeyMsg:
		// Permission dialog key handling
		if m.permActive {
			switch msg.Type {
			case tea.KeyUp:
				if m.permSelected > 0 {
					m.permSelected--
				}
			case tea.KeyDown:
				if m.permSelected < 3 {
					m.permSelected++
				}
			case tea.KeyEnter, tea.KeyEsc:
				m.permActive = false
			}
			return m, nil
		}

		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			if m.running {
				if m.cancel != nil {
					m.cancel()
				}
				m.running = false
				m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Interrupted]"})
				return m, nil
			}
			m.saveSession()
			m.quitting = true
			return m, tea.Quit

		case tea.KeyEnter:
			if m.running {
				return m, nil
			}
			input := strings.TrimSpace(m.input.Value())
			if input == "" {
				return m, nil
			}

			if strings.HasPrefix(input, "/") {
				m.handleCommand(input)
				m.input.Reset()
				return m, nil
			}

			m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
			m.inputHistory = append(m.inputHistory, input)
			m.historyIdx = len(m.inputHistory)
			m.running = true
			m.input.Reset()

			m.ctx, m.cancel = context.WithCancel(context.Background())
			go m.runEngine(input)
			cmds = append(cmds, m.listenEvents())

		case tea.KeyCtrlP:
			if !m.running && len(m.inputHistory) > 0 {
				if m.historyIdx > 0 {
					m.historyIdx--
				}
				m.input.SetValue(m.inputHistory[m.historyIdx])
			}

		case tea.KeyCtrlN:
			if !m.running && len(m.inputHistory) > 0 {
				if m.historyIdx < len(m.inputHistory)-1 {
					m.historyIdx++
					m.input.SetValue(m.inputHistory[m.historyIdx])
				} else {
					m.historyIdx = len(m.inputHistory)
					m.input.Reset()
				}
			}

		case tea.KeyUp:
			m.viewport.ScrollUp(3)

		case tea.KeyDown:
			m.viewport.ScrollDown(3)

		case tea.KeyPgUp:
			m.viewport.HalfPageUp()

		case tea.KeyPgDown:
			m.viewport.HalfPageDown()
		}

	case engineEventMsg:
		m.handleEngineEvent(msg.event)
		if !m.running {
			in, out := m.eng.TotalUsage()
			m.totalInTokens = in
			m.totalOutTokens = out
		}
		if m.running {
			cmds = append(cmds, m.listenEvents())
		}

	case spinnerTickMsg:
		if m.running {
			m.spinnerIdx = (m.spinnerIdx + 1) % len(spinnerFrames)
			cmds = append(cmds, func() tea.Msg {
				time.Sleep(80 * time.Millisecond)
				return spinnerTickMsg{}
			})
		}
	}

	// Update input when not running
	if !m.running && !m.permActive {
		newInput, cmd := m.input.Update(msg)
		m.input = newInput
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the full screen.
func (m Model) View() string {
	if m.quitting {
		return "\n  Goodbye!\n\n"
	}

	var b strings.Builder

	// Header (fixed at top)
	headerStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true).Padding(0, 1)
	b.WriteString(headerStyle.Render("Ergate"))
	b.WriteString(lipgloss.NewStyle().Foreground(Muted).Render(fmt.Sprintf("  model: %s\n\n", m.cfg.Model)))

	// Welcome page when no messages
	if len(m.messages) == 0 {
		welcome := lipgloss.NewStyle().Foreground(Muted).Padding(1).Render(
			"Welcome to Ergate!\n\n" +
				"  Ctrl+C  Quit\n" +
				"  ↑/↓     Scroll\n" +
				"  Ctrl+P  Previous input\n" +
				"  Ctrl+N  Next input\n" +
				"  PgUp/Dn Page scroll\n" +
				"\nType a message to start...",
		)
		b.WriteString(welcome)
	} else {
		// Messages with separation
		var prevRole string
		for _, msg := range m.messages {
			// Add blank line between user messages and previous content (1.4)
			if msg.Role == "user" && prevRole != "" && prevRole != "user" {
				b.WriteString("\n")
			}
			// Add left border accent for assistant blocks (1.4)
			if msg.Role == "assistant" && prevRole != "assistant" {
				b.WriteString("\n")
			}
			b.WriteString(renderMessage(msg))
			b.WriteString("\n")
			prevRole = msg.Role
		}
	}

	// Spinner with context (1.2)
	if m.running {
		spinnerText := spinnerFrames[m.spinnerIdx] + " Thinking..."
		if m.currentToolName != "" {
			spinnerText = spinnerFrames[m.spinnerIdx] + " " + m.currentToolName + "..."
		}
		b.WriteString(SpinnerStyle.Render(spinnerText + "\n"))
	}

	b.WriteString("\n")

	// Set viewport
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()

	// Permission dialog
	var bottom strings.Builder
	if m.permActive {
		bottom.WriteString(renderPermDialog(m.permToolName, m.permSummary, m.permSelected, m.width))
		bottom.WriteString("\n")
	}

	// Input area
	inputView := InputAreaStyle.Render(m.input.View())
	bottom.WriteString(inputView)
	bottom.WriteString("\n")

	// Status bar (1.1) — context %, cost, plan mode, session ID
	in, out := m.eng.TotalUsage()
	totalTokens := in + out
	ctxPct := 0
	if totalTokens > 0 {
		ctxPct = totalTokens * 100 / 128000 // rough context window
	}
	cost := estimateCost(m.cfg.Model, in, out)
	status := fmt.Sprintf(" turn:%d | ctx:%d%% | $%.4f", m.currentTurn, ctxPct, cost)
	if m.sessionID != "" {
		status += fmt.Sprintf(" | %s", truncateStr(m.sessionID, 12))
	}
	if m.running {
		status = " ⏳" + status
	}
	bottom.WriteString(StatusBarStyle.Render(" " + status + " "))

	return lipgloss.JoinVertical(lipgloss.Left, m.viewport.View(), bottom.String())
}

func (m *Model) handleCommand(input string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "/exit", "/quit":
		m.saveSession()
		m.quitting = true
	case "/clear":
		m.eng.Clear()
		m.messages = nil
	case "/save":
		m.saveSession()
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "Session saved."})
	case "/load":
		if m.sessionStore != nil && len(parts) > 1 {
			sess, err := m.sessionStore.Load(parts[1])
			if err == nil {
				m.eng.ImportSession(engine.SessionData{Messages: sess.Messages, Usage: sess.Usage})
				m.messages = []ChatMessage{{Role: "system", Content: fmt.Sprintf("[Loaded: %s]", parts[1])}}
				m.sessionID = parts[1]
			} else {
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: fmt.Sprintf("Load failed: %v", err)})
			}
		}
	case "/sessions":
		if m.sessionStore != nil {
			ids, _ := m.sessionStore.List()
			m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Sessions: %v", ids)})
		}
	case "/help":
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "/help /exit /clear /model /usage /config /save /load /sessions /cost /status"})
	case "/model":
		if len(parts) > 1 {
			m.cfg.Model = parts[1]
		}
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Model: %s", m.cfg.Model)})
	case "/usage":
		in, out := m.eng.TotalUsage()
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Tokens — in:%d out:%d total:%d", in, out, in+out)})
	case "/cost":
		in, out := m.eng.TotalUsage()
		cost := estimateCost(m.cfg.Model, in, out)
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Est. cost: $%.4f  (in:%d out:%d)", cost, in, out)})
	case "/config":
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf(
			"Provider:%s  Model:%s  Permissions:%s  MaxTurns:%d",
			m.cfg.APIProvider, m.cfg.Model, m.cfg.PermissionMode, m.cfg.MaxTurns,
		)})
	case "/status":
		msgs := m.eng.Messages()
		in, out := m.eng.TotalUsage()
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf(
			"Model:%s  Messages:%d  Tokens(in:%d out:%d)  Session:%s",
			m.cfg.Model, len(msgs), in, out, m.sessionID,
		)})
	default:
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Unknown: %s", parts[0])})
	}
}

func (m *Model) runEngine(input string) {
	// eng.Run sends all events (including errors) through the channel before
	// closing it. The returned error is secondary — ignore it.
	_ = m.eng.Run(m.ctx, input, m.eventChan)
}

func (m *Model) listenEvents() tea.Cmd {
	return func() tea.Msg {
		select {
		case event, ok := <-m.eventChan:
			if !ok {
				return engineEventMsg{event: engine.Event{Type: engine.EventDone}}
			}
			return engineEventMsg{event: event}
		case <-time.After(100 * time.Millisecond):
			return engineEventMsg{event: engine.Event{Type: ""}}
		}
	}
}

func (m *Model) handleEngineEvent(event engine.Event) {
	switch event.Type {
	case engine.EventText:
		if text, ok := event.Data.(string); ok {
			n := len(m.messages)
			if n > 0 && m.messages[n-1].Role == "assistant" {
				m.messages[n-1].Content += text
			} else {
				m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: text})
			}
		}
		m.currentTurn = event.Turn

	case engine.EventThinking:
		if text, ok := event.Data.(string); ok {
			n := len(m.messages)
			if n > 0 && m.messages[n-1].Role == "thinking" {
				m.messages[n-1].Content += text
			} else {
				m.messages = append(m.messages, ChatMessage{Role: "thinking", Content: text})
			}
			}

	case engine.EventToolUse:
		if data, ok := event.Data.(map[string]any); ok {
			name, _ := data["name"].(string)
			m.currentToolName = name
			input, _ := data["input"].(string)
			m.messages = append(m.messages, ChatMessage{Role: "tool", Content: fmt.Sprintf("⚙ %s", name), Detail: input})
		}

	case engine.EventToolResult:
		m.currentToolName = ""
		if data, ok := event.Data.(map[string]any); ok {
			content, _ := data["content"].(string)
			isError, _ := data["is_error"].(bool)
			if isError {
				m.messages = append(m.messages, ChatMessage{Role: "error", Content: content})
			} else {
				m.messages = append(m.messages, ChatMessage{
					Role:    "tool",
					Content: truncateStr(content, 200),
					Detail:  content,
				})
			}
		}

	case engine.EventError:
		var s string
		if err, ok := event.Data.(error); ok {
			s = err.Error()
		} else if str, ok := event.Data.(string); ok {
			s = str
		}
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: s})
		m.running = false

	case engine.EventAborted:
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Cancelled]"})
		m.running = false

	case engine.EventDone:
		m.running = false
		m.currentToolName = ""

	case engine.EventTurnEnd:
		m.currentTurn = event.Turn
	}
}

// engineEventMsg wraps an engine event as a tea.Msg.
type engineEventMsg struct {
	event engine.Event
}

// spinnerTickMsg triggers a spinner frame update.
type spinnerTickMsg struct{}

func estimateCost(model string, inTokens, outTokens int) float64 {
	// Approximate costs per 1M tokens (USD)
	rates := map[string]struct{ in, out float64 }{
		// Anthropic
		"claude-sonnet-4-20250514": {3.0, 15.0},
		"claude-opus-4-20250514":   {15.0, 75.0},
		"claude-haiku-3-5":         {0.8, 4.0},
		// OpenAI
		"gpt-4o":     {2.5, 10.0},
		"gpt-4o-mini": {0.15, 0.6},
		// DeepSeek
		"deepseek-chat":     {0.27, 1.10},
		"deepseek-reasoner": {0.55, 2.19},
	}
	for prefix, rate := range rates {
		if len(model) >= len(prefix) && model[:len(prefix)] == prefix {
			return float64(inTokens)/1e6*rate.in + float64(outTokens)/1e6*rate.out
		}
	}
	// Default: assume ~$3/$15 per 1M tokens
	return float64(inTokens)/1e6*3.0 + float64(outTokens)/1e6*15.0
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func renderMessage(msg ChatMessage) string {
	switch msg.Role {
	case "user":
		return UserMsgStyle.Render("▸ ") + AssistantTextStyle.Render(msg.Content)
	case "assistant":
		// Left border accent for assistant blocks (1.4)
		rendered := util.RenderMarkdown(msg.Content, 0)
		if rendered != "" {
			return AssistantBorderStyle.Render("│") + " " + AssistantTextStyle.Render(rendered)
		}
		return ""
	case "tool":
		s := AssistantToolStyle.Render(msg.Content)
		if msg.Detail != "" {
			display := renderToolDetail(msg.Content, msg.Detail)
			s += "\n" + ToolResultStyle.Render(display)
		}
		return s
	case "thinking":
		return ThinkingStyle.Render("[thinking] " + truncateStr(msg.Content, 80))
	case "error":
		return ErrorStyle.Render("✖ " + msg.Content)
	case "system":
		return HelpStyle.Render("· " + msg.Content)
	default:
		return msg.Content
	}
}

// renderToolDetail renders tool detail with diff-style coloring (1.5).
func renderToolDetail(toolLine, detail string) string {
	// Check if it looks like a diff (Edit result)
	if strings.Contains(toolLine, "Edit") || strings.Contains(toolLine, "edit") {
		var out strings.Builder
		for _, line := range strings.Split(detail, "\n") {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "+") {
				out.WriteString(DiffAddedStyle.Render(line))
			} else if strings.HasPrefix(trimmed, "-") {
				out.WriteString(DiffRemovedStyle.Render(line))
			} else if strings.HasPrefix(trimmed, "@@") {
				out.WriteString(DiffHunkStyle.Render(line))
			} else {
				out.WriteString(MutedStyle(line))
			}
			out.WriteString("\n")
		}
		return strings.TrimRight(out.String(), "\n")
	}
	return truncateStr(detail, 100)
}

func renderPermDialog(toolName, summary string, selected int, width int) string {
	opts := []string{"Allow Once", "Always Allow", "Deny", "Always Deny"}
	// Dynamic width: use 60% of screen width, max 72, min 40
	dialogW := width * 60 / 100
	if dialogW > 72 {
		dialogW = 72
	}
	if dialogW < 40 {
		dialogW = 40
	}
	title := " Permission Required "
	var b strings.Builder
	// Top border
	b.WriteString("┌" + strings.Repeat("─", dialogW-2) + "┐\n")
	// Title
	b.WriteString(fmt.Sprintf("│ %-*s │\n", dialogW-4, title))
	// Tool name
	b.WriteString(fmt.Sprintf("│ Tool: %-*s │\n", dialogW-10, toolName))
	// Summary (truncated to fit)
	summaryLine := truncateStr(summary, dialogW-6)
	b.WriteString(fmt.Sprintf("│ %-*s │\n", dialogW-4, summaryLine))
	// Separator
	b.WriteString("│" + strings.Repeat("─", dialogW-2) + "│\n")
	// Options
	for i, opt := range opts {
		cursor := "  "
		if i == selected {
			cursor = "▶ "
		}
		line := cursor + opt
		b.WriteString(fmt.Sprintf("│ %-*s │\n", dialogW-4, line))
	}
	// Bottom border
	b.WriteString("└" + strings.Repeat("─", dialogW-2) + "┘")
	return PermissionDialogStyle.Render(b.String())
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (expand with Enter)"
}
