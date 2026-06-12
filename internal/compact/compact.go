package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/raydraw/ergate/internal/llm"
)

const (
	// DefaultThreshold is the fallback compaction threshold (in estimated tokens).
	// When the model's ContextWindow is known, use ContextWindow * 0.8 instead.
	DefaultThreshold = 30_000
	// KeepRecent is the number of recent tool result messages to preserve.
	KeepRecent = 3
	// SnipKeepRecent is the number of recent thinking blocks to preserve.
	SnipKeepRecent = 2
	// MaxConsecutiveFailures is the circuit breaker limit for AutoCompact.
	MaxConsecutiveFailures = 3
)

// EstimateTokens uses a heuristic of ~4 chars per token.
func EstimateTokens(messages []llm.Message) int {
	raw, _ := json.Marshal(messages)
	return len(raw) / 4
}

// ShouldCompact returns true when estimated tokens exceed the given threshold.
// Pass 0 to use DefaultThreshold.
func ShouldCompact(messages []llm.Message, threshold int) bool {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return EstimateTokens(messages) > threshold
}

// SnipCompact clears old thinking/reasoning content from assistant messages.
// This is the lightest compaction layer — zero API calls, minimal side effects.
// Keeps the most recent SnipKeepRecent thinking blocks intact.
func SnipCompact(msgs []llm.Message) ([]llm.Message, int) {
	var thinkingIdx []int
	for i, m := range msgs {
		if m.Role == "assistant" {
			for _, b := range m.Content {
				if b.Type == "thinking" && len(b.Thinking) > 200 {
					thinkingIdx = append(thinkingIdx, i)
					break
				}
			}
		}
	}

	if len(thinkingIdx) <= SnipKeepRecent {
		return msgs, 0
	}

	tokensSaved := 0
	for _, idx := range thinkingIdx[:len(thinkingIdx)-SnipKeepRecent] {
		for j := range msgs[idx].Content {
			if msgs[idx].Content[j].Type == "thinking" && len(msgs[idx].Content[j].Thinking) > 200 {
				tokensSaved += len(msgs[idx].Content[j].Thinking) / 4
				msgs[idx].Content[j].Thinking = "[thinking cleared]"
			}
		}
	}
	return msgs, tokensSaved
}

// MicroCompact replaces old tool result content with "[cleared]" to save tokens.
// Keeps the most recent N tool result messages intact. Modifies the slice in place.
func MicroCompact(msgs []llm.Message) []llm.Message {
	var toolIdx []int
	for i, m := range msgs {
		if hasToolResult(m) {
			toolIdx = append(toolIdx, i)
		}
	}

	if len(toolIdx) <= KeepRecent {
		return msgs
	}

	for _, idx := range toolIdx[:len(toolIdx)-KeepRecent] {
		for j := range msgs[idx].Content {
			if msgs[idx].Content[j].Type == "tool_result" && len(msgs[idx].Content[j].Content) > 100 {
				msgs[idx].Content[j].Content = json.RawMessage(`"[cleared]"`)
			}
		}
	}
	return msgs
}

// toolCallIDs returns the set of tool_call IDs in a message slice.
func toolCallIDs(msgs []llm.Message) map[string]bool {
	ids := make(map[string]bool)
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "tool_use" && b.ID != "" {
				ids[b.ID] = true
			}
		}
	}
	return ids
}

func hasToolResult(m llm.Message) bool {
	for _, b := range m.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// AutoCompact sends the conversation to the LLM for summarization.
// Returns compressed messages that completely replace the originals.
// Prefer FoldCompact for new code — it preserves recent tail messages for
// better context continuity and prefix-cache baseline.
func AutoCompact(ctx context.Context, client llm.LLMClient, messages []llm.Message, model string) ([]llm.Message, error) {
	return foldCompact(ctx, client, messages, model, 0)
}

// FoldCompact sends the conversation prefix (all except the last keepTail
// messages) to the LLM for summarization, then appends the tail unchanged.
// The tail messages retain their original byte representation, which preserves
// context quality and provides a richer prefix-cache baseline for later turns.
//
// If keepTail is 0 or >= len(messages), it degrades to AutoCompact behavior.
func FoldCompact(ctx context.Context, client llm.LLMClient, messages []llm.Message, model string, keepTail int) ([]llm.Message, error) {
	if keepTail <= 0 || keepTail >= len(messages) {
		return foldCompact(ctx, client, messages, model, 0)
	}
	return foldCompact(ctx, client, messages, model, keepTail)
}

// DefaultKeepTail is the default number of messages to preserve at the end of
// a FoldCompact operation. The actual split point is aligned backward to a
// user-message boundary to avoid breaking tool_call→tool_result pairs.
const DefaultKeepTail = 3

// foldCompact is the shared implementation.
func foldCompact(ctx context.Context, client llm.LLMClient, messages []llm.Message, model string, keepTail int) ([]llm.Message, error) {
	var head, tail []llm.Message
	if keepTail > 0 && keepTail < len(messages) {
		split := len(messages) - keepTail
		if split < 1 {
			split = 1
		}
		// Resolve orphan tool results: if the tail contains a tool_result
		// whose tool_call is in the head, move split backward to include
		// that assistant message, keeping the pair together.
		head = messages[:split]
		tail = messages[split:]
		for {
			orphan := false
			headIDs := toolCallIDs(head)
			for _, m := range tail {
				for _, b := range m.Content {
					if b.Type == "tool_result" && headIDs[b.ToolUseID] {
						// Find the assistant message in head that owns this tool_call
						// and move split to include it in tail.
						for i := split - 1; i >= 0; i-- {
							for _, bb := range messages[i].Content {
								if bb.Type == "tool_use" && bb.ID == b.ToolUseID {
									split = i
									orphan = true
									break
								}
							}
							if orphan {
								break
							}
						}
					}
				}
			}
			if !orphan || split <= 1 {
				break
			}
			head = messages[:split]
			tail = messages[split:]
		}
	} else {
		head = messages
	}

	raw, _ := json.Marshal(head)
	convText := string(raw)
	if len(convText) > 80_000 {
		convText = convText[:80_000]
	}

	summarizePrompt := fmt.Sprintf(
		"Summarize this conversation for continuity. Be concise. Include:\n"+
			"1) What was accomplished\n2) Current state\n3) Key decisions\n\n%s",
		convText,
	)

	req := &llm.ChatRequest{
		Model:     model,
		System:    "You are a conversation summarizer. Be extremely concise.",
		Messages:  []llm.Message{llm.NewUserMessage(summarizePrompt)},
		MaxTokens: 2000,
	}

	resp, err := client.Chat(ctx, req)
	if err != nil {
		return messages, fmt.Errorf("compact summarize: %w", err)
	}

	var summary strings.Builder
	for _, msg := range resp.Messages {
		for _, block := range msg.Content {
			if block.Type == "text" {
				summary.WriteString(block.Text)
			}
		}
	}

	compacted := []llm.Message{
		llm.NewCompactBoundary("auto", EstimateTokens(head), summary.String()),
		llm.NewUserMessage("[Conversation compressed]\n\n" + summary.String()),
		{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Understood. Continuing with summary context."}}},
	}
	compacted = append(compacted, tail...)
	return compacted, nil
}

// PruneCompact archives large tool results to disk and replaces them with
// short pointers. It keeps the most recent N tool results intact
// (same KeepRecent as MicroCompact). Returns modified slice and bytes freed.
func PruneCompact(msgs []llm.Message, thresholdBytes int) ([]llm.Message, int) {
	if thresholdBytes <= 0 {
		return msgs, 0
	}

	var toolIdx []int
	for i, m := range msgs {
		if hasToolResult(m) {
			toolIdx = append(toolIdx, i)
		}
	}
	if len(toolIdx) <= KeepRecent {
		return msgs, 0
	}

	saved := 0
	for _, idx := range toolIdx[:len(toolIdx)-KeepRecent] {
		for j := range msgs[idx].Content {
			if msgs[idx].Content[j].Type != "tool_result" {
				continue
			}
			content := decodeRawMessage(msgs[idx].Content[j].Content)
			if len(content) <= thresholdBytes {
				continue
			}

			// Archive to disk.
			resultDir := filepath.Join(".ergate", "tool-results")
			os.MkdirAll(resultDir, 0o700)
			fname := filepath.Join(resultDir, fmt.Sprintf("prune_%d.txt", time.Now().UnixNano()))
			if err := os.WriteFile(fname, []byte(content), 0o644); err == nil {
				pointer := fmt.Sprintf("[pruned: %d bytes saved to %s. Use Read with file_path=%q to retrieve the full result.]",
					len(content), fname, fname)
				saved += len(content) - len(pointer)
				msgs[idx].Content[j].Content = mustMarshalString(pointer)
			}
		}
	}
	return msgs, saved
}

func decodeRawMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func mustMarshalString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}

// CompactToolSchema returns the JSON schema for the compact tool.
func CompactToolSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"focus": {"type": "string", "description": "What to preserve during compaction"}
		}
	}`)
}
