package message

// renderUser renders a user message.
func renderUser(m *ChatMessage) string {
	return UserStyle.Render("▸ ") + AssistantTextStyle.Render(m.Content)
}
