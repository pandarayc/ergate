package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func init() {
	Register(openaiProvider{})
}

type openaiProvider struct{}

func (openaiProvider) Name() string           { return "openai" }
func (openaiProvider) DefaultBaseURL() string { return "https://api.openai.com/v1" }
func (openaiProvider) NewClient(apiKey, baseURL string) LLMClient {
	return NewOpenAIClient(apiKey, baseURL)
}

// OpenAIClient implements LLMClient for OpenAI-compatible Chat Completions API.
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	adapter    ProviderAdapter
}

// NewOpenAIClient creates a new OpenAI-compatible API client.
func NewOpenAIClient(apiKey, baseURL string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		adapter: OpenAIAdapter{},
	}
}

// Adapter returns the provider adapter for feature introspection.
func (c *OpenAIClient) Adapter() ProviderAdapter { return c.adapter }

// Chat sends a non-streaming request.
func (c *OpenAIClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	reqBody := c.adapter.BuildRequestBody(req)
	reqBody["stream"] = false

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+c.adapter.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return nil, c.adapter.ParseErrorResponse(resp.StatusCode, respBody, body)
	}

	var result openaiChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return c.toChatResponse(&result), nil
}

// ChatStream sends a streaming request.
func (c *OpenAIClient) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	reqBody := c.adapter.BuildRequestBody(req)
	reqBody["stream"] = true
	reqBody["stream_options"] = map[string]interface{}{"include_usage": true}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+c.adapter.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return nil, c.adapter.ParseErrorResponse(resp.StatusCode, respBody, body)
	}

	events := make(chan StreamEvent, 64)
	go c.readSSEStream(ctx, resp.Body, events)
	return events, nil
}

func (c *OpenAIClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func (c *OpenAIClient) setHeaders(req *http.Request) {
	for k, v := range c.adapter.Headers(c.apiKey) {
		req.Header.Set(k, v)
	}
}

// readSSEStream reads OpenAI SSE stream format (data: lines with [DONE] terminator).
func (c *OpenAIClient) readSSEStream(ctx context.Context, body io.ReadCloser, events chan<- StreamEvent) {
	defer close(events)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var toolCalls map[int]*openaiToolCall // index -> tool call being built

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: EventError, Error: ctx.Err()}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())

		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			events <- StreamEvent{Type: EventDone, Data: json.RawMessage(`{"stop_reason": "stop"}`)}
			return
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Emit usage if present (last chunk when stream_options.include_usage=true).
		if chunk.Usage != nil {
			usageData := map[string]interface{}{
				"delta": map[string]interface{}{
					"stop_reason": "stop",
				},
				"usage": map[string]interface{}{
					"input_tokens":              chunk.Usage.PromptTokens,
					"output_tokens":             chunk.Usage.CompletionTokens,
					"prompt_cache_hit_tokens":   chunk.Usage.PromptCacheHitTokens,
					"prompt_cache_miss_tokens":  chunk.Usage.PromptCacheMissTokens,
				},
			}
			raw, _ := json.Marshal(usageData)
			events <- StreamEvent{Type: EventMessageDelta, Data: raw}
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.Content != "" {
			raw, _ := json.Marshal(map[string]string{"text": choice.Delta.Content})
			events <- StreamEvent{Type: EventText, Data: raw}
		}

		// Reasoning content (DeepSeek R1)
		if choice.Delta.ReasoningContent != "" {
			raw, _ := json.Marshal(map[string]string{"thinking": choice.Delta.ReasoningContent})
			events <- StreamEvent{Type: EventThinking, Data: raw}
		}

		if len(choice.Delta.ToolCalls) > 0 {
			if toolCalls == nil {
				toolCalls = make(map[int]*openaiToolCall)
			}
			for _, tc := range choice.Delta.ToolCalls {
				if _, exists := toolCalls[tc.Index]; !exists {
					toolCalls[tc.Index] = &openaiToolCall{ID: tc.ID}
					events <- StreamEvent{Type: EventToolUseStart, Data: mustMarshal(map[string]interface{}{
						"id":    tc.ID,
						"name":  tc.Function.Name,
						"index": tc.Index,
					})}
				}

				existing := toolCalls[tc.Index]
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Name = tc.Function.Name
				}
				existing.Arguments += tc.Function.Arguments
			}
		}

		if choice.FinishReason == "tool_calls" && toolCalls != nil {
			for i, tc := range toolCalls {
				raw, _ := json.Marshal(map[string]interface{}{
					"id":    tc.ID,
					"name":  tc.Name,
					"input": json.RawMessage(tc.Arguments),
					"index": i,
				})
				events <- StreamEvent{Type: EventToolUseEnd, Data: raw}
			}
			toolCalls = nil
		}
	}

	if err := scanner.Err(); err != nil {
		events <- StreamEvent{Type: EventError, Error: fmt.Errorf("scan stream: %w", err)}
		return
	}

	events <- StreamEvent{Type: EventDone, Data: json.RawMessage(`{"stop_reason": "stop"}`)}
}

// --- OpenAI API types ---

type openaiChatResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   openaiUsage    `json:"usage"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Index     int                    `json:"index"`
	Name      string                 `json:"-"`
	Arguments string                 `json:"-"`
	Function  openaiToolCallFunction `json:"function"`
}

type openaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	PromptCacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int `json:"prompt_cache_miss_tokens"`
}

type openaiStreamChunk struct {
	Choices []openaiStreamChoice `json:"choices"`
	Usage   *openaiUsage         `json:"usage,omitempty"` // present in last chunk when stream_options.include_usage=true
}

type openaiStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openaiStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason"`
}

type openaiStreamDelta struct {
	Role             string                 `json:"role,omitempty"`
	Content          string                 `json:"content,omitempty"`
	ReasoningContent string                 `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiStreamToolCall `json:"tool_calls,omitempty"`
}

type openaiStreamToolCall struct {
	Index    int                          `json:"index"`
	ID       string                       `json:"id,omitempty"`
	Type     string                       `json:"type,omitempty"`
	Function openaiStreamToolCallFunction `json:"function"`
}

type openaiStreamToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func (c *OpenAIClient) toChatResponse(resp *openaiChatResponse) *ChatResponse {
	result := &ChatResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: Usage{
			InputTokens:     resp.Usage.PromptTokens,
			OutputTokens:    resp.Usage.CompletionTokens,
			CacheHitTokens:  resp.Usage.PromptCacheHitTokens,
			CacheMissTokens: resp.Usage.PromptCacheMissTokens,
		},
	}

	for _, choice := range resp.Choices {
		var blocks []ContentBlock

		if choice.Message.Content != "" {
			blocks = append(blocks, ContentBlock{Type: "text", Text: choice.Message.Content})
		}

		for _, tc := range choice.Message.ToolCalls {
			blocks = append(blocks, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}

		result.Messages = append(result.Messages, Message{Role: "assistant", Content: blocks})
		result.StopReason = choice.FinishReason
	}

	return result
}

func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}
