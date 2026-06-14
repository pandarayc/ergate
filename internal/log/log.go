// Package log provides an append-only message log with type-system enforcement.
// Direct mutation of message history is blocked — only Append and Fold are allowed.
// This guarantees byte-level stability of the message prefix for provider prefix caches.
package log

import (
	"context"
	"sync"

	"github.com/raydraw/ergate/internal/compact"
	"github.com/raydraw/ergate/internal/llm"
)

// Log is an append-only conversation history. The underlying message slice is
// unexported; all writes go through Append (normal turns) or Fold (compaction).
// This prevents accidental message mutation that would invalidate prefix caches.
type Log struct {
	mu      sync.Mutex
	entries []llm.Message
}

// New creates an empty Log.
func New() *Log {
	return &Log{entries: make([]llm.Message, 0)}
}

// Append adds a message to the end of the log. This is the only way to add new
// messages during normal turn processing — no other code path can mutate entries.
func (l *Log) Append(msg llm.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, msg)
}

// Messages returns a copy of the current message history. The copy is safe to
// pass to the LLM API — mutations to the returned slice do not affect the log.
func (l *Log) Messages() []llm.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]llm.Message, len(l.entries))
	copy(result, l.entries)
	return result
}

// Len returns the number of messages in the log.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// Fold triggers LLM summarization of old messages while preserving recent ones.
// It delegates to compact.FoldCompact and replaces the internal message list
// atomically. This is the ONLY allowed mutation of existing messages — and it's
// a deliberate, controlled operation that resets the prefix-cache baseline.
func (l *Log) Fold(ctx context.Context, client llm.LLMClient, model string, keepTail int) error {
	l.mu.Lock()
	msgs := make([]llm.Message, len(l.entries))
	copy(msgs, l.entries)
	l.mu.Unlock()

	compacted, err := compact.FoldCompact(ctx, client, msgs, model, keepTail)
	if err != nil {
		return err
	}

	l.mu.Lock()
	l.entries = compacted
	l.mu.Unlock()
	return nil
}

// Import replaces the entire log with a previously exported message list.
// Used for session resumption.
func (l *Log) Import(msgs []llm.Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make([]llm.Message, len(msgs))
	copy(l.entries, msgs)
}

// Export returns a deep copy of the current message list for serialization.
func (l *Log) Export() []llm.Message {
	return l.Messages()
}

// Clear empties the log, resetting conversation state.
func (l *Log) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = make([]llm.Message, 0)
}
