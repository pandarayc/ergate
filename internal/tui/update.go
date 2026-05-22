package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/engine"
)

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 7
		m.input.SetWidth(msg.Width - 4)
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

	// Update input when not running — skip Enter (handled above)
	updateInput := !m.running && !m.permActive
	if updateInput {
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

	case engine.EventToolResult:
		m.flushCoalesced()
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

	case engine.EventDone:
		m.flushCoalesced()
		m.running = false
		m.currentToolName = ""

	case engine.EventTurnEnd:
		m.flushCoalesced()
		m.currentTurn = event.Turn
	}
}
