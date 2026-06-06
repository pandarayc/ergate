package tui

import tea "github.com/charmbracelet/bubbletea"

// AppModel is the top-level application model wrapping AppModelV2.
type AppModel struct {
	chat *AppModelV2
}

// NewAppModel creates the top-level application model.
func NewAppModel(chat *AppModelV2) AppModel {
	return AppModel{chat: chat}
}

// Init initializes the app.
func (m AppModel) Init() tea.Cmd {
	return m.chat.Init()
}

// Update routes messages to the chat model.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	newChat, cmd := m.chat.Update(msg)
	m.chat = newChat.(*AppModelV2)
	return m, cmd
}

// View renders the full UI.
func (m AppModel) View() string {
	return m.chat.View()
}
