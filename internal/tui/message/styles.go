package message

import "github.com/charmbracelet/lipgloss"

// Colors match ergate's tui package color scheme.
var (
	Accent    = lipgloss.Color("#7C3AED")
	Info      = lipgloss.Color("#3B82F6")
	Muted     = lipgloss.Color("#9CA3AF")
	Error     = lipgloss.Color("#EF4444")
	Success   = lipgloss.Color("#10B981")
	TextColor = lipgloss.Color("#F9FAFB")
)

// Style definitions for each message role.
var (
	UserStyle = lipgloss.NewStyle().
			Foreground(Info).
			Bold(true)

	AssistantBorderStyle = lipgloss.NewStyle().
				Foreground(Accent).
				Bold(true)

	AssistantTextStyle = lipgloss.NewStyle().
				Foreground(TextColor)

	ToolStyle = lipgloss.NewStyle().
			Foreground(Accent).
			Italic(true)

	ToolResultStyle = lipgloss.NewStyle().
			Foreground(Muted)

	ThinkingStyle = lipgloss.NewStyle().
			Foreground(Muted)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(Error)

	SystemStyle = lipgloss.NewStyle().
			Foreground(Muted)

	// Fold styles.
	FoldStyle  = lipgloss.NewStyle().Foreground(Accent).Bold(true)
	DiffAdded  = lipgloss.NewStyle().Foreground(Success)
	DiffRemoved = lipgloss.NewStyle().Foreground(Error)
	DiffHunk   = lipgloss.NewStyle().Foreground(Info)
)
