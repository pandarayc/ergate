package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// DetailCloseMsg is emitted when the user dismisses the detail popup.
type DetailCloseMsg struct{}

// DetailModel is a self-contained popup overlay for viewing detail content
// (tool output, thinking text, etc.). It implements tea.Model.
type DetailModel struct {
	Title   string
	Content string

	// Scroll state.
	scroll    int
	maxScroll int // computed from content lines

	// Search state.
	searchMode  bool
	searchQ     string
	searchMatches []searchMatch
	searchIdx   int
	searchOff   int

	// Copy mode state — character-precise, matching outer CopyMode.
	copyActive   bool
	copyAnchorX  int // visual column within content line (0 = left edge of text area)
	copyAnchorY  int // content line index
	copyFocusX   int
	copyFocusY   int

	// Dimensions set by parent via WindowSizeMsg.
	width  int
	height int

	// bodyStartY is set during View() — the Y offset (within popup) where
	// the scrollable body content begins. Used for mouse coordinate mapping.
	bodyStartY int
}

type searchMatch struct {
	Line int
	Text string
}

const (
	dlgMaxWidth  = 110
	dlgMinWidth  = 50
	dlgMaxHeight = 30
	dlgMinHeight = 10
	dlgDropdownN = 5
)

// NewDetailModel creates a new detail popup.
func NewDetailModel(title, content string, w, h int) *DetailModel {
	return &DetailModel{
		Title:   title,
		Content: content,
		width:   w,
		height:  h,
	}
}

// Bounds returns the actual visual position and size of the popup in terminal
// coordinates. Measures the rendered View() output so border, padding, and
// dynamic content height are all accounted for.
func (m *DetailModel) Bounds(termW, termH int) (x, y, w, h int) {
	view := m.View()
	w = lipgloss.Width(view)
	h = strings.Count(view, "\n") + 1
	x = (termW - w) / 2
	if x < 0 {
		x = 0
	}
	y = (termH - h) / 2
	if y < 0 {
		y = 0
	}
	return
}

// Init implements tea.Model.
func (m *DetailModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *DetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		return m, m.handleKey(msg)

	case tea.MouseMsg:
		m.handleMouse(msg)
	}
	return m, nil
}

// View renders the detail popup as a centered, bordered box.
func (m *DetailModel) View() string {
	w := m.width * 90 / 100
	if w > dlgMaxWidth {
		w = dlgMaxWidth
	}
	if w < dlgMinWidth {
		w = dlgMinWidth
	}
	h := m.height * 90 / 100
	if h > dlgMaxHeight {
		h = dlgMaxHeight
	}
	if h < dlgMinHeight {
		h = dlgMinHeight
	}

	innerW := w - 4

	title := m.renderTitle(innerW)
	searchBlock := m.renderSearch(innerW)
	divider := lipgloss.NewStyle().Foreground(BorderDim).Width(innerW).
		Render(strings.Repeat("─", innerW))

	contentH := h - 2 - strings.Count(title, "\n") - 1 -
		strings.Count(searchBlock, "\n") - 1 - 1 - 1
	if contentH < 3 {
		contentH = 3
	}
	// Track where body starts for mouse coordinate mapping.
	// Layout: top_border(1) + title + search + divider(1) = start of body.
	titleLines := strings.Count(title, "\n") + 1
	searchLines := strings.Count(searchBlock, "\n") + 1
	m.bodyStartY = 2 + titleLines + searchLines

	body := m.renderBody(innerW, contentH)
	hints := m.renderHints(innerW)

	inner := lipgloss.JoinVertical(lipgloss.Left,
		title, searchBlock, divider, body, "", hints,
	)

	return lipgloss.NewStyle().
		Width(w).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Accent).
		Padding(0, 1).
		Render(inner)
}

// --- keyboard handling ---

func (m *DetailModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Search mode: route character keys to search query.
	if m.searchMode {
		return m.handleSearchKey(msg)
	}

	switch msg.Type {
	case tea.KeyEsc:
		return func() tea.Msg { return DetailCloseMsg{} }

	case tea.KeyUp:
		if m.scroll > 0 {
			m.scroll--
		}
	case tea.KeyDown:
		if m.scroll < m.maxScroll {
			m.scroll++
		}
	case tea.KeyPgUp:
		m.scroll -= 10
		if m.scroll < 0 {
			m.scroll = 0
		}
	case tea.KeyPgDown:
		m.scroll += 10
		if m.scroll > m.maxScroll {
			m.scroll = m.maxScroll
		}

	case tea.KeyRunes:
		switch {
		case len(msg.Runes) == 1 && (msg.Runes[0] == 'q' || msg.Runes[0] == 'Q'):
			return func() tea.Msg { return DetailCloseMsg{} }
		case len(msg.Runes) == 1 && msg.Runes[0] == 'k':
			if m.scroll > 0 {
				m.scroll--
			}
		case len(msg.Runes) == 1 && msg.Runes[0] == 'j':
			if m.scroll < m.maxScroll {
				m.scroll++
			}
		case len(msg.Runes) == 1 && msg.Runes[0] == '/':
			m.searchMode = true
			m.searchQ = ""
		}
	}
	return nil
}

func (m *DetailModel) handleSearchKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.searchMode = false
		m.searchQ = ""
		m.searchMatches = nil

	case tea.KeyEnter:
		if m.searchIdx >= 0 && m.searchIdx < len(m.searchMatches) {
			m.scroll = m.searchMatches[m.searchIdx].Line
			m.searchMode = false
		}

	case tea.KeyCtrlP:
		if len(m.searchMatches) > 0 {
			m.searchIdx = (m.searchIdx - 1 + len(m.searchMatches)) % len(m.searchMatches)
			if m.searchIdx < m.searchOff {
				m.searchOff = m.searchIdx
			}
		}

	case tea.KeyCtrlN:
		if len(m.searchMatches) > 0 {
			m.searchIdx = (m.searchIdx + 1) % len(m.searchMatches)
			if m.searchIdx >= m.searchOff+dlgDropdownN {
				m.searchOff = m.searchIdx - dlgDropdownN + 1
			}
		}

	case tea.KeyBackspace:
		if len(m.searchQ) > 0 {
			m.searchQ = m.searchQ[:len(m.searchQ)-1]
			m.updateSearch()
		}

	case tea.KeyRunes:
		// In search mode, typing goes to search query, not scrolling.
		m.searchQ += string(msg.Runes)
		m.updateSearch()
	}
	return nil
}

func (m *DetailModel) updateSearch() {
	m.searchMatches = searchContent(m.Content, m.searchQ)
	m.searchIdx = 0
	m.searchOff = 0
}

// --- mouse handling ---

// visualXToByte maps a visual column position (0-indexed) to a byte offset
// within the given line, accounting for CJK fullwidth characters.
func visualXToByte(line string, targetX int) int {
	if targetX <= 0 {
		return 0
	}
	visualW := 0
	for i, r := range line {
		rw := 1
		if r > 127 {
			rw = lipgloss.Width(string(r))
		}
		if visualW+rw > targetX {
			return i
		}
		visualW += rw
	}
	return len(line)
}

func (m *DetailModel) handleMouse(msg tea.MouseMsg) {
	lines := strings.Split(m.Content, "\n")
	if len(lines) == 0 {
		return
	}

	// Map popup-relative Y to content line using dynamic header height.
	contentY := msg.Y - m.bodyStartY
	if contentY < 0 {
		return
	}
	idx := contentY + m.scroll
	if idx >= len(lines) {
		return
	}
	// Map popup-relative X to visual column within the content area.
	// Content starts after left border(1) + left padding(1) = 2.
	col := msg.X - 2
	if col < 0 {
		col = 0
	}

	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.scroll > 0 {
			m.scroll--
		}
	case tea.MouseButtonWheelDown:
		if m.scroll < m.maxScroll {
			m.scroll++
		}
	case tea.MouseButtonLeft:
		switch msg.Action {
		case tea.MouseActionPress:
			m.copyActive = true
			m.copyAnchorX = col
			m.copyAnchorY = idx
			m.copyFocusX = col
			m.copyFocusY = idx
		case tea.MouseActionMotion:
			if m.copyActive {
				m.copyFocusX = col
				m.copyFocusY = idx
			}
		case tea.MouseActionRelease:
			if m.copyActive {
				m.copyActive = false
				text := m.extractCopyText(lines)
				if text != "" {
					copyToClipboard(text)
				}
			}
		}
	}
}

// extractCopyText extracts character-precise text from the selected region
// using visual column → byte offset mapping.
func (m *DetailModel) extractCopyText(lines []string) string {
	sx, sy, ex, ey := m.copyAnchorX, m.copyAnchorY, m.copyFocusX, m.copyFocusY
	if sy > ey || (sy == ey && sx > ex) {
		sx, sy, ex, ey = ex, ey, sx, sy
	}
	if sy >= len(lines) || ey >= len(lines) {
		return ""
	}

	var result []string
	for i := sy; i <= ey; i++ {
		line := stripAnsi(lines[i])
		if i == sy && i == ey {
			start := visualXToByte(line, sx)
			end := visualXToByte(line, ex+1) // +1 to include character under cursor
			result = append(result, line[start:end])
		} else if i == sy {
			result = append(result, line[visualXToByte(line, sx):])
		} else if i == ey {
			result = append(result, line[:visualXToByte(line, ex+1)])
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// --- rendering ---

func (m *DetailModel) renderTitle(w int) string {
	label := m.Title
	if label == "" {
		label = "Detail"
	}
	return lipgloss.NewStyle().
		Foreground(Accent).Bold(true).
		Width(w).Align(lipgloss.Left).
		Render(" " + label)
}

func (m *DetailModel) renderSearch(w int) string {
	style := lipgloss.NewStyle().Foreground(Muted).Width(w)
	if !m.searchMode {
		return style.Render("  / to search")
	}

	cursor := " "
	if len(m.searchQ)%2 == 0 {
		cursor = "█"
	}
	inputLine := lipgloss.NewStyle().
		Foreground(Info).Background(BgPanel).Width(w).
		Render(" > " + m.searchQ + cursor)

	if len(m.searchMatches) == 0 {
		if m.searchQ != "" {
			return inputLine + "\n" + style.Render("  no matches")
		}
		return inputLine
	}

	start := m.searchOff
	end := start + dlgDropdownN
	if end > len(m.searchMatches) {
		end = len(m.searchMatches)
	}

	selBg := lipgloss.NewStyle().Background(Accent).Foreground(TextColor)
	norm := lipgloss.NewStyle().Foreground(Muted)

	var dd []string
	for i := start; i < end; i++ {
		sm := m.searchMatches[i]
		entry := fmt.Sprintf("  %d. %s", sm.Line+1, sm.Text)
		if i == m.searchIdx {
			entry = selBg.Width(w).Render(entry)
		} else {
			entry = norm.Render(entry)
		}
		dd = append(dd, entry)
	}
	return inputLine + "\n" + strings.Join(dd, "\n")
}

func (m *DetailModel) renderBody(w, maxLines int) string {
	if m.Content == "" {
		return lipgloss.NewStyle().Foreground(Muted).Render("(empty)")
	}
	lines := strings.Split(m.Content, "\n")

	// Clamp scroll.
	m.maxScroll = len(lines) - maxLines
	if m.maxScroll < 0 {
		m.maxScroll = 0
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
	if m.scroll > m.maxScroll {
		m.scroll = m.maxScroll
	}

	end := m.scroll + maxLines
	if end > len(lines) {
		end = len(lines)
	}

	// Copy mode selection range — character-precise visual columns.
		const selBg = "\x1b[48;5;24m" // dark blue, matching outer CopyMode
		var sx, sy, ex, ey int
		if m.copyActive {
			sx, sy, ex, ey = m.copyAnchorX, m.copyAnchorY, m.copyFocusX, m.copyFocusY
			if sy > ey || (sy == ey && sx > ex) {
				sx, sy, ex, ey = ex, ey, sx, sy
			}
		}

		var visible []string
		for i := m.scroll; i < end; i++ {
			line := lines[i]
			if lipgloss.Width(line) > w {
				line = ansi.Truncate(line, w-3, "...")
			}
			// Search match highlight.
			if m.searchMode && m.searchQ != "" && m.searchIdx >= 0 &&
				m.searchIdx < len(m.searchMatches) &&
				m.searchMatches[m.searchIdx].Line == i {
				line = highlightMatch(line, m.searchQ)
			}
			// Copy selection highlight — character-precise via injectBgRange.
			if m.copyActive {
				switch {
				case i < sy || i > ey:
					// no highlight
				case sy == ey && i == sy:
					line = injectBgRange(line, selBg, sx, ex)
				case i == sy:
					line = injectBgRange(line, selBg, sx, -1)
				case i == ey:
					line = injectBgRange(line, selBg, 0, ex)
				default:
					line = injectBgRange(line, selBg, 0, -1)
				}
			}
			visible = append(visible, line)
	}

	return lipgloss.NewStyle().Foreground(TextColor).Width(w).
		Render(strings.Join(visible, "\n"))
}

func (m *DetailModel) renderHints(w int) string {
	if m.searchMode {
		return lipgloss.NewStyle().Foreground(Muted).Width(w).Render(
			"  Ctrl+P/N match · Enter jump · Esc cancel · q close",
		)
	}
	return lipgloss.NewStyle().Foreground(Muted).Width(w).Render(
		"  ↑↓/jk scroll · PgUp/PgDn · / search · Esc/q close",
	)
}

// searchContent runs fuzzy search against content lines.
func searchContent(content, query string) []searchMatch {
	if query == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	lowerQ := strings.ToLower(query)
	var matches []searchMatch
	for i, line := range lines {
		lowerL := strings.ToLower(line)
		if strings.Contains(lowerL, lowerQ) || fuzzyMatch(lowerL, lowerQ) {
			matches = append(matches, searchMatch{Line: i, Text: truncateForDisplay(line, 60)})
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Line < matches[j].Line
	})
	if len(matches) > 20 {
		matches = matches[:20]
	}
	return matches
}
