package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/engine"
)

// BUG(mouse): scroll/click/copy broken in terminal. Events arrive but render doesn't reflect state changes.
// Last known good: pre-fold-refactoring (Task #70). Mouse handling at line ~157.
// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// Overlay key handling (blocks normal input)
		if m.overlay != nil {
			m.handleOverlayKey(msg)
			cmds = append(cmds, m.syncMouse())
			return m, tea.Batch(cmds...)
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

		case tea.KeyCtrlJ:
			if !m.running {
				m.input.InsertString("\n")
			}
			return m, nil

		case tea.KeyEnter:
			if msg.Alt {
				if !m.running {
					m.input.InsertString("\n")
				}
				return m, nil
			}
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

			// Ensure previous engine goroutine has fully exited
			select {
			case <-m.engineDone:
			default:
				return m, nil
			}

			m.messages = append(m.messages, ChatMessage{Role: "user", Content: input})
			m.inputHistory = append(m.inputHistory, input)
			m.historyIdx = len(m.inputHistory)
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
		if m.coalesceDirty && len(m.coalesceText) >= 2048 {
			m.flushCoalesced()
		}
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
			cmds = append(cmds, nextSpinnerTick())
		}
		if m.coalesceDirty {
			m.flushCoalesced()
		}
	}

	// Mouse events (handled outside switch).
	if mouse, ok := msg.(tea.MouseMsg); ok {
		if m.overlay != nil {
			return m, nil
		}
		// Click handling
		if mouse.Action == tea.MouseActionRelease && mouse.Button == tea.MouseButtonLeft {
			if m.handleToolBarClick(mouse.Y) {
				cmds = append(cmds, m.syncMouse())
				return m, tea.Batch(cmds...)
			}
			if m.handleViewportClick(mouse.Y) {
				cmds = append(cmds, m.syncMouse())
				return m, tea.Batch(cmds...)
			}
		}
		// Wheel scrolling
		if mouse.Button == tea.MouseButtonWheelUp {
			m.viewport.ScrollUp(3)
		}
		if mouse.Button == tea.MouseButtonWheelDown {
			m.viewport.ScrollDown(3)
		}
	}

	// Update input — skip during overlay; skip mouse events; skip Enter (handled above).
	updateInput := m.overlay == nil
	if updateInput {
		if _, ok := msg.(tea.MouseMsg); ok {
			updateInput = false // never forward mouse events to textarea
		}
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyEnter && !keyMsg.Alt {
			updateInput = false
		}
	}
	if updateInput {
		newInput, cmd := m.input.Update(msg)
		m.input = newInput
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.syncInputHeight()
		m.syncViewportHeight()
	}

	if cmd := m.syncMouse(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
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

func (m *Model) handleOverlayKey(msg tea.KeyMsg) {
	o := m.overlay
	switch o.Kind {
	case OverlayPermission:
		switch msg.Type {
		case tea.KeyUp:
			if o.Selected > 0 {
				o.Selected--
			}
		case tea.KeyDown:
			if o.Selected < 3 {
				o.Selected++
			}
		case tea.KeyEnter, tea.KeyEsc:
			m.overlay = nil
		}

	case OverlayDetail:
		if o.DetailSearchMode {
			m.handleDetailSearchKey(msg)
			return
		}
		switch msg.Type {
		case tea.KeyEsc:
			m.overlay = nil
		case tea.KeyUp, tea.KeyDown:
			if msg.Type == tea.KeyUp {
				o.DetailScroll--
			} else {
				o.DetailScroll++
			}
		case tea.KeyPgUp:
			o.DetailScroll -= 10
		case tea.KeyPgDown:
			o.DetailScroll += 10
		case tea.KeyRunes:
			if len(msg.Runes) == 1 && msg.Runes[0] == '/' {
				o.DetailSearchMode = true
				o.DetailSearchQ = ""
				o.DetailMatches = nil
				o.DetailMatchIdx = 0
				o.DetailMatchOff = 0
			} else if len(msg.Runes) == 1 && msg.Runes[0] == 'q' {
				m.overlay = nil
			}
		case tea.KeySpace:
			// 'j' for down, 'k' for up — handled via Runes
		}
		// Handle j/k via Runes fallback
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'j':
				o.DetailScroll++
			case 'k':
				o.DetailScroll--
			}
		}
	}
}

func (m *Model) handleDetailSearchKey(msg tea.KeyMsg) {
	o := m.overlay
	switch msg.Type {
	case tea.KeyEsc:
		o.DetailSearchMode = false
		o.DetailSearchQ = ""
		o.DetailMatches = nil

	case tea.KeyEnter:
		// Jump to selected match
		if o.DetailMatchIdx >= 0 && o.DetailMatchIdx < len(o.DetailMatches) {
			o.DetailScroll = o.DetailMatches[o.DetailMatchIdx].Line
			o.DetailSearchMode = false
		}

	case tea.KeyCtrlP:
		if len(o.DetailMatches) > 0 {
			if o.DetailMatchIdx > 0 {
				o.DetailMatchIdx--
			} else {
				o.DetailMatchIdx = len(o.DetailMatches) - 1
			}
			// Keep selected match in dropdown view
			if o.DetailMatchIdx < o.DetailMatchOff {
				o.DetailMatchOff = o.DetailMatchIdx
			}
		}

	case tea.KeyCtrlN:
		if len(o.DetailMatches) > 0 {
			if o.DetailMatchIdx < len(o.DetailMatches)-1 {
				o.DetailMatchIdx++
			} else {
				o.DetailMatchIdx = 0
			}
			if o.DetailMatchIdx >= o.DetailMatchOff+detailDropdownN {
				o.DetailMatchOff = o.DetailMatchIdx - detailDropdownN + 1
			}
		}

	case tea.KeyBackspace:
		if len(o.DetailSearchQ) > 0 {
			o.DetailSearchQ = o.DetailSearchQ[:len(o.DetailSearchQ)-1]
			o.DetailMatches = detailSearch(o.DetailContent, o.DetailSearchQ)
			o.DetailMatchIdx = 0
			o.DetailMatchOff = 0
		}

	case tea.KeyRunes:
		o.DetailSearchQ += string(msg.Runes)
		o.DetailMatches = detailSearch(o.DetailContent, o.DetailSearchQ)
		o.DetailMatchIdx = 0
		o.DetailMatchOff = 0
	}
}

const maxToolOutputLines = 8

// foldToolOutput decides how to display tool output.
// If content exceeds maxToolOutputLines visual lines, it's collapsed.
// width is the available column width for line wrapping calculation.
func foldToolOutput(content string, width int) (display string, collapsed bool, totalLines int) {
	totalLines = visualLineCount(content, width)
	if totalLines <= maxToolOutputLines {
		return content, false, totalLines
	}
	// Build display: show enough physical lines to fit ~7 visual lines.
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
		if visual >= maxToolOutputLines-1 {
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

// handleViewportClick processes a left-click in the viewport (message area).
// Returns true if the click was consumed.
func (m *Model) handleViewportClick(mouseY int) bool {
	vpStartY := 2 // header = 2 lines
	vpEndY := 2 + m.viewport.Height - 1
	if mouseY < vpStartY || mouseY > vpEndY {
		return false
	}
	// Click in viewport — toggle expand/collapse from bottom up.
	// Expand: click on a collapsed fold indicator → show full content.
	// Collapse: click on an expanded message → fold it back.
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].Collapsed {
			m.messages[i].Collapsed = false
			m.messages[i].rendered = ""
			return true
		}
	}
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].wasFolded && !m.messages[i].Collapsed {
			m.messages[i].Collapsed = true
			m.messages[i].rendered = ""
			return true
		}
	}
	return false
}

// handleToolBarClick processes a left-click on the toolsbar area.
// Returns true if the click was consumed (stop further processing).
func (m *Model) handleToolBarClick(mouseY int) bool {
	if len(m.toolsBar.Items) == 0 {
		return false
	}

	// Header occupies lines 0-1. Viewport starts at line 2.
	tbStartY := 2 + m.viewport.Height
	tbHeight := m.toolsBar.Height()
	tbEndY := tbStartY + tbHeight - 1

	if mouseY < tbStartY || mouseY > tbEndY {
		return false // click outside toolsbar
	}

	idx := mouseY - tbStartY
	collapsed := !m.toolsBar.Expanded && len(m.toolsBar.Items) > maxToolBarLines

	// Fold line clicked?
	if collapsed && idx == maxToolBarLines-1 {
		m.toolsBar.ToggleExpand()
		m.syncViewportHeight()
		return true
	}

	// Item with Expand content clicked?
	if idx >= 0 && idx < len(m.toolsBar.Items) {
		item := m.toolsBar.Items[idx]
		if item.Expand != "" {
			m.overlay = &Overlay{
				Kind:          OverlayDetail,
				DetailTitle:   item.Icon + " " + item.Label,
				DetailContent: item.Expand,
			}
			return true
		}
	}

	return false
}

func (m *Model) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventChan
		if !ok {
			return engineEventMsg{event: engine.Event{Type: engine.EventDone}}
		}
		return engineEventMsg{event: event}
	}
}

func (m *Model) handleEngineEvent(event engine.Event) {
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
