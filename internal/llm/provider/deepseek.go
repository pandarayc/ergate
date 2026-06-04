package provider

import (
	"context"
	"encoding/json"
	"io"

	"github.com/raydraw/ergate/internal/llm"
)

func init() {
	llm.Register(deepseekProvider{})
}

type deepseekProvider struct{}

func (deepseekProvider) Name() string           { return "deepseek" }
func (deepseekProvider) DefaultBaseURL() string { return "https://api.deepseek.com" }
func (deepseekProvider) NewClient(apiKey, baseURL string) llm.LLMClient {
	return NewOpenAIClientWithAdapter(apiKey, baseURL, DeepSeekAdapter{})
}

// DeepSeekAdapter extends the OpenAI protocol with DeepSeek-specific features:
//   - reasoning_content in delta chunks (R1 thinking)
//   - prompt_cache_hit_tokens / prompt_cache_miss_tokens in usage
//
// It embeds OpenAIAdapter and overrides only the methods that differ.
type DeepSeekAdapter struct {
	OpenAIAdapter
}

// BuildRequestBody builds an OpenAI-format request with reasoning_content support.
func (DeepSeekAdapter) BuildRequestBody(req *llm.ChatRequest) map[string]interface{} {
	return buildOpenAIRequestBody(req, requestOpts{withReasoning: true})
}

// Features reports DeepSeek capabilities on top of the OpenAI base.
func (DeepSeekAdapter) Features() llm.FeatureSet {
	return llm.FeatureSet{
		SupportsReasoning: true,
		SupportsVision:    true,
		StreamProtocol:    "openai-sse",
	}
}

// ParseStream implements OpenAI SSE streaming with DeepSeek extensions.
func (DeepSeekAdapter) ParseStream(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	parseOpenAISSEStream(ctx, body, events, deepseekChunkHandler)
}

// --- DeepSeek-specific chunk handler ---

func deepseekChunkHandler(data string, events chan<- llm.StreamEvent, toolCalls *map[int]*openaiToolCall) {
	var chunk deepseekStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}

	// Usage with cache metrics (last chunk when stream_options.include_usage=true).
	if chunk.Usage != nil {
		raw, _ := json.Marshal(map[string]interface{}{
			"delta": map[string]interface{}{"stop_reason": "stop"},
			"usage": map[string]interface{}{
				"input_tokens":              chunk.Usage.PromptTokens,
				"output_tokens":             chunk.Usage.CompletionTokens,
				"prompt_cache_hit_tokens":   chunk.Usage.PromptCacheHitTokens,
				"prompt_cache_miss_tokens":  chunk.Usage.PromptCacheMissTokens,
			},
		})
		events <- llm.StreamEvent{Type: llm.EventMessageDelta, Data: raw}
	}

	if len(chunk.Choices) == 0 {
		return
	}
	choice := chunk.Choices[0]

	if choice.Delta.Content != "" {
		raw, _ := json.Marshal(map[string]string{"text": choice.Delta.Content})
		events <- llm.StreamEvent{Type: llm.EventText, Data: raw}
	}

	// DeepSeek R1 reasoning content
	if choice.Delta.ReasoningContent != "" {
		raw, _ := json.Marshal(map[string]string{"thinking": choice.Delta.ReasoningContent})
		events <- llm.StreamEvent{Type: llm.EventThinking, Data: raw}
	}

	emitToolCallEvents(choice.Delta.ToolCalls, choice.FinishReason, events, toolCalls)
}

// --- DeepSeek-specific types (extend OpenAI base types) ---

type deepseekStreamChunk struct {
	Choices []deepseekStreamChoice `json:"choices"`
	Usage   *deepseekUsage         `json:"usage,omitempty"`
}

type deepseekStreamChoice struct {
	Index        int                 `json:"index"`
	Delta        deepseekStreamDelta `json:"delta"`
	FinishReason string              `json:"finish_reason"`
}

type deepseekStreamDelta struct {
	Role             string                 `json:"role,omitempty"`
	Content          string                 `json:"content,omitempty"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"` // R1 extension
	ToolCalls        []openaiStreamToolCall `json:"tool_calls,omitempty"`
}

type deepseekUsage struct {
	PromptTokens          int `json:"prompt_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`  // extension
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"` // extension
}

// Verify interface compliance at compile time.
var _ llm.ProviderAdapter = DeepSeekAdapter{}
