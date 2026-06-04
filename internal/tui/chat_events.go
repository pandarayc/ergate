package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/session"
)

// Update handles messages for the chat page.
func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.input.SetWidth(msg.Width - 4)
		m.syncViewportHeight()
		return m, nil

	case tea.KeyMsg:
		// Handle special keys; regular typing falls through to InputArea.
		consumed, cmd := m.handleKeyMsg(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if consumed {
			return m, tea.Batch(cmds...)
		}

	case tea.MouseMsg:
		newM, cmd := m.handleMouseMsg(msg, &cmds)
		m = newM
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case engineEventMsg:
		m.handleEngineEvent(msg.event)
		if m.coalesceDirty && len(m.coalesceText) >= 2048 {
			m.flushCoalesced()
		}
		if !m.running {
			in, out := m.eng.TotalUsage()
			m.totalInTokens = in
			m.totalOutTokens = out
			m.saveSession()
		}
		if m.running {
			cmds = append(cmds, m.listenEvents())
		}

	case spinnerTickMsg:
		if m.running {
			m.spinnerIdx = (m.spinnerIdx + 1) % len(spinnerFrames)
			cmds = append(cmds, nextSpinnerTick())
		}
		if m.coalesceDirty {
			m.flushCoalesced()
		}
	}

	// Forward to InputArea (handles textarea input + select mode).
	updateInput := true
	if _, ok := msg.(tea.MouseMsg); ok {
		updateInput = false
	}
	if updateInput {
		newInput, cmd := m.input.Update(msg)
		m.input = newInput
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.syncViewportHeight()
	}

	if cmd := m.syncMouse(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleKeyMsg handles special keys. Returns (consumed=true) when the key was handled
// and should NOT be forwarded to InputArea. Returns (consumed=false) for regular typing.
func (m *ChatModel) handleKeyMsg(msg tea.KeyMsg) (consumed bool, cmd tea.Cmd) {
	var cmds []tea.Cmd

	switch msg.Type {
	case tea.KeyCtrlC:
		// Settled selection: clear highlight, don't quit.
		if m.copyMode.IsActive() {
			m.copyMode.Cancel()
			return true, nil
		}
		if m.running {
			if m.cancel != nil {
				m.cancel()
			}
			m.running = false
			m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Interrupted]"})
			return true, nil
		}
		// Clear input text first; quit only when already empty.
		if strings.TrimSpace(m.input.Value()) != "" {
			m.input.Reset()
			return true, nil
		}
		m.saveSession()
		return true, tea.Quit

	case tea.KeyEsc:
		// Settled selection: clear highlight, don't quit.
		if m.copyMode.IsActive() {
			m.copyMode.Cancel()
			return true, nil
		}
		if m.running {
			if m.cancel != nil {
				m.cancel()
			}
			m.running = false
			m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Interrupted]"})
			return true, nil
		}
		// ESC no longer quits the program — only interrupts running tasks.
		return true, nil

	case tea.KeyCtrlJ:
		if !m.running {
			m.input.InsertString("\n")
		}
		return true, nil

	case tea.KeyEnter:
		if msg.Alt {
			if !m.running {
				m.input.InsertString("\n")
			}
			return true, nil
		}
		if m.running {
			return true, nil
		}
		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			return true, nil
		}

		if strings.HasPrefix(input, "/") {
			m.handleCommand(input)
			m.input.Reset()
			return true, nil
		}

		// Ensure previous engine goroutine has fully exited
		select {
		case <-m.engineDone:
		default:
			return true, nil
		}

		m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
		m.input.AddHistory(input)
		m.running = true
		m.input.Reset()

		ctx, cancel := context.WithCancel(context.Background())
		m.ctx = ctx
		m.cancel = cancel
		ch := make(chan engine.Event, 128)
		m.eventChan = ch
		m.engineDone = make(chan struct{})
		go func() {
			defer cancel()
			defer close(m.engineDone)
			_ = m.eng.Run(ctx, input, ch)
		}()
		cmds = append(cmds, m.listenEvents(), nextSpinnerTick())
		return true, tea.Batch(cmds...)

	case tea.KeyUp:
		// Navigate history only when cursor is at first line; otherwise move cursor.
		if m.input.CursorLine() == 0 {
			if !m.running {
				m.input.PrevHistory()
			}
			return true, nil
		}
		return false, nil

	case tea.KeyDown:
		// Navigate history only when cursor is at last line; otherwise move cursor.
		if m.input.CursorLine() == m.input.LineCount()-1 {
			if !m.running {
				m.input.NextHistory()
			}
			return true, nil
		}
		return false, nil

	case tea.KeyCtrlP:
		debugf("PrevHistory: running=%v", m.running)
		if !m.running {
			m.input.PrevHistory()
		}
		return true, nil

	case tea.KeyCtrlN:
		debugf("NextHistory: running=%v", m.running)
		if !m.running {
			m.input.NextHistory()
		}
		return true, nil

	case tea.KeyPgUp:
		m.viewport.HalfPageUp()
		return true, nil
	case tea.KeyPgDown:
		m.viewport.HalfPageDown()
		return true, nil
	}

	debugf("Key unhandled: type=%v runes=%q", msg.Type, string(msg.Runes))
	return false, nil
}

func (m ChatModel) handleMouseMsg(msg tea.MouseMsg, cmds *[]tea.Cmd) (ChatModel, tea.Cmd) {
	debugf("Mouse: action=%v button=%v x=%d y=%d copyMode=%v vpHeight=%d", msg.Action, msg.Button, msg.X, msg.Y, m.copyMode.IsActive(), m.viewport.Height)

	// Coarse hit test: is the mouse in the viewport (content) area?
	// Footer (toolbar, input, spacer) starts at viewport.Height.
	inViewport := msg.Y >= 0 && msg.Y < m.viewport.Height

	// Wheel scrolling — only in viewport area.
	if msg.Button == tea.MouseButtonWheelUp {
		if inViewport {
			m.viewport.ScrollUp(3)
		}
		return m, nil
	}
	if msg.Button == tea.MouseButtonWheelDown {
		if inViewport {
			m.viewport.ScrollDown(3)
		}
		return m, nil
	}

	// Left button press → enter copy mode only in viewport area.
	if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
		if inViewport {
			m.copyMode.Enter(msg.X, msg.Y, m.viewport.YOffset)
			debugf("copyMode enter: x=%d y=%d anchorY=%d", msg.X, msg.Y, m.copyMode.anchorY)
		}
		return m, nil
	}

	// Motion during copy mode → track drag.
	if msg.Action == tea.MouseActionMotion && m.copyMode.IsActive() {
		m.copyMode.Track(msg.X, msg.Y, m.viewport.YOffset)
		debugf("copyMode track: x=%d y=%d focusY=%d", msg.X, msg.Y, m.copyMode.focusY)
		return m, nil
	}

	// Left button release: resolve drag vs click.
	if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
		wasDrag := m.copyMode.focusX >= 0
		if wasDrag {
			dx := m.copyMode.focusX - m.copyMode.anchorX
			dy := m.copyMode.focusY - m.copyMode.anchorY
			if dx < 0 {
				dx = -dx
			}
			if dy < 0 {
				dy = -dy
			}
			wasDrag = dy > 0 || dx > 1
		}
		debugf("copyMode release: anchorY=%d focusY=%d wasDrag=%v", m.copyMode.anchorY, m.copyMode.focusY, wasDrag)
		if wasDrag {
			text := m.copyMode.Finish()
			debugf("copyMode finish: textLen=%d", len(text))
			if text != "" {
				copyToClipboard(text)
			}
			return m, nil
		}
		m.copyMode.Cancel()

		// Plain click — dispatch by region.
		if !inViewport {
			if m.handleToolBarClick(msg.Y) {
				*cmds = append(*cmds, m.syncMouse())
				return m, tea.Batch(*cmds...)
			}
			return m, nil
		}
		if m.handleViewportClick(msg.Y) {
			*cmds = append(*cmds, m.syncMouse())
			return m, tea.Batch(*cmds...)
		}
		return m, nil
	}

	return m, nil
}

// handleCommand processes slash commands.
func (m *ChatModel) handleCommand(input string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "/exit", "/quit":
		m.saveSession()
		// quitting signal will be handled by AppModel
	case "/clear":
		m.eng.Clear()
		m.messages = nil
	case "/save":
		m.saveSession()
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "Session saved."})
	case "/resume":
		if m.sessionStore == nil {
			m.messages = append(m.messages, ChatMessage{Role: "error", Content: "No session store available."})
			break
		}
		// Save current session first
		m.saveSession()
		
		sessID := ""
		if len(parts) > 1 {
			sessID = parts[1]
		}
		
		var sess *session.Session
		var err error
		if sessID != "" {
			sess, err = m.sessionStore.Load(sessID)
		} else {
			sess, err = m.sessionStore.Latest()
		}
		
		if err != nil {
			m.messages = append(m.messages, ChatMessage{Role: "error", Content: fmt.Sprintf("Resume failed: %v", err)})
		} else if sess == nil {
			m.messages = append(m.messages, ChatMessage{Role: "system", Content: "No saved sessions to resume."})
		} else {
			m.eng.ImportSession(engine.SessionData{Messages: sess.Messages, Usage: sess.Usage})
			m.messages = []ChatMessage{
				{Role: "system", Content: fmt.Sprintf("[Resumed session: %s - %d messages]", sess.ID, len(sess.Messages))},
			}
			m.messages = append(m.messages, convertMessages(m.eng.Messages())...)
			m.sessionID = sess.ID
			m.totalInTokens = sess.Usage.InputTokens
			m.totalOutTokens = sess.Usage.OutputTokens
			m.forceScrollBottom = true
		}
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
	case "/thinking":
		m.hideThinking = !m.hideThinking
		state := "visible"
		if m.hideThinking {
			state = "hidden"
		}
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: fmt.Sprintf("Thinking output: %s", state)})
	case "/help":
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "/help /exit /clear /model /usage /config /save /load /resume /sessions /cost /status /thinking"})
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

// Engine events

func (m *ChatModel) handleEngineEvent(event engine.Event) {
	switch event.Type {
	case engine.EventText:
		if text, ok := event.Data.(string); ok {
		if m.coalesceDirty && m.coalesceRole != "assistant" {
			m.flushCoalesced()
		}
		m.coalesceText += text
		m.coalesceRole = "assistant"
		m.coalesceDirty = true
		}
		m.currentTurn = event.Turn

	case engine.EventThinking:
		if m.hideThinking {
			break
		}
		if text, ok := event.Data.(string); ok {
		if m.coalesceDirty && m.coalesceRole != "thinking" {
			m.flushCoalesced()
		}
		m.coalesceText += text
		m.coalesceRole = "thinking"
		m.coalesceDirty = true
		}

	case engine.EventToolUse:
		m.flushCoalesced()
		if data, ok := event.Data.(map[string]any); ok {
		name, _ := data["name"].(string)
		m.currentToolName = name
		input, _ := data["input"].(string)
		m.messages = append(m.messages, ChatMessage{Role: "tool", Content: fmt.Sprintf("⚙ %s", name), Detail: input})
		}
		m.refreshToolsBar()

	case engine.EventToolResult:
		m.flushCoalesced()
		m.currentToolName = ""
		m.refreshToolsBar()
		if data, ok := event.Data.(map[string]any); ok {
		content, _ := data["content"].(string)
		isError, _ := data["is_error"].(bool)
		if isError {
			m.messages = append(m.messages, ChatMessage{Role: "error", Content: content})
		} else {
			m.messages = append(m.messages, ChatMessage{
				Role: "tool", Content: content, Detail: content,
			})
		}
		}

	case engine.EventError:
		m.flushCoalesced()
		var s string
		if err, ok := event.Data.(error); ok {
		s = err.Error()
		} else if str, ok := event.Data.(string); ok {
		s = str
		}
		m.messages = append(m.messages, ChatMessage{Role: "error", Content: s})
		m.running = false

	case engine.EventAborted:
		m.flushCoalesced()
		m.messages = append(m.messages, ChatMessage{Role: "system", Content: "[Cancelled]"})
		m.running = false
		m.refreshToolsBar()

	case engine.EventDone:
		m.flushCoalesced()
		m.running = false
		m.currentToolName = ""
		m.refreshToolsBar()

	case engine.EventTurnEnd:
		m.flushCoalesced()
		m.currentTurn = event.Turn
		m.refreshToolsBar()
	}
}

func (m *ChatModel) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventChan
		if !ok {
		return engineEventMsg{event: engine.Event{Type: engine.EventDone}}
		}
		return engineEventMsg{event: event}
	}
}

// flushCoalesced writes buffered streaming deltas to messages.
func (m *ChatModel) flushCoalesced() {
	if !m.coalesceDirty {
		return
	}
	text := m.coalesceText
	m.coalesceText = ""
	m.coalesceDirty = false

	n := len(m.messages)
	if n > 0 && m.messages[n-1].Role == m.coalesceRole {
		m.messages[n-1].Content += text
		m.messages[n-1].dirty = true
		m.messages[n-1].rendered = ""
	} else {
		m.messages = append(m.messages, ChatMessage{Role: m.coalesceRole, Content: text, dirty: true})
	}
}

// Tool output fold
const maxToolOutputLines = 8
const maxThinkingLines = 4

func foldToolOutput(content string, width int, maxLines int) (display string, collapsed bool, totalLines int) {
	if maxLines <= 0 {
		maxLines = maxToolOutputLines
	}
	totalLines = visualLineCount(content, width)
	if totalLines <= maxLines {
		return content, false, totalLines
	}
	lines := strings.Split(content, "\n")
	visual := 0
	cut := 0
	for i, line := range lines {
		lineWidth := len([]rune(line))
		if lineWidth == 0 {
		visual++
		} else {
		visual += (lineWidth + width - 1) / width
		}
		if visual >= maxLines-1 {
		cut = i + 1
		break
		}
	}
	if cut == 0 || cut > len(lines) {
		cut = len(lines)
	}
	display = strings.Join(lines[:cut], "\n")
	return display, true, totalLines
}

// handleViewportClick toggles the fold state of a message widget at mouseY.
// mouseY is the terminal row (0-indexed), viewport starts at terminal row 0.
// Uses layout.Content (populated by renderContent) for hit testing.
func (m *ChatModel) handleViewportClick(mouseY int) bool {
	if mouseY < 0 || mouseY >= m.viewport.Height {
		return false
	}
	if m.layout.ContentHeight == 0 {
		return false // layout not yet computed this cycle
	}

	contentY := mouseY + m.viewport.YOffset
	if contentY >= m.layout.ContentHeight {
		return false
	}

	for i := range m.layout.Content {
		w := &m.layout.Content[i]
		if contentY < w.Y || contentY >= w.Y+w.Height {
		continue
		}
		if w.Kind != WidgetMessage || w.Index < 0 || w.Index >= len(m.messages) {
		continue
		}
		msg := &m.messages[w.Index]
		msg.Collapsed = !msg.Collapsed
		msg.dirty = true
		msg.rendered = ""
		debugf("click toggle: msg[%d] role=%s collapsed=%v yOff=%d", w.Index, msg.Role, msg.Collapsed, m.viewport.YOffset)
		return true
	}
	return false
}

// handleToolBarClick processes clicks on the toolsbar using layout.Footer widgets.
func (m *ChatModel) handleToolBarClick(mouseY int) bool {
	for i := range m.layout.Footer {
		w := &m.layout.Footer[i]
		if mouseY < w.Y || mouseY >= w.Y+w.Height {
		continue
		}
		// Fold toggle row.
		if w.Index < 0 {
		m.toolsBar.ToggleExpand()
		m.syncViewportHeight()
		return true
		}
		// Toolbar item click.
		if w.Index >= 0 && w.Index < len(m.toolsBar.Items) {
		item := m.toolsBar.Items[w.Index]
		if item.Expand != "" {
			return true
		}
		}
		return false
	}
	return false
}

func (m *ChatModel) syncMouse() tea.Cmd {
	if m.mouseDisabled {
		m.mouseDisabled = false
		return tea.EnableMouseCellMotion
	}
	return nil
}
