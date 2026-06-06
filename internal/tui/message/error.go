package message

// renderError renders an error message.
func renderError(m *ChatMessage) string {
	return ErrorStyle.Render("✖ " + m.Content)
}
