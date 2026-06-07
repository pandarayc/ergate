package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/session"
	"github.com/raydraw/ergate/internal/tui/list"
	"github.com/raydraw/ergate/internal/tui/message"
)

// AppModelV2 is the new TUI model using list.List + message.ChatMessage.
// It replaces the viewport.Model + WidgetLayout architecture.
type AppModelV2 struct {
	cfg *config.Config
	eng *engine.Engine

	// Core UI components.
	msgList  *list.List
	messages []*message.ChatMessage
	input    InputArea
	toolsBar ToolsBar

	// Engine state.
	running         bool
	currentTurn     int
	currentToolName string
	width           int
	height          int

	// Engine communication.
	eventChan  chan engine.Event
	engineDone chan struct{}
	ctx        context.Context
	cancel     context.CancelFunc

	// Streaming delta buffer.
	stream *message.StreamBuffer

	// Scroll.
	forceScrollBottom bool
	hideThinking      bool

	// Session persistence.
	sessionStore *session.Store
	sessionID    string

	// Overlay support — reuses existing OverlayManager from overlay.go.
	overlays             OverlayManager
	pendingSessionPicker bool // set by NewAppModelV2 when -r flag is used

	// Spinner.
	spinnerIdx int

	// Copy mode for text selection (reuses existing CopyMode).
	copyMode CopyMode

	// View dirty flag to skip prewrapContent when content hasn't changed.
	viewDirty     bool
	cachedWrapped string
}

// NewAppModelV2 creates the new-style application model.
// If resume is true and sessions exist, the session picker will be shown on first render.
func NewAppModelV2(cfg *config.Config, eng *engine.Engine, store *session.Store, resume bool) *AppModelV2 {
	engineDone := make(chan struct{})
	close(engineDone)

	m := &AppModelV2{
		cfg:          cfg,
		eng:          eng,
		msgList:      list.New(80, 20),
		messages:     make([]*message.ChatMessage, 0),
		input:        NewInputArea(),
		toolsBar:     ToolsBar{},
		engineDone:   engineDone,
		stream:       message.NewStreamBuffer(),
		sessionStore: store,
	}

	if resume && store != nil {
		if sessions, err := store.List(); err == nil && len(sessions) > 0 {
			// Defer picker to first View() call when dimensions are known.
			m.pendingSessionPicker = true
		}
	}

	return m
}

// Init initializes the model.
func (m *AppModelV2) Init() tea.Cmd {
	return tea.Batch(
		nextSpinnerTick(),
		m.input.Blink(),
	)
}

// Update routes messages and handles user input.
func (m *AppModelV2) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// WindowSize always handled.
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsm.Width
		m.height = wsm.Height
		m.msgList.SetSize(wsm.Width, m.viewportHeight())
		m.input.SetWidth(wsm.Width - 4)
		m.viewDirty = true // width change requires re-wrap
		return m, nil
	}

	// Overlay active — only overlay gets events (blocking).
	if m.overlays.IsActive() {
		return m.handleOverlayEvent(msg)
	}

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		consumed, cmd := m.handleKey(msg)
		if consumed {
			return m, cmd
		}
		newInput, inputCmd := m.input.Update(msg)
		m.input = newInput
		return m, inputCmd

	case tea.MouseMsg:
		cmd := m.handleMouse(msg)
		return m, cmd

	case engineEventMsg:
		m.handleEngineEvent(msg.event)
		if m.running {
			cmds = append(cmds, m.listenEvents())
		}

	case spinnerTickMsg:
		m.spinnerIdx++
		if m.running {
			cmds = append(cmds, nextSpinnerTick())
		}
		if m.stream.IsDirty() {
			m.flushStream()
		}
	}

	return m, tea.Batch(cmds...)
}

// handleOverlayEvent routes keyboard events to the active overlay.
func (m *AppModelV2) handleOverlayEvent(msg tea.Msg) (tea.Model, tea.Cmd) {
	o := m.overlays.Active()
	if o == nil {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch o.Kind {
		case OverlayPermission:
			if o.Selected > 0 && msg.Type == tea.KeyUp {
				o.Selected--
			}
			if o.Selected < 3 && msg.Type == tea.KeyDown {
				o.Selected++
			}
			if msg.Type == tea.KeyEnter || msg.Type == tea.KeyEsc {
				m.overlays.Hide()
			}
		case OverlayDetail:
			if msg.Type == tea.KeyEsc || (len(msg.Runes) == 1 && msg.Runes[0] == 'q') {
				m.overlays.Hide()
			}
			if msg.Type == tea.KeyUp || (len(msg.Runes) == 1 && msg.Runes[0] == 'k') {
				if o.DetailScroll > 0 {
					o.DetailScroll--
				}
			}
			if msg.Type == tea.KeyDown || (len(msg.Runes) == 1 && msg.Runes[0] == 'j') {
				o.DetailScroll++
			}
			if msg.Type == tea.KeyPgUp {
				o.DetailScroll -= 10
			}
			if msg.Type == tea.KeyPgDown {
				o.DetailScroll += 10
			}
			if len(msg.Runes) == 1 && msg.Runes[0] == '/' {
				o.DetailSearchMode = true
				o.DetailSearchQ = ""
				o.DetailMatches = nil
				o.DetailMatchIdx = 0
				o.DetailMatchOff = 0
			}
		case OverlaySessionPicker:
			if msg.Type == tea.KeyEsc {
				m.dismissSessionPicker()
			}
			if o.SessionPickerCursor > 0 && msg.Type == tea.KeyUp {
				o.SessionPickerCursor--
			}
			if o.SessionPickerCursor < len(o.SessionPickerItems)-1 && msg.Type == tea.KeyDown {
				o.SessionPickerCursor++
			}
			if msg.Type == tea.KeyEnter {
				m.selectSessionFromPicker()
			}
		}
	case tea.MouseMsg:
		return m, nil // mouse blocked during overlay
	}
	return m, nil
}

// showSessionPicker displays the session picker overlay on startup.
func (m *AppModelV2) showSessionPicker() {
	items := m.loadSessionPickerData()
	if len(items) == 0 {
		return
	}
	m.overlays.Show(&Overlay{
		Kind:               OverlaySessionPicker,
		SessionPickerItems: items,
		SessionPickerCursor: 0,
		SessionPickerScroll: 0,
	})
}

// dismissSessionPicker hides the session picker overlay.
func (m *AppModelV2) dismissSessionPicker() {
	m.overlays.Hide()
}

// selectSessionFromPicker loads the selected session and hides the picker.
func (m *AppModelV2) selectSessionFromPicker() {
	o := m.overlays.Active()
	if o == nil || o.SessionPickerCursor >= len(o.SessionPickerItems) {
		return
	}
	m.loadSession(o.SessionPickerItems[o.SessionPickerCursor].ID)
	m.overlays.Hide()
}

// loadSessionPickerData returns session items for the picker.
func (m *AppModelV2) loadSessionPickerData() []SessionItem {
	if m.sessionStore == nil {
		return nil
	}
	ids, err := m.sessionStore.List()
	if err != nil {
		return nil
	}
	var items []SessionItem
	for _, id := range ids {
		sess, err := m.sessionStore.Load(id)
		if err != nil {
			continue
		}
		items = append(items, SessionItem{
			ID:           sess.ID,
			UpdatedAt:    sess.UpdatedAt,
			MessageCount: len(sess.Messages),
			Model:        sess.Model,
		})
	}
	return items
}

// loadSession loads a session by ID.
func (m *AppModelV2) loadSession(id string) {
	if m.sessionStore == nil {
		return
	}
	sess, err := m.sessionStore.Load(id)
	if err != nil || sess == nil {
		m.appendMessage(message.New("error", fmt.Sprintf("load session %s: %v", id, err)))
		return
	}
	m.eng.ImportSession(engine.SessionData{
		Messages: sess.Messages,
		Usage:    sess.Usage,
	})
	m.sessionID = sess.ID
	m.appendMessage(message.New("system",
		fmt.Sprintf("[Restored session: %s — %d messages]", sess.ID, len(sess.Messages))))
	for _, msg := range m.eng.Messages() {
		m.appendConvertedMessages(msg)
	}
	m.forceScrollBottom = true
}

// View renders the full TUI.
func (m *AppModelV2) View() string {
	// Show pending session picker (from -r flag).
	if m.pendingSessionPicker && !m.overlays.IsActive() {
		m.pendingSessionPicker = false
		m.showSessionPicker()
	}

	// Scroll to bottom before rendering so the cached content matches
	// the visible scroll position. Must happen before msgList.Render().
	if m.forceScrollBottom && len(m.messages) > 0 {
		m.msgList.ScrollToBottom()
		m.forceScrollBottom = false
		m.viewDirty = true
	}

	var b strings.Builder

	// Header.
	headerStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true).Padding(0, 1)
	b.WriteString(headerStyle.Render("Ergate"))
	b.WriteString(lipgloss.NewStyle().Foreground(Muted).Render(
		fmt.Sprintf("  model: %s\n", m.cfg.Model),
	))

	// Welcome page or content.
	if len(m.messages) == 0 {
		b.WriteString(m.welcomeView())
	} else {
		b.WriteString(m.msgList.Render())
	}

	// Spinner.
	if m.running {
		spinnerText := spinnerFrames[m.spinnerIdx%len(spinnerFrames)] + " Thinking..."
		if m.currentToolName != "" {
			spinnerText = spinnerFrames[m.spinnerIdx%len(spinnerFrames)] + " " + m.currentToolName + "..."
		}
		b.WriteString(SpinnerStyle.Render(spinnerText + "\n"))
	}

	// Sync input height.
	m.input.SyncHeight()

	// Pre-wrap and pad content to viewport height.
	// Cache avoids per-frame string allocations over thousands of lines.
	vpHeight := m.viewportHeight()
	if m.viewDirty {
		contentStr := b.String()
		w := prewrapContent(contentStr, m.width)
		m.copyMode.SetContent(w)
		lines := strings.Split(w, "\n")
		for len(lines) < vpHeight {
			lines = append(lines, "")
		}
		m.cachedWrapped = strings.Join(lines, "\n")
		m.viewDirty = false
	}
	wrapped := m.cachedWrapped
	if m.copyMode.IsActive() {
		wrapped = m.copyMode.Highlight(wrapped)
	}

	// Footer.
	var bottom strings.Builder

	// Tools bar.
	if tb := m.toolsBar.View(m.width); tb != "" {
		bottom.WriteString(tb)
		bottom.WriteString("\n")
	}

	// Spacer.
	bottom.WriteString(lipgloss.NewStyle().Foreground(BorderDim).Render("───"))
	bottom.WriteString("\n")

	// Input area.
	accentBar := lipgloss.NewStyle().Foreground(Accent).Bold(true).Render("┃")
	inputView := InputAreaStyle.Render(m.input.View())
	for i, line := range strings.Split(inputView, "\n") {
		if i > 0 {
			bottom.WriteString("\n")
		}
		bottom.WriteString(accentBar + line)
	}
	bottom.WriteString("\n")

	// Status bar.
	sb := StatusBar{
		Turn:    m.currentTurn,
		TotalIn: m.engTotalIn(),
		TotalOut: m.engTotalOut(),
		Model:   m.cfg.Model,
		CacheRatio: m.eng.CacheRatio(),
		SessionID: m.sessionID,
		Running: m.running,
	}
	bottom.WriteString(accentBar + sb.View())

	result := lipgloss.JoinVertical(lipgloss.Left, wrapped, bottom.String())

	// Composite overlay on top.
	if m.overlays.IsActive() {
		o := m.overlays.Active()
		switch o.Kind {
		case OverlayPermission:
			// Permission is inline — already accounted for by viewport height reduction.
			return result
		case OverlayDetail:
			detailView := renderDetailOverlay(o, m.width, m.height)
			return lipgloss.JoinVertical(lipgloss.Left, wrapped, detailView)
		case OverlaySessionPicker:
			pickerView := renderSessionPicker(o, m.width, m.height)
			return lipgloss.JoinVertical(lipgloss.Left, wrapped, pickerView)
		}
	}

	return result
}

// welcomeView renders the initial welcome screen.
func (m *AppModelV2) welcomeView() string {
	return lipgloss.NewStyle().Foreground(Muted).Padding(1).Render(
		"Welcome to Ergate!\n\n" +
			"  Ctrl+C  Quit\n" +
			"  ↑/↓     Input history\n" +
			"  PgUp/Dn Scroll\n" +
			"  Ctrl+P  Previous input\n" +
			"  Ctrl+N  Next input\n" +
			"\nType a message to start...",
	)
}

// viewportHeight calculates the visible area for the message list.
func (m *AppModelV2) viewportHeight() int {
	if m.height == 0 {
		return 20
	}
	header := 2
	spacer := 1
	stat := 1
	input := m.input.LineCount()
	if input < 1 {
		input = 1
	}
	tbH := m.toolsBar.Height()
	oh := m.overlayReservedHeight()
	return max(m.height-header-spacer-tbH-input-stat-oh, 3)
}

// overlayReservedHeight returns the number of rows reserved for an inline overlay.
func (m *AppModelV2) overlayReservedHeight() int {
	if !m.overlays.IsActive() {
		return 0
	}
	o := m.overlays.Active()
	if o.Kind == OverlayPermission {
		return 8 // permission dialog fixed height
	}
	return 0 // detail/session picker are modals rendered outside viewport
}

// handleKey processes keyboard input.
func (m *AppModelV2) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		if m.copyMode.IsActive() {
			m.copyMode.Cancel()
			return true, nil
		}
		if m.running {
			m.cancelRun()
			return true, nil
		}
		// Not running — quit if input empty, otherwise clear input.
		if m.input.Value() == "" {
			m.saveSession()
			return true, tea.Quit
		}
		m.input.Reset()
		return true, nil

	case tea.KeyEsc:
		if m.copyMode.IsActive() {
			m.copyMode.Cancel()
			return true, nil
		}
		if m.running {
			m.cancelRun()
			return true, nil
		}

	case tea.KeyEnter:
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
		return true, m.startRun(input)

	case tea.KeyPgUp:
		m.msgList.ScrollBy(-10)
		m.viewDirty = true
		return true, nil

	case tea.KeyPgDown:
		m.msgList.ScrollBy(10)
		m.viewDirty = true
		return true, nil

	case tea.KeyUp:
		if m.input.CursorLine() == 0 && !m.running {
			m.input.PrevHistory()
			return true, nil
		}

	case tea.KeyDown:
		if m.input.CursorLine() == m.input.LineCount()-1 && !m.running {
			m.input.NextHistory()
			return true, nil
		}

	case tea.KeyCtrlP:
		if !m.running {
			m.input.PrevHistory()
		}
		return true, nil

	case tea.KeyCtrlN:
		if !m.running {
			m.input.NextHistory()
		}
		return true, nil
	}

	return false, nil
}

// contentY adjusts terminal mouse Y to list-relative Y by subtracting the header.
func (m *AppModelV2) contentY(terminalY int) int {
	return terminalY - 2 // Ergate title (1 row) + model line (1 row)
}

// handleMouse processes mouse events — reuses CopyMode for text selection.
func (m *AppModelV2) handleMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.msgList.ScrollBy(-3)
		if !m.copyMode.IsActive() {
			m.viewDirty = true
		}
		return nil
	case tea.MouseButtonWheelDown:
		m.msgList.ScrollBy(3)
		if !m.copyMode.IsActive() {
			m.viewDirty = true
		}
		return nil
	case tea.MouseButtonLeft:
		switch msg.Action {
		case tea.MouseActionPress:
			// SetContent receives the visible content (not full scrollable content),
			// so terminal Y maps 1:1 to wrapped content line. No offset needed.
			m.copyMode.Enter(msg.X, msg.Y, 0)
			return nil
		case tea.MouseActionMotion:
			if m.copyMode.IsActive() {
				m.copyMode.Track(msg.X, msg.Y, 0)
			}
			return nil
		case tea.MouseActionRelease:
			if m.copyMode.HasSelection() {
				text := m.copyMode.Finish()
				if text != "" {
					copyToClipboard(text)
				}
				return nil
			}
			m.copyMode.Cancel()
			listY := msg.Y - 2
			if listY >= 0 && listY < m.msgList.Height() {
				return m.handleItemClick(msg.X, listY)
			}
			return nil
		}
	}
	return nil
}

// handleItemClick dispatches a click to the list item at the given position.
// x and y are list-relative coordinates (header already subtracted).
func (m *AppModelV2) handleItemClick(x, y int) tea.Cmd {
	item, itemY := m.msgList.ItemAtPosition(x, y)
	if item == nil {
		return nil
	}
	if handler, ok := item.(list.MouseHandler); ok {
		if handler.HandleMouseClick(list.MouseButtonLeft, x, itemY) {
			return nil
		}
	}
	return nil
}

// startRun begins a new engine turn. Returns the commands to listen for events.
func (m *AppModelV2) startRun(input string) tea.Cmd {
	// Add user message.
	userMsg := message.New("user", input)
	userMsg.Finish()
	m.appendMessage(userMsg)

	m.input.AddHistory(input)
	m.input.Reset()
	m.running = true
	m.forceScrollBottom = true

	ch := make(chan engine.Event, 128)
	m.eventChan = ch
	m.engineDone = make(chan struct{})
	m.ctx, m.cancel = context.WithCancel(context.Background())

	go func() {
		defer m.cancel()
		defer close(m.engineDone)
		_ = m.eng.Run(m.ctx, input, ch)
	}()

	return tea.Batch(m.listenEvents(), nextSpinnerTick())
}

// cancelRun interrupts the running engine.
func (m *AppModelV2) cancelRun() {
	if !m.running {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.running = false
	m.currentToolName = ""
	m.appendMessage(message.New("system", "[Interrupted]"))
	m.refreshToolsBar()
}

// refreshToolsBar rebuilds the toolbar state.
func (m *AppModelV2) refreshToolsBar() {
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
}

// appendMessage adds a message to the list and updates the list.
func (m *AppModelV2) appendMessage(msg *message.ChatMessage) {
	m.messages = append(m.messages, msg)
	m.msgList.AppendItems(msg)
	m.viewDirty = true
}

// flushStream writes the buffered streaming text to a message.
func (m *AppModelV2) flushStream() {
	text, role := m.stream.Flush()
	if text == "" {
		return
	}
	// Append to existing message if it matches role, otherwise create new.
	if len(m.messages) > 0 {
		last := m.messages[len(m.messages)-1]
		if last.Role == role {
			last.AppendContent(text)
			m.msgList.UpdateItem(len(m.messages) - 1)
			m.viewDirty = true
			return
		}
	}
	msg := message.New(role, text)
	m.appendMessage(msg)
}

// handleEngineEvent dispatches engine events into the message list.
func (m *AppModelV2) handleEngineEvent(event engine.Event) {
	switch event.Type {
	case engine.EventText:
		if text, ok := event.Data.(string); ok {
			m.stream.Append(text, "assistant")
		}
		m.currentTurn = event.Turn

	case engine.EventThinking:
		if m.hideThinking {
			break
		}
		if text, ok := event.Data.(string); ok {
			m.stream.Append(text, "thinking")
		}

	case engine.EventToolUse:
		m.flushStream()
		if data, ok := event.Data.(map[string]any); ok {
			name, _ := data["name"].(string)
			input, _ := data["input"].(string)
			msg := message.NewTool("⚙ "+name, input)
			m.appendMessage(msg)
			m.currentToolName = name
		}
		m.refreshToolsBar()
		m.forceScrollBottom = true

	case engine.EventToolResult:
		m.flushStream()
		m.currentToolName = ""
		m.refreshToolsBar()
		if data, ok := event.Data.(map[string]any); ok {
			content, _ := data["content"].(string)
			isError, _ := data["is_error"].(bool)
			if isError {
				m.appendMessage(message.New("error", content))
			} else {
				msg := message.NewTool(content, content)
				msg.Finish()
				m.appendMessage(msg)
			}
		}
		m.forceScrollBottom = true

	case engine.EventError:
		m.flushStream()
		var s string
		if err, ok := event.Data.(error); ok {
			s = err.Error()
		} else if str, ok := event.Data.(string); ok {
			s = str
		}
		m.appendMessage(message.New("error", s))
		m.running = false

	case engine.EventAborted:
		m.flushStream()
		m.appendMessage(message.New("system", "[Cancelled]"))
		m.running = false
		m.currentToolName = ""
		m.refreshToolsBar()

	case engine.EventDone:
		m.flushStream()
		if len(m.messages) > 0 {
			m.messages[len(m.messages)-1].Finish()
		}
		m.running = false
		m.currentToolName = ""
		m.refreshToolsBar()

	case engine.EventTurnEnd:
		m.flushStream()
		m.currentTurn = event.Turn
		m.refreshToolsBar()
	}
}

// listenEvents returns a command that reads the next engine event.
func (m *AppModelV2) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventChan
		if !ok {
			return engineEventMsg{event: engine.Event{Type: engine.EventDone}}
		}
		return engineEventMsg{event: event}
	}
}

// saveSession persists the current conversation.
func (m *AppModelV2) saveSession() {
	if m.sessionStore == nil {
		return
	}
	data := m.eng.ExportSession()
	sess := &session.Session{
		ID:       m.sessionID,
		Model:    m.cfg.Model,
		Messages: data.Messages,
		Usage:    data.Usage,
		Turns:    data.Turns,
	}
	if err := m.sessionStore.Save(sess); err == nil {
		m.sessionID = sess.ID
		m.sessionStore.Prune(20)
	}
}

// handleCommand processes slash commands. Migrated from chat_events.go:314-417.
func (m *AppModelV2) handleCommand(input string) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	switch parts[0] {
	case "/exit", "/quit":
		m.saveSession()
	case "/clear":
		m.eng.Clear()
		m.messages = nil
		m.msgList.SetItems(nil)
	case "/save":
		m.saveSession()
		m.appendMessage(message.New("system", "Session saved."))
	case "/resume":
		m.handleResume(parts)
	case "/load":
		m.handleLoad(parts)
	case "/sessions":
		if m.sessionStore != nil {
			ids, _ := m.sessionStore.List()
			m.appendMessage(message.New("system", fmt.Sprintf("Sessions: %v", ids)))
		}
	case "/thinking":
		m.hideThinking = !m.hideThinking
		state := "visible"
		if m.hideThinking {
			state = "hidden"
		}
		m.appendMessage(message.New("system", fmt.Sprintf("Thinking output: %s", state)))
	case "/help":
		m.appendMessage(message.New("system", "/help /exit /clear /model /usage /config /save /load /resume /sessions /cost /status /thinking"))
	case "/model":
		if len(parts) > 1 {
			m.cfg.Model = parts[1]
		}
		m.appendMessage(message.New("system", fmt.Sprintf("Model: %s", m.cfg.Model)))
	case "/usage":
		in, out := m.eng.TotalUsage()
		m.appendMessage(message.New("system", fmt.Sprintf("Tokens — in:%d out:%d total:%d", in, out, in+out)))
	case "/cost":
		in, out := m.eng.TotalUsage()
		opts := m.cfg.ActiveModelOptions()
		cost := estimateCost(m.cfg.Model, in, out, opts)
		m.appendMessage(message.New("system", fmt.Sprintf("Est. cost: $%.4f  (in:%d out:%d)", cost, in, out)))
	case "/config":
		m.appendMessage(message.New("system", fmt.Sprintf(
			"Provider:%s  Model:%s  Permissions:%s  MaxTurns:%d",
			m.cfg.APIProvider, m.cfg.Model, m.cfg.PermissionMode, m.cfg.MaxTurns,
		)))
	case "/status":
		msgs := m.eng.Messages()
		in, out := m.eng.TotalUsage()
		m.appendMessage(message.New("system", fmt.Sprintf(
			"Model:%s  Messages:%d  Tokens(in:%d out:%d)  Session:%s",
			m.cfg.Model, len(msgs), in, out, m.sessionID,
		)))
	default:
		m.appendMessage(message.New("system", fmt.Sprintf("Unknown command: %s", parts[0])))
	}
}

func (m *AppModelV2) handleResume(parts []string) {
	if m.sessionStore == nil {
		m.appendMessage(message.New("error", "No session store available."))
		return
	}
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
		m.appendMessage(message.New("error", fmt.Sprintf("Resume failed: %v", err)))
	} else if sess == nil {
		m.appendMessage(message.New("system", "No saved sessions to resume."))
	} else {
		m.eng.ImportSession(engine.SessionData{Messages: sess.Messages, Usage: sess.Usage})
		m.messages = []*message.ChatMessage{
			message.New("system", fmt.Sprintf("[Resumed session: %s - %d messages]", sess.ID, len(sess.Messages))),
		}
		m.sessionID = sess.ID
		// Convert engine messages to TUI messages.
		for _, msg := range m.eng.Messages() {
			m.appendConvertedMessages(msg)
		}
		m.forceScrollBottom = true
	}
}

func (m *AppModelV2) handleLoad(parts []string) {
	if m.sessionStore == nil || len(parts) < 2 {
		return
	}
	sess, err := m.sessionStore.Load(parts[1])
	if err != nil {
		m.appendMessage(message.New("error", fmt.Sprintf("Load failed: %v", err)))
		return
	}
	m.eng.ImportSession(engine.SessionData{Messages: sess.Messages, Usage: sess.Usage})
	m.messages = []*message.ChatMessage{
		message.New("system", fmt.Sprintf("[Loaded: %s]", parts[1])),
	}
	m.sessionID = parts[1]
	m.msgList.SetItems([]list.Item{m.messages[0]})
}

// appendConvertedMessages converts an engine-level message to TUI messages and appends them.
func (m *AppModelV2) appendConvertedMessages(msg llm.Message) {
	for _, b := range msg.Content {
		switch msg.Role {
		case "user":
			if b.Type == "text" && b.Text != "" {
				m.appendMessage(message.New("user", b.Text))
			}
		case "assistant":
			switch b.Type {
			case "text":
				if b.Text != "" {
					m.appendMessage(message.New("assistant", b.Text))
				}
			case "tool_use":
				m.appendMessage(message.NewTool("⚙ "+b.Name, string(b.Input)))
			case "thinking":
				m.appendMessage(message.New("thinking", b.Thinking))
			}
		case "tool":
			if b.Type == "tool_result" {
				if b.IsError {
					m.appendMessage(message.New("error", string(b.Content)))
				} else {
					content := string(b.Content)
					m.appendMessage(message.NewTool(content, content))
				}
			}
		}
	}
}

// engTotalIn returns total input tokens from the engine.
func (m *AppModelV2) engTotalIn() int {
	in, _ := m.eng.TotalUsage()
	return in
}

// engTotalOut returns total output tokens from the engine.
func (m *AppModelV2) engTotalOut() int {
	_, out := m.eng.TotalUsage()
	return out
}

// --- Event data extraction helpers (mirror chat_events.go logic) ---

func eventString(data any) string {
	if s, ok := data.(string); ok {
		return s
	}
	if b, ok := data.([]byte); ok {
		return string(b)
	}
	return fmt.Sprintf("%v", data)
}

func toolUseData(data any) (name, input string) {
	type toolUseInfo struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	}
	if info, ok := data.(toolUseInfo); ok {
		return info.Name, info.Input
	}
	return "", ""
}

func toolResultData(data any) (content string, isError bool) {
	type toolResultInfo struct {
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if info, ok := data.(toolResultInfo); ok {
		return info.Content, info.IsError
	}
	return eventString(data), false
}
