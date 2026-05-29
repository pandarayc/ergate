package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Screen identifies the current top-level page.
type Screen int

const (
	ScreenChat Screen = iota
	// Future: ScreenAgentTeam
)

// AppModel is the top-level model. It manages screen routing and overlay state.
type AppModel struct {
	screen  Screen
	chat    ChatModel
	overlay OverlayManager
	width   int
	height  int
}

// NewAppModel creates the top-level application model.
func NewAppModel(chat ChatModel) AppModel {
	return AppModel{
		screen: ScreenChat,
		chat:   chat,
	}
}

// Init initializes the app.
func (m AppModel) Init() tea.Cmd {
	return m.chat.Init()
}

// Update handles messages by routing to the active screen or overlay.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// WindowSize is always handled at the top level.
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = wsm.Width
		m.height = wsm.Height
		// Pass through to chat.
		newChat, cmd := m.chat.Update(msg)
		m.chat = newChat
		return &m, cmd
	}

	// Overlay active → only overlay gets events (blocking).
	if m.overlay.IsActive() {
		return m.handleOverlayEvent(msg)
	}

	debugf("AppModel.Update: %T", msg)
	// Route to current screen.
	switch m.screen {
	case ScreenChat:
		newChat, cmd := m.chat.Update(msg)
		m.chat = newChat
		return &m, cmd
	}
	return &m, nil
}

// View renders the full UI.
func (m *AppModel) View() string {
	// Set inline overlay height for chat layout.
	if m.overlay.IsActive() && m.overlay.Active().Kind == OverlayPermission {
		m.chat.SetOverlayHeight(8)
	} else {
		m.chat.SetOverlayHeight(0)
	}

	base := m.chat.View()

	// Composite overlay on top.
	if m.overlay.IsActive() {
		switch m.overlay.Active().Kind {
		case OverlayPermission:
			// Permission is rendered inline in the footer — already accounted for by overlayHeight.
			return base
		case OverlayDetail:
			// Detail is a modal overlay.
			detailView := renderDetailOverlay(m.overlay.Active(), m.width, m.height)
			return lipgloss.JoinVertical(lipgloss.Left, m.chat.viewport.View(), detailView)
		}
	}
	return base
}

// handleOverlayEvent routes messages to the active overlay handler.
func (m AppModel) handleOverlayEvent(msg tea.Msg) (tea.Model, tea.Cmd) {
	o := m.overlay.Active()

	switch msg := msg.(type) {
	case tea.KeyMsg:
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
				m.overlay.Hide()
			}

		case OverlayDetail:
			if o.DetailSearchMode {
				handleDetailSearchKey(o, msg)
				return &m, nil
			}
			switch msg.Type {
			case tea.KeyEsc:
				m.overlay.Hide()
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
					m.overlay.Hide()
				}
			}
			// j/k for scroll
			if len(msg.Runes) == 1 {
				switch msg.Runes[0] {
				case 'j':
					o.DetailScroll++
				case 'k':
					o.DetailScroll--
				}
			}
		}
	case tea.MouseMsg:
		// Mouse events are blocked while overlay is active.
		return &m, nil
	}

	return &m, nil
}

// ShowOverlay activates an overlay.
func (m *AppModel) ShowOverlay(o *Overlay) {
	m.overlay.Show(o)
}

