package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Base colors
	Accent    = lipgloss.Color("#7C3AED") // Purple
	Subtle    = lipgloss.Color("#6B7280")
	Success   = lipgloss.Color("#10B981")
	Error     = lipgloss.Color("#EF4444")
	Warning   = lipgloss.Color("#F59E0B")
	Info      = lipgloss.Color("#3B82F6")
	Muted     = lipgloss.Color("#9CA3AF")
	BgDark    = lipgloss.Color("#111827")
	BgPanel   = lipgloss.Color("#1F2937")
	BorderDim = lipgloss.Color("#374151")
	TextColor = lipgloss.Color("#F9FAFB")

	// Style definitions
	AppStyle = lipgloss.NewStyle().
			Padding(0)

	HeaderStyle = lipgloss.NewStyle().
			Background(BgDark).
			Foreground(Accent).
			Bold(true).
			Padding(0, 1)

	ChatAreaStyle = lipgloss.NewStyle().
			Padding(0, 1)

	InputAreaStyle = lipgloss.NewStyle().
			Background(BgPanel).
			Padding(0, 1)

	UserMsgStyle = lipgloss.NewStyle().
			Foreground(Info).
			Bold(true)

	AssistantTextStyle = lipgloss.NewStyle().
				Foreground(TextColor)

	AssistantToolStyle = lipgloss.NewStyle().
				Foreground(Accent).
				Italic(true)

	ToolResultStyle = lipgloss.NewStyle().
			Foreground(Muted).
			PaddingLeft(2)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(Error).
			Bold(true)

	SpinnerStyle = lipgloss.NewStyle().
			Foreground(Warning).
			Italic(true)

	CostStyle = lipgloss.NewStyle().
			Foreground(Muted)

	HelpStyle = lipgloss.NewStyle().
			Foreground(Subtle)

	StatusBarStyle = lipgloss.NewStyle().
			Background(BgDark).
			Foreground(Muted).
			Padding(0, 1)

	PermissionDialogStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(Warning).
				Padding(1, 2).
				Background(BgPanel)

	ThinkingStyle = lipgloss.NewStyle().
			Foreground(Muted).
			Italic(true)

	// ChatDimStyle dims background chat content behind modal overlays.
	ChatDimStyle = lipgloss.NewStyle().
			Foreground(Subtle)


	// AssistantBorderStyle is the left border for assistant message blocks.
	AssistantBorderStyle = lipgloss.NewStyle().
				Foreground(Accent).
				Bold(true)
)

// RenderUserPrompt renders a user prompt marker.
func RenderUserPrompt() string {
	return UserMsgStyle.Render("▸ ")
}

// RenderToolUse renders a tool use indicator.
func RenderToolUse(name, input string) string {
	s := AssistantToolStyle.Render("⚙ " + name)
	if input != "" && len(input) < 80 {
		s += " " + MutedStyle(input)
	}
	return s
}

func MutedStyle(s string) string {
	return lipgloss.NewStyle().Foreground(Muted).Render(s)
}

// ThemeName identifies the active theme.
type ThemeName string

const (
	ThemeDark  ThemeName = "dark"
	ThemeLight ThemeName = "light"
)

// Theme holds the complete set of styles for a theme.
type Theme struct {
	Name         ThemeName
	Bg           lipgloss.Color
	BgPanel      lipgloss.Color
	Text         lipgloss.Color
	TextMuted    lipgloss.Color
	Accent       lipgloss.Color
	Border       lipgloss.Color
	InputBg      lipgloss.Color
	StatusBg     lipgloss.Color
}

var DarkTheme = Theme{
	Name: ThemeDark,
	Bg: lipgloss.Color("#111827"), BgPanel: lipgloss.Color("#1F2937"),
	Text: lipgloss.Color("#F9FAFB"), TextMuted: lipgloss.Color("#9CA3AF"),
	Accent: lipgloss.Color("#7C3AED"), Border: lipgloss.Color("#374151"),
	InputBg: lipgloss.Color("#1F2937"), StatusBg: lipgloss.Color("#111827"),
}

var LightTheme = Theme{
	Name: ThemeLight,
	Bg: lipgloss.Color("#FFFFFF"), BgPanel: lipgloss.Color("#F3F4F6"),
	Text: lipgloss.Color("#111827"), TextMuted: lipgloss.Color("#6B7280"),
	Accent: lipgloss.Color("#7C3AED"), Border: lipgloss.Color("#D1D5DB"),
	InputBg: lipgloss.Color("#F9FAFB"), StatusBg: lipgloss.Color("#F3F4F6"),
}

// CurrentTheme is the active theme (defaults to dark).
var CurrentTheme = DarkTheme

// SetTheme switches the active theme.
func SetTheme(name ThemeName) {
	switch name {
	case ThemeLight:
		CurrentTheme = LightTheme
	default:
		CurrentTheme = DarkTheme
	}
}

// DiffAddedStyle highlights added lines.
var DiffAddedStyle = lipgloss.NewStyle().Foreground(Success)

// DiffRemovedStyle highlights removed lines.
var DiffRemovedStyle = lipgloss.NewStyle().Foreground(Error)

// DiffHunkStyle highlights diff headers.
var DiffHunkStyle = lipgloss.NewStyle().Foreground(Info).Bold(true)

// RenderDiffLine renders a diff line prefix.
func RenderDiffLine(prefix rune, line string) string {
	switch prefix {
	case '+':
		return DiffAddedStyle.Render("+ " + line)
	case '-':
		return DiffRemovedStyle.Render("- " + line)
	case '@':
		return DiffHunkStyle.Render(line)
	default:
		return MutedStyle(line)
	}
}
