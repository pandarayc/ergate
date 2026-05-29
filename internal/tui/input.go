package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// InputMode is the input state: free typing or selecting from options.
type InputMode int

const (
	InputEdit   InputMode = iota // free text input
	InputSelect                  // option selection
)

// SelectOption is a single choice in selection mode.
type SelectOption struct {
	Label       string
	Description string
	Value       any
}

// SelectConfig configures a selection session.
type SelectConfig struct {
	Options  []SelectOption
	OnSelect func(selected []SelectOption) tea.Cmd // called when user confirms
	OnCancel func() tea.Cmd                        // called when user cancels
}

// InputArea wraps a textarea with dual edit/select mode.
type InputArea struct {
	textarea textarea.Model

	mode       InputMode
	selectOpts []SelectOption
	selectIdx  int
	selectCfg  SelectConfig

	// History
	history []string
	histIdx int
}

// NewInputArea creates an InputArea.
func NewInputArea() InputArea {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.ShowLineNumbers = false
	ta.MaxHeight = 5
	ta.SetPromptFunc(0, func(lineIdx int) string { return "" })
	ta.SetHeight(1)
	ta.Focus()
	return InputArea{textarea: ta, mode: InputEdit}
}

// Update handles messages for the input area.
func (ia InputArea) Update(msg tea.Msg) (InputArea, tea.Cmd) {
	if ia.mode == InputSelect {
		return ia.updateSelect(msg)
	}
	return ia.updateEdit(msg)
}

// View renders the input area.
func (ia InputArea) View() string {
	if ia.mode == InputSelect {
		return ia.viewSelect()
	}
	return ia.textarea.View()
}

// SetWidth sets the textarea width.
func (ia *InputArea) SetWidth(w int) {
	ia.textarea.SetWidth(w)
}

// LineCount returns the visual line count of the input (1-5).
func (ia InputArea) LineCount() int {
	lc := ia.textarea.LineCount()
	if lc < 1 {
		lc = 1
	}
	if lc > 5 {
		lc = 5
	}
	return lc
}

// SyncHeight adjusts textarea height to match visual line count.
func (ia *InputArea) SyncHeight() {
	lc := ia.LineCount()
	ia.textarea.SetHeight(lc)
}

// CursorLine returns the current cursor line (0-indexed).
func (ia InputArea) CursorLine() int {
	return ia.textarea.Line()
}

// Value returns the current textarea content.
func (ia InputArea) Value() string {
	return ia.textarea.Value()
}

// SetValue sets the textarea content.
func (ia *InputArea) SetValue(s string) {
	ia.textarea.SetValue(s)
}

// Reset clears the textarea.
func (ia *InputArea) Reset() {
	ia.textarea.Reset()
}

// InsertString inserts text at cursor.
func (ia *InputArea) InsertString(s string) {
	ia.textarea.InsertString(s)
}

// Blink returns the cursor blink command.
func (ia InputArea) Blink() tea.Cmd {
	return textarea.Blink
}

// Mode returns the current input mode.
func (ia InputArea) Mode() InputMode {
	return ia.mode
}

// EnterSelect switches to selection mode. Textarea content is preserved.
func (ia *InputArea) EnterSelect(cfg SelectConfig) {
	ia.mode = InputSelect
	ia.selectOpts = cfg.Options
	ia.selectIdx = 0
	ia.selectCfg = cfg
}

// ExitSelect returns to edit mode.
func (ia *InputArea) ExitSelect() {
	ia.mode = InputEdit
	ia.selectOpts = nil
	ia.selectCfg = SelectConfig{}
}

// History

// AddHistory appends a string to input history.
func (ia *InputArea) AddHistory(s string) {
	ia.history = append(ia.history, s)
	ia.histIdx = len(ia.history)
}

// PrevHistory returns the previous history entry.
func (ia *InputArea) PrevHistory() {
	if len(ia.history) == 0 {
		return
	}
	if ia.histIdx > 0 {
		ia.histIdx--
	}
	ia.textarea.SetValue(ia.history[ia.histIdx])
}

// NextHistory returns the next history entry.
func (ia *InputArea) NextHistory() {
	if len(ia.history) == 0 {
		return
	}
	if ia.histIdx < len(ia.history)-1 {
		ia.histIdx++
		ia.textarea.SetValue(ia.history[ia.histIdx])
	} else {
		ia.histIdx = len(ia.history)
		ia.textarea.Reset()
	}
}

// Select mode internals

func (ia InputArea) updateEdit(msg tea.Msg) (InputArea, tea.Cmd) {
	newTA, cmd := ia.textarea.Update(msg)
	ia.textarea = newTA
	lc := ia.textarea.LineCount()
	if lc < 1 {
		lc = 1
	}
	if lc > 5 {
		lc = 5
	}
	ia.textarea.SetHeight(lc)
	return ia, cmd
}

func (ia InputArea) updateSelect(msg tea.Msg) (InputArea, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyUp:
			if ia.selectIdx > 0 {
				ia.selectIdx--
			}
		case tea.KeyDown:
			if ia.selectIdx < len(ia.selectOpts)-1 {
				ia.selectIdx++
			}
		case tea.KeyEnter:
			sel := []SelectOption{ia.selectOpts[ia.selectIdx]}
			ia.ExitSelect()
			if ia.selectCfg.OnSelect != nil {
				return ia, ia.selectCfg.OnSelect(sel)
			}
		case tea.KeyEsc:
			ia.ExitSelect()
			if ia.selectCfg.OnCancel != nil {
				return ia, ia.selectCfg.OnCancel()
			}
		}
	}
	return ia, nil
}

func (ia InputArea) viewSelect() string {
	// TODO: proper styled select view — placeholder for now
	var b strings.Builder
	b.WriteString("Select mode:\n")
	for i, opt := range ia.selectOpts {
		cursor := "  "
		if i == ia.selectIdx {
			cursor = "▶ "
		}
		b.WriteString(cursor + opt.Label + "\n")
	}
	return b.String()
}
