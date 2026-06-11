package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/raydraw/ergate/internal/session"
)

// SessionItem holds session metadata for the picker list.
type SessionItem struct {
	ID           string
	UpdatedAt    time.Time
	MessageCount int
	Model        string
}

// SessionAction is the result of user interaction with the session page.
type SessionAction int

const (
	SessionActionNone SessionAction = iota
	SessionActionSelect
	SessionActionNew
)

// SwitchToSessionPageMsg tells AppModel to switch to the session browser.
type SwitchToSessionPageMsg struct{}

// SwitchToChatPageMsg tells AppModel to switch to the chat page.
// If SessionID is non-empty, the chat page loads that session.
type SwitchToChatPageMsg struct {
	SessionID string
}

// SessionPage is the full-screen session browser page.
type SessionPage struct {
	store  *session.Store
	items  []SessionItem
	cursor int
	scroll int
	width  int
	height int
}

// NewSessionPage creates a new session page and loads session items.
func NewSessionPage(store *session.Store, width, height int) SessionPage {
	sp := SessionPage{
		store:  store,
		width:  width,
		height: height,
	}
	sp.LoadItems()
	return sp
}

// LoadItems refreshes the session list from the store.
func (sp *SessionPage) LoadItems() {
	if sp.store == nil {
		sp.items = nil
		return
	}
	ids, err := sp.store.List()
	if err != nil {
		sp.items = nil
		return
	}
	sp.items = make([]SessionItem, 0, len(ids))
	for _, id := range ids {
		sess, err := sp.store.Load(id)
		if err != nil {
			continue
		}
		sp.items = append(sp.items, SessionItem{
			ID:           sess.ID,
			UpdatedAt:    sess.UpdatedAt,
			MessageCount: len(sess.Messages),
			Model:        sess.Model,
		})
	}
	// Clamp cursor after reload.
	if sp.cursor >= len(sp.items) {
		sp.cursor = len(sp.items) - 1
	}
	if sp.cursor < 0 && len(sp.items) > 0 {
		sp.cursor = 0
	}
}

// View renders the full-screen session browser.
func (sp SessionPage) View() string {
	if sp.width == 0 || sp.height == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// --- Header ---
	titleStyle := lipgloss.NewStyle().Foreground(Accent).Bold(true)
	b.WriteString(titleStyle.Render("Sessions"))

	hintStyle := lipgloss.NewStyle().Foreground(BorderDim)
	b.WriteString(hintStyle.Render("                  n new  d delete"))
	b.WriteString("\n\n")

	// --- List area ---
	headerLines := 3 // title + blank line
	hintsLines := 2  // blank + hints
	listHeight := sp.height - headerLines - hintsLines
	if listHeight < 1 {
		listHeight = 1
	}

	// Auto-scroll to keep cursor visible.
	if sp.cursor < sp.scroll {
		sp.scroll = sp.cursor
	}
	if sp.cursor >= sp.scroll+listHeight {
		sp.scroll = sp.cursor - listHeight + 1
	}
	if sp.scroll < 0 {
		sp.scroll = 0
	}

	cursorStyle := lipgloss.NewStyle().Foreground(TextColor).Background(Accent).Bold(true)
	normalStyle := lipgloss.NewStyle().Foreground(TextColor)
	mutedStyle := lipgloss.NewStyle().Foreground(Muted)
	dimStyle := lipgloss.NewStyle().Foreground(BorderDim)

	end := sp.scroll + listHeight
	if end > len(sp.items) {
		end = len(sp.items)
	}

	for i := sp.scroll; i < end; i++ {
		item := sp.items[i]
		line := fmt.Sprintf(" %s  %s  %s",
			item.UpdatedAt.Format("01-02 15:04"),
			dimStyle.Render(fmt.Sprintf("%d msgs", item.MessageCount)),
			mutedStyle.Render(item.Model),
		)
		if i == sp.cursor {
			b.WriteString(cursorStyle.Render(line))
		} else {
			b.WriteString(normalStyle.Render(line))
		}
		b.WriteString("\n")
	}

	// Empty state.
	if len(sp.items) == 0 {
		emptyMsg := "  No saved sessions. Press n to start a new one."
		b.WriteString(mutedStyle.Render(emptyMsg))
		b.WriteString("\n")
		for i := 0; i < listHeight-1; i++ {
			b.WriteString("\n")
		}
	} else {
		used := end - sp.scroll
		for i := used; i < listHeight; i++ {
			b.WriteString("\n")
		}
	}

	// --- Bottom hints ---
	b.WriteString("\n")
	b.WriteString(hintStyle.Render("  ↑↓ navigate  Enter select  Esc back"))

	return b.String()
}

// Update handles keyboard events for the session page.
func (sp SessionPage) Update(msg tea.Msg) (SessionPage, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		sp.width = msg.Width
		sp.height = msg.Height

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyUp:
			if sp.cursor > 0 {
				sp.cursor--
			}

		case tea.KeyDown:
			if sp.cursor < len(sp.items)-1 {
				sp.cursor++
			}

		case tea.KeyEnter:
			if sp.cursor >= 0 && sp.cursor < len(sp.items) {
				return sp, func() tea.Msg {
					return SwitchToChatPageMsg{SessionID: sp.items[sp.cursor].ID}
				}
			}

		case tea.KeyEsc:
			return sp, func() tea.Msg {
				return SwitchToChatPageMsg{}
			}

		case tea.KeyRunes:
			if len(msg.Runes) != 1 {
				break
			}
			switch msg.Runes[0] {
			case 'n', 'N':
				return sp, func() tea.Msg {
					return SwitchToChatPageMsg{}
				}
			case 'd', 'D':
				if sp.cursor >= 0 && sp.cursor < len(sp.items) && sp.store != nil {
					id := sp.items[sp.cursor].ID
					_ = sp.store.Delete(id)
					sp.LoadItems()
					if sp.cursor >= len(sp.items) {
						sp.cursor = len(sp.items) - 1
					}
					if sp.cursor < 0 && len(sp.items) > 0 {
						sp.cursor = 0
					}
				}
			}
		}
	}
	return sp, nil
}
