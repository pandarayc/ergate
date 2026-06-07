package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

func hasToolResult(m llm.Message) bool {
	for _, b := range m.Content {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// AutoCompact sends the conversation to the LLM for summarization.
// Returns compressed messages that should replace the originals.
func AutoCompact(ctx context.Context, client llm.LLMClient, messages []llm.Message, model string) ([]llm.Message, error) {
	raw, _ := json.Marshal(messages)
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

	return []llm.Message{
		llm.NewCompactBoundary("auto", EstimateTokens(messages), summary.String()),
		llm.NewUserMessage("[Conversation compressed]\n\n" + summary.String()),
		{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Understood. Continuing with summary context."}}},
	}, nil
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
