package message

// StreamBuffer coalesces streaming deltas into coherent messages.
// It buffers text until a role boundary or flush trigger.
type StreamBuffer struct {
	text string
	role string
	dirty bool
}

// NewStreamBuffer creates a new StreamBuffer.
func NewStreamBuffer() *StreamBuffer {
	return &StreamBuffer{}
}

// Append adds text for the given role. If the role differs from the current buffer,
// Flush is called to finalize the previous role's message.
func (s *StreamBuffer) Append(text, role string) {
	if s.dirty && s.role != role {
		s.text = text
		s.role = role
		s.dirty = true
		return
	}
	s.text += text
	s.role = role
	s.dirty = true
}

// Flush returns the buffered text and role, and resets the buffer.
// Returns ("", "") if nothing is buffered.
func (s *StreamBuffer) Flush() (text, role string) {
	if !s.dirty {
		return "", ""
	}
	text = s.text
	role = s.role
	s.text = ""
	s.dirty = false
	return text, role
}

// IsDirty returns true if there is buffered text.
func (s *StreamBuffer) IsDirty() bool { return s.dirty }

// Len returns the length of buffered text.
func (s *StreamBuffer) Len() int { return len(s.text) }

// Reset clears the buffer without flushing.
func (s *StreamBuffer) Reset() {
	s.text = ""
	s.dirty = false
}
