package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/tui/list"
	"github.com/raydraw/ergate/internal/tui/message"
)

// ChatModel is the TUI chat model using list.List + message.ChatMessage.
type ChatModel struct {
	cfg *config.Config
	eng *engine.Engine

	// Core UI components.
	msgList  *list.List
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

	// Overlay support — reuses existing OverlayManager from overlay.go.
	overlays OverlayManager

	// Popup detail overlay (tool output, thinking).
	detailModel *DetailModel

	// Spinner.
	spinnerIdx int

	// Copy mode for text selection (reuses existing CopyMode).
	copyMode CopyMode

	// View dirty flag to skip prewrapContent when content hasn't changed.
	viewDirty     bool
	cachedWrapped string
}

// NewChatModel creates a new chat model.
func NewChatModel(cfg *config.Config, eng *engine.Engine) *ChatModel {
	engineDone := make(chan struct{})
	close(engineDone)

	return &ChatModel{
		cfg:        cfg,
		eng:        eng,
		msgList:    list.New(80, 20),
		input:      NewInputArea(),
		toolsBar:   ToolsBar{},
		engineDone: engineDone,
		stream:     message.NewStreamBuffer(),
	}
}

// Init initializes the model.
func (m *ChatModel) Init() tea.Cmd {
	return tea.Batch(
		nextSpinnerTick(),
		m.input.Blink(),
	)
}

// Update routes messages and handles user input.
func (m *ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
	if m.detailModel != nil {
		return m.handleDetailMsg(msg)
	}
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
func (m *ChatModel) handleOverlayEvent(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case OverlayDetail, OverlayToolChain:
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
		}
	case tea.MouseMsg:
		switch o.Kind {
		case OverlayDetail, OverlayToolChain:
			m.handleOverlayMouse(o, msg)
		}
		return m, nil
	}
	return m, nil
}


// handleDetailMsg routes keyboard/mouse events to the detail popup model.
// Non-input messages (WindowSize, etc.) pass through.
func (m *ChatModel) handleDetailMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg, tea.MouseMsg:
		// Route to detailModel only; block propagation.
		newModel, cmd := m.detailModel.Update(msg)
		m.detailModel = newModel.(*DetailModel)
		return m, cmd
	case DetailCloseMsg:
		m.detailModel = nil
		m.viewDirty = true
		return m, nil
	}
	// Non-input messages (WindowSize, etc.) pass through to chat.
	return m, nil
}

// handleOverlayMouse handles mouse events for copy mode within detail/toolchain overlays.
func (m *ChatModel) handleOverlayMouse(o *Overlay, msg tea.MouseMsg) {
	contentLines := strings.Split(o.DetailContent, "\n")
	if len(contentLines) == 0 {
		return
	}

	// Map screen Y to content-line index accounting for ContentStartY + DetailScroll.
	detailY := msg.Y - o.ContentStartY
	if detailY < 0 {
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			o.CopyActive = false
		}
		return
	}
	contentIdx := detailY + o.DetailScroll
	if contentIdx >= len(contentLines) {
		return
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if o.DetailScroll > 0 {
			o.DetailScroll--
		}
	case tea.MouseButtonWheelDown:
		o.DetailScroll++
	case tea.MouseButtonLeft:
		switch msg.Action {
		case tea.MouseActionPress:
			o.CopyActive = true
			o.CopyAnchorY = contentIdx
			o.CopyFocusY = contentIdx
		case tea.MouseActionMotion:
			if o.CopyActive {
				o.CopyFocusY = contentIdx
			}
		case tea.MouseActionRelease:
			if o.CopyActive {
				o.CopyActive = false
				start := o.CopyAnchorY
				end := o.CopyFocusY
				if start > end {
					start, end = end, start
				}
				if end < len(contentLines) {
					text := strings.Join(contentLines[start:end+1], "\n")
					if text != "" {
						copyToClipboard(text)
					}
				}
			}
		}
	}
}


// LoadSession loads a session by ID, clearing the current chat state
// and importing the session into the engine.
func (m *ChatModel) LoadSession(id string) {
	if err := m.eng.LoadSession(id); err != nil {
		m.appendMessage(message.New("error", fmt.Sprintf("load session %s: %v", id, err)))
		return
	}
	// Clear current chat state before importing.
	m.msgList.SetItems(nil)
	sessID := m.eng.SessionID()
	m.appendMessage(message.New("system",
		fmt.Sprintf("[Restored session: %s]", sessID)))
	for _, msg := range m.eng.Messages() {
		m.appendConvertedMessages(msg)
	}
	m.forceScrollBottom = true
}


// lastMsg returns the last message or nil.
func (m *ChatModel) lastMsg() *message.ChatMessage {
	if m.msgList.ItemCount() == 0 {
		return nil
	}
	return m.msgList.ItemAt(m.msgList.ItemCount() - 1).(*message.ChatMessage)
}

// msgCount returns the number of messages.
func (m *ChatModel) msgCount() int { return m.msgList.ItemCount() }

// View renders the full TUI.
func (m *ChatModel) View() string {
	// Scroll to bottom before rendering so the cached content matches
	// the visible scroll position. Must happen before msgList.Render().
	if m.forceScrollBottom && m.msgCount() > 0 {
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
	if m.msgCount() == 0 {
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
		Turn:       m.currentTurn,
		TotalIn:    m.engTotalIn(),
		TotalOut:   m.engTotalOut(),
		Model:      m.cfg.Model,
		CacheRatio: m.eng.CacheRatio(),
		SessionID:  m.eng.SessionID(),
		Running:    m.running,
	}
	bottom.WriteString(accentBar + sb.View())

	result := lipgloss.JoinVertical(lipgloss.Left, wrapped, bottom.String())

	// Composite overlay on top.
	if m.overlays.IsActive() {
		o := m.overlays.Active()
		switch o.Kind {
		case OverlayPermission:
			permView := renderPermissionOverlay(o, m.width)
			return lipgloss.JoinVertical(lipgloss.Left, wrapped, permView)

		}
	}

	// Detail popup (centered overlay via PlaceOverlay).
	if m.detailModel != nil {
		overlay := m.detailModel.View()
		return lipgloss.Place(m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			overlay,
			lipgloss.WithWhitespaceChars("░"),
			lipgloss.WithWhitespaceForeground(Subtle),
		)
	}

		return result
}

// welcomeView renders the initial welcome screen.
func (m *ChatModel) welcomeView() string {
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
func (m *ChatModel) viewportHeight() int {
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

// renderModalOverlay renders a centered detail/toolchain popup with dimmed
// chat content as background. TUI lacks z-layering, so we truncate the
// chat to the top portion, show the popup centered, and dim background lines.
func (m *ChatModel) renderModalOverlay(o *Overlay, wrapped, statusBar string) string {
	detailView := renderDetailOverlay(o, m.width, m.height/2)
	detailLines := strings.Split(detailView, "\n")
	detailH := len(detailLines)

	statusLines := strings.Split(statusBar, "\n")
	statusH := len(statusLines)

	totalH := m.height
	// If popup is taller than viewport, just show it alone.
	if detailH >= totalH-1 {
		o.ContentStartY = 0 // no offset needed, entire screen is popup
		return detailView
	}

	// Available space above and below popup.
	gap := totalH - detailH - statusH
	aboveH := gap / 2

	// Add popup's screen Y offset to body position for copy mode.
	o.ContentStartY += aboveH
	belowH := gap - aboveH

	dimStyle := ChatDimStyle

	// Build top section: last `aboveH` lines of chat, dimmed.
	chatLines := strings.Split(wrapped, "\n")
	var topLines []string
	if len(chatLines) > aboveH {
		topLines = chatLines[len(chatLines)-aboveH:]
	} else {
		pad := aboveH - len(chatLines)
		for range pad {
			topLines = append(topLines, "")
		}
		topLines = append(topLines, chatLines...)
	}
	// Dim the background chat lines.
	for i, l := range topLines {
		topLines[i] = dimStyle.Render(l)
	}

	// Build bottom section: blank padding + status bar.
	var bottomLines []string
	for range belowH {
		bottomLines = append(bottomLines, "")
	}
	bottomLines = append(bottomLines, statusLines...)

	// Assemble: top (dimmed chat) + popup + bottom (padding + status).
	var all []string
	all = append(all, topLines...)
	all = append(all, detailLines...)
	all = append(all, bottomLines...)

	return strings.Join(all, "\n")
}

// overlayReservedHeight returns the number of rows reserved for an inline overlay.
func (m *ChatModel) overlayReservedHeight() int {
	if !m.overlays.IsActive() {
		return 0
	}
	o := m.overlays.Active()
	if o.Kind == OverlayPermission {
		return 8 // permission dialog fixed height
	}
	if o.Kind == OverlayToolChain {
		return m.height / 2
	}
	return 0 // detail/session picker are modals rendered outside viewport
}

// handleKey processes keyboard input.
func (m *ChatModel) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
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
			cmd := m.handleCommand(input)
			m.input.Reset()
			return true, cmd
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
func (m *ChatModel) contentY(terminalY int) int {
	return terminalY - 2 // Ergate title (1 row) + model line (1 row)
}

// handleMouse processes mouse events — reuses CopyMode for text selection.
func (m *ChatModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
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
func (m *ChatModel) handleItemClick(x, y int) tea.Cmd {
	item, itemY := m.msgList.ItemAtPosition(x, y)
	if item == nil {
		return nil
	}
	if msg, ok := item.(*message.ChatMessage); ok {
		switch msg.Role {
		case "toolchain":
			return m.openToolChainOverlayFor(msg)
		case "thinking":
			m.detailModel = NewDetailModel("[thinking]", msg.Content, m.width, m.height)
			m.viewDirty = true
			return nil
		}
	}
	if handler, ok := item.(list.MouseHandler); ok {
		if handler.HandleMouseClick(list.MouseButtonLeft, x, itemY) {
			return nil
		}
	}
	return nil
}

// startRun begins a new engine turn. Returns the commands to listen for events.
func (m *ChatModel) startRun(input string) tea.Cmd {
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
func (m *ChatModel) cancelRun() {
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
}

// appendMessage adds a message to the list and updates the list.
func (m *ChatModel) appendMessage(msg *message.ChatMessage) {
	m.msgList.AppendItems(msg)
	m.viewDirty = true
}

// flushStream writes the buffered streaming text to a message.
func (m *ChatModel) flushStream() {
	text, role := m.stream.Flush()
	if text == "" {
		return
	}
	// Append to existing message if it matches role, otherwise create new.
	if last := m.lastMsg(); last != nil && last.Role == role {
		last.AppendContent(text)
		m.msgList.UpdateItem(m.msgCount() - 1)
		m.viewDirty = true
		return
	}
	msg := message.New(role, text)
	m.appendMessage(msg)
}

// handleEngineEvent dispatches engine events into the message list.
func (m *ChatModel) handleEngineEvent(event engine.Event) {
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
		if data, ok := event.Data.(map[string]any); ok {
			name, _ := data["name"].(string)
			m.currentToolName = name
		}
		m.refreshToolsBar()

	case engine.EventToolResult:
		m.flushStream()
		m.currentToolName = ""
		m.refreshToolsBar()

	case engine.EventToolChain:
		m.flushStream()
		m.currentToolName = ""
		if data, ok := event.Data.(map[string]any); ok {
			msg := message.NewToolChain(data)
			if msg != nil {
				m.appendMessage(msg)
			}
		}
		m.refreshToolsBar()
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
		if last := m.lastMsg(); last != nil {
			last.Finish()
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
func (m *ChatModel) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventChan
		if !ok {
			return engineEventMsg{event: engine.Event{Type: engine.EventDone}}
		}
		return engineEventMsg{event: event}
	}
}

// saveSession delegates to the engine for session persistence.
func (m *ChatModel) saveSession() {
	m.eng.SaveSession()
}

// handleCommand processes slash commands. Migrated from chat_events.go:314-417.
func (m *ChatModel) handleCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}
	switch parts[0] {
	case "/exit", "/quit":
		m.saveSession()
	case "/clear":
		m.eng.Clear()
		m.msgList.SetItems(nil)
	case "/save":
		m.saveSession()
		m.appendMessage(message.New("system", "Session saved."))
	case "/resume":
		return m.handleResume(parts)
	case "/load":
		return m.handleLoad(parts)
	case "/sessions":
		return func() tea.Msg {
			return SwitchToSessionPageMsg{}
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
			m.cfg.Model, len(msgs), in, out, m.eng.SessionID(),
		)))
	default:
		m.appendMessage(message.New("system", fmt.Sprintf("Unknown command: %s", parts[0])))
	}
	return nil
}

func formatToolChainDetail(detail string) string {
	var items []struct {
		Name    string `json:"name"`
		Input   string `json:"input"`
		Content string `json:"content"`
		IsError bool   `json:"is_error"`
	}
	if json.Unmarshal([]byte(detail), &items) != nil {
		return detail
	}
	var b strings.Builder
	sep := strings.Repeat("─", 40)
	for i, it := range items {
		status := "✓"
		if it.IsError {
			status = "✗"
		}
		b.WriteString(fmt.Sprintf("── %s %s %s\n", it.Name, status, sep))
		b.WriteString(it.Content)
		if i < len(items)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

// openToolChainOverlay opens the most recent tool chain as a pop layer.
func (m *ChatModel) openToolChainOverlay() tea.Cmd {
	for i := m.msgList.ItemCount() - 1; i >= 0; i-- {
		msg := m.msgList.ItemAt(i).(*message.ChatMessage)
		if msg.Role == "toolchain" {
			return m.openToolChainOverlayFor(msg)
		}
	}
	return nil
}

// openToolChainOverlayFor opens the pop layer for a specific tool chain message.
func (m *ChatModel) openToolChainOverlayFor(msg *message.ChatMessage) tea.Cmd {
	title := msg.ChainSummary
	if title == "" {
		title = "Tool Chain"
	}
	detailContent := formatToolChainDetail(msg.Detail)
	if detailContent == "" {
		detailContent = msg.Content
	}
	m.detailModel = NewDetailModel(title, detailContent, m.width, m.height)
	m.viewDirty = true
	return nil
}


func (m *ChatModel) handleResume(parts []string) tea.Cmd {
	if m.eng == nil {
		m.appendMessage(message.New("error", "Session service not available."))
		return nil
	}
	m.saveSession()
	return func() tea.Msg {
		return SwitchToSessionPageMsg{}
	}
}

func (m *ChatModel) handleLoad(parts []string) tea.Cmd {
	if m.eng == nil || len(parts) < 2 {
		return nil
	}
	m.LoadSession(parts[1])
	return nil
}

// appendConvertedMessages converts an engine-level message to TUI messages and appends them.
func (m *ChatModel) appendConvertedMessages(msg llm.Message) {
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
func (m *ChatModel) engTotalIn() int {
	in, _ := m.eng.TotalUsage()
	return in
}

// engTotalOut returns total output tokens from the engine.
func (m *ChatModel) engTotalOut() int {
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
