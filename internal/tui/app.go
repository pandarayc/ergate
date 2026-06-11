package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/session"
)

// Page identifies the active page in the page-shell architecture.
type Page int

const (
	PageChat Page = iota
	PageSession
)

// AppModel is the top-level application model (page router).
//
// Architecture:
//
//	AppModel (page router)
//	├─ SessionPage  — full-screen session browser (for -r flag, /resume command)
//	└─ ChatPage     — chat main interface with overlays (Permission, Detail only)
//
// Shared state (sessionStore, width, height) lives here so both
// pages have access without coupling.
type AppModel struct {
	page        Page
	sessionPage SessionPage
	chatPage    *ChatModel

	// Shared state.
	sessionStore *session.Store
	sessionID    string
	width        int
	height       int
}

// NewAppModel creates the top-level application model.
//
// If resume is true and sessions exist on disk, the app starts on the session
// browser page instead of the chat page.
func NewAppModel(cfg *config.Config, eng *engine.Engine, store *session.Store, resume bool) AppModel {
	chatPage := NewChatModel(cfg, eng, store)

	startPage := PageChat
	var sessionPage SessionPage
	if resume && store != nil {
		if sessions, err := store.List(); err == nil && len(sessions) > 0 {
			startPage = PageSession
			sessionPage = NewSessionPage(store, 0, 0)
		}
	}

	return AppModel{
		page:         startPage,
		sessionPage:  sessionPage,
		chatPage:     chatPage,
		sessionStore: store,
	}
}

// Init delegates to the chat page.
func (m *AppModel) Init() tea.Cmd {
	return m.chatPage.Init()
}

// Update routes messages to the active page and handles global messages.
func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Broadcast to both pages.
		m.sessionPage.width = msg.Width
		m.sessionPage.height = msg.Height
		// ChatPage always gets WindowSize to keep internal layout correct.
		newChat, cmd := m.chatPage.Update(msg)
		m.chatPage = newChat.(*ChatModel)
		return m, cmd

	case SwitchToSessionPageMsg:
		m.page = PageSession
		m.sessionPage = NewSessionPage(m.sessionStore, m.width, m.height)
		return m, nil

	case SwitchToChatPageMsg:
		if msg.SessionID != "" {
			m.chatPage.LoadSession(msg.SessionID)
		}
		m.page = PageChat
		return m, nil
	}

	// Route to the current page.
	switch m.page {
	case PageSession:
		var cmd tea.Cmd
		m.sessionPage, cmd = m.sessionPage.Update(msg)
		return m, cmd
	case PageChat:
		return m.chatPage.Update(msg)
	}
	return m, nil
}

// View renders the active page.
func (m *AppModel) View() string {
	switch m.page {
	case PageSession:
		return m.sessionPage.View()
	case PageChat:
		return m.chatPage.View()
	}
	return ""
}
