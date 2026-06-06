package message

// renderSystem renders a system message.
func renderSystem(m *ChatMessage) string {
	return SystemStyle.Render("· " + m.Content)
}
