package llm

import (
	"context"
	"encoding/json"
	"time"
)

// LLMClient is the abstraction over LLM API providers.
type LLMClient interface {
	// Chat sends a non-streaming request and returns the full response.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream sends a streaming request and returns a channel of events.
	ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error)

	// Close releases underlying resources.
	Close() error

	// Adapter returns the provider adapter for message formatting and feature introspection.
	Adapter() ProviderAdapter
}

// ChatRequest is a provider-agnostic request shape.
type ChatRequest struct {
	Model          string       `json:"model"`
	System         string       `json:"system,omitempty"`
	Messages       []Message    `json:"messages"`
	Tools          []ToolConfig `json:"tools,omitempty"`
	MaxTokens      int          `json:"max_tokens"`
	Temperature    float64      `json:"temperature,omitempty"`
	ThinkingBudget int          `json:"-"` // Claude extended thinking token budget (0 = disabled)
}

// ChatResponse is the provider-agnostic response shape.
type ChatResponse struct {
	ID         string    `json:"id"`
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Usage      Usage     `json:"usage"`
	StopReason string    `json:"stop_reason"`
}

// Message represents a chat message with discriminated union support.
type Message struct {
	Type      MessageType   `json:"type"`
	UUID      string        `json:"uuid"`
	Timestamp time.Time     `json:"timestamp"`
	Role      string        `json:"role"`
	Content   []ContentBlock `json:"content"`

	// Assistant-specific
	MessageID  string `json:"message_id,omitempty"`
	Model      string `json:"model,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`

	// System-specific
	Subtype string `json:"subtype,omitempty"`
	Level   string `json:"level,omitempty"`

	// Flags
	IsMeta    bool `json:"is_meta,omitempty"`
	IsVirtual bool `json:"is_virtual,omitempty"`
}

// ContentBlock represents a single content block in a message.
type ContentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Cached     interface{}     `json:"cache_control,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Thinking   string          `json:"thinking,omitempty"`           // extended thinking (Claude)
	Reasoning  string          `json:"reasoning_content,omitempty"`  // reasoning chain (e.g. DeepSeek R1)
	Citations  interface{}     `json:"citations,omitempty"`           // references
}

// ToolConfig describes a tool to the LLM.
type ToolConfig struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// Usage tracks token consumption.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// Provider-specific cache metrics (e.g. prompt_cache_hit_tokens / prompt_cache_miss_tokens).
	CacheHitTokens  int `json:"prompt_cache_hit_tokens,omitempty"`
	CacheMissTokens int `json:"prompt_cache_miss_tokens,omitempty"`
}

// StreamEvent is emitted during streaming.
type StreamEvent struct {
	Type  StreamEventType
	Data  json.RawMessage
	Error error
}

// StreamEventType indicates the kind of stream event.
type StreamEventType string

const (
	EventText           StreamEventType = "text"
	EventThinking       StreamEventType = "thinking"
	EventToolUseStart   StreamEventType = "tool_use_start"
	EventToolUseEnd     StreamEventType = "tool_use_end"
	EventError          StreamEventType = "error"
	EventDone           StreamEventType = "done"
	EventMessageStart   StreamEventType = "message_start"
	EventMessageDelta   StreamEventType = "message_delta"
)

// ToolUseBlock represents a tool call extracted from an assistant response.
type ToolUseBlock struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Streaming bool            `json:"-"` // true if being built across stream events
}

// ToolResultBlock represents a tool result.
type ToolResultBlock struct {
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Provider describes an LLM API provider and its client factory.
// Implementations register themselves via Register() in init().
type Provider interface {
	Name() string
	DefaultBaseURL() string
	NewClient(apiKey, baseURL string) LLMClient
}

var registry = make(map[string]Provider)

// Register registers a provider. Call from init().
func Register(p Provider) {
	registry[p.Name()] = p
}

// IsRegistered returns true if the provider name is available.
func IsRegistered(name string) bool {
	_, ok := registry[name]
	return ok
}

// RegisteredProviders returns the names of all registered providers.
func RegisteredProviders() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// NewLLMClient creates the appropriate client for the given provider name.
func NewLLMClient(providerName, apiKey, baseURL string) (LLMClient, error) {
	p, ok := registry[providerName]
	if !ok {
		return nil, &ProviderError{Provider: providerName}
	}
	if baseURL == "" {
		baseURL = p.DefaultBaseURL()
	}
	return p.NewClient(apiKey, baseURL), nil
}

// ProviderError represents an unsupported provider.
type ProviderError struct {
	Provider string
}

func (e *ProviderError) Error() string {
	if e.Provider == "" {
		return "unsupported LLM provider"
	}
	return "unsupported LLM provider: " + e.Provider
}
