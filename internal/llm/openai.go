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
)

func init() {
	Register(openaiProvider{})
	Register(deepseekProvider{})
}

type openaiProvider struct{}

func (openaiProvider) Name() string               { return "openai" }
func (openaiProvider) DefaultBaseURL() string      { return "https://api.openai.com/v1" }
func (openaiProvider) NewClient(apiKey, baseURL string) LLMClient {
	return NewOpenAIClient(apiKey, baseURL)
}

type deepseekProvider struct{}

func (deepseekProvider) Name() string               { return "deepseek" }
func (deepseekProvider) DefaultBaseURL() string      { return "https://api.deepseek.com/v1" }
func (deepseekProvider) NewClient(apiKey, baseURL string) LLMClient {
	return NewOpenAIClient(apiKey, baseURL)
}

// OpenAIClient implements LLMClient for OpenAI-compatible Chat Completions API.
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIClient creates a new OpenAI-compatible API client.
func NewOpenAIClient(apiKey, baseURL string) *OpenAIClient {
	return &OpenAIClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 0, // streaming requests can last minutes
		},
	}
}

// Chat sends a non-streaming request.
func (c *OpenAIClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	reqBody := c.buildRequest(req, false)

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
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
		return nil, c.parseError(resp, body)
	}

	var result openaiChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return c.toChatResponse(&result), nil
}

// ChatStream sends a streaming request.
func (c *OpenAIClient) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	reqBody := c.buildRequest(req, true)

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(body))
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
		return nil, c.parseError(resp, body)
	}

	events := make(chan StreamEvent, 64)
	go c.readSSEStream(ctx, resp.Body, events)
	return events, nil
}

func (c *OpenAIClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// Adapter returns nil — OpenAI adapter is not yet extracted.
func (c *OpenAIClient) Adapter() ProviderAdapter { return nil }

func (c *OpenAIClient) buildRequest(req *ChatRequest, stream bool) map[string]interface{} {
	apiReq := map[string]interface{}{
		"model":       req.Model,
		"max_tokens":  req.MaxTokens,
		"stream":      stream,
		"temperature": req.Temperature,
	}

	// Messages
	messages := make([]map[string]interface{}, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, msg := range req.Messages {
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": c.convertContent(msg.Content),
		}
		if msg.Role == "assistant" {
			// For assistant messages with tool calls, include tool_calls
			var toolCalls []map[string]interface{}
			for _, block := range msg.Content {
				if block.Type == "tool_use" {
					tc := map[string]interface{}{
						"id":   block.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      block.Name,
							"arguments": string(block.Input),
						},
					}
					toolCalls = append(toolCalls, tc)
				}
			}
			if len(toolCalls) > 0 {
				m["tool_calls"] = toolCalls
			}
		}
		messages = append(messages, m)
	}
	apiReq["messages"] = messages

	// Tools → OpenAI format
	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(req.Tools))
		for _, t := range req.Tools {
			tool := map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  json.RawMessage(t.InputSchema),
				},
			}
			tools = append(tools, tool)
		}
		apiReq["tools"] = tools
	}

	return apiReq
}

func (c *OpenAIClient) convertContent(blocks []ContentBlock) interface{} {
	// If it's a single text block, return as string
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}

	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, map[string]interface{}{"type": "text", "text": b.Text})
		case "tool_result":
			result = append(result, map[string]interface{}{
				"tool_call_id": b.ToolUseID,
				"role":         "tool",
				"content":      string(b.Content),
			})
		case "image":
			result = append(result, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]interface{}{
					"url": b.Text, // assume data URL format
				},
			})
		}
	}
	return result
}

func (c *OpenAIClient) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
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
			continue // skip malformed chunks
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		// Text content
		if choice.Delta.Content != "" {
			raw, _ := json.Marshal(map[string]string{"text": choice.Delta.Content})
			events <- StreamEvent{Type: EventText, Data: raw}
		}

		// Reasoning content (DeepSeek R1)
		if choice.Delta.ReasoningContent != "" {
			raw, _ := json.Marshal(map[string]string{"thinking": choice.Delta.ReasoningContent})
			events <- StreamEvent{Type: EventThinking, Data: raw}
		}

		// Tool calls (streaming)
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

		// Finish reason indicates tool call is complete
		if choice.FinishReason == "tool_calls" && toolCalls != nil {
			for i, tc := range toolCalls {
				raw, _ := json.Marshal(map[string]interface{}{
					"id":   tc.ID,
					"name": tc.Name,
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

type openaiChatResponse struct {
	ID      string           `json:"id"`
	Model   string           `json:"model"`
	Choices []openaiChoice   `json:"choices"`
	Usage   openaiUsage      `json:"usage"`
}

type openaiChoice struct {
	Index        int              `json:"index"`
	Message      openaiMessage    `json:"message"`
	FinishReason string           `json:"finish_reason"`
}

type openaiMessage struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	ToolCalls []openaiToolCall  `json:"tool_calls,omitempty"`
}

type openaiToolCall struct {
	ID        string                `json:"id"`
	Type      string                `json:"type"`
	Index     int                   `json:"index"`
	Name      string                `json:"-"`
	Arguments string                `json:"-"`
	Function  openaiToolCallFunction `json:"function"`
}

type openaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiStreamChunk struct {
	Choices []openaiStreamChoice `json:"choices"`
}

type openaiStreamChoice struct {
	Index        int                 `json:"index"`
	Delta        openaiStreamDelta   `json:"delta"`
	FinishReason string              `json:"finish_reason"`
}

type openaiStreamDelta struct {
	Role             string                  `json:"role,omitempty"`
	Content          string                  `json:"content,omitempty"`
	ReasoningContent string                  `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiStreamToolCall  `json:"tool_calls,omitempty"`
}

type openaiStreamToolCall struct {
	Index    int                              `json:"index"`
	ID       string                           `json:"id,omitempty"`
	Type     string                           `json:"type,omitempty"`
	Function openaiStreamToolCallFunction     `json:"function"`
}

type openaiStreamToolCallFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func (c *OpenAIClient) toChatResponse(resp *openaiChatResponse) *ChatResponse {
	result := &ChatResponse{
		ID:     resp.ID,
		Model:  resp.Model,
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
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

func (c *OpenAIClient) parseError(resp *http.Response, reqBody []byte) error {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))

	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
		return &APIError{
			Type:    errResp.Error.Type,
			Message: errResp.Error.Message,
			Status:  resp.StatusCode,
		}
	}

	return &APIError{
		Type:    fmt.Sprintf("http_%d", resp.StatusCode),
		Message: fmt.Sprintf("HTTP %d: %s (req: %s)", resp.StatusCode, string(respBody), truncateReq(reqBody)),
		Status:  resp.StatusCode,
	}
}

func truncateReq(b []byte) string {
	if len(b) <= 200 {
		return string(b)
	}
	return string(b[:200]) + "..."
}

func mustMarshal(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}
