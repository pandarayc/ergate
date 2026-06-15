package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/raydraw/ergate/internal/llm"
)

// OpenAIAdapter implements llm.ProviderAdapter for the OpenAI Chat Completions API.
// It handles the pure OpenAI protocol — no provider-specific extensions.
type OpenAIAdapter struct{}

// --- request options ---

// requestOpts controls optional behaviour in buildOpenAIRequestBody.
type requestOpts struct {
	withReasoning bool // inject reasoning_content into assistant messages
}

// buildOpenAIRequestBody is the shared request builder for OpenAI-protocol providers.
func buildOpenAIRequestBody(req *llm.ChatRequest, opts requestOpts) map[string]interface{} {
	apiReq := map[string]interface{}{
		"model":       req.Model,
		"max_tokens":  req.MaxTokens,
		"temperature": req.Temperature,
	}

	messages := make([]map[string]interface{}, 0, len(req.Messages)+1)
	if req.System != "" {
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, msg := range req.Messages {
		if msg.Role == "user" {
			var textBlocks []llm.ContentBlock
			for _, block := range msg.Content {
				if block.Type == "tool_result" {
					contentStr := decodeRawMessage(block.Content)
					messages = append(messages, map[string]interface{}{
						"role":         "tool",
						"tool_call_id": block.ToolUseID,
						"content":      contentStr,
					})
				} else {
					textBlocks = append(textBlocks, block)
				}
			}
			if len(textBlocks) > 0 {
				messages = append(messages, map[string]interface{}{
					"role":    msg.Role,
					"content": openaiConvertContent(textBlocks),
				})
			}
			continue
		}

		if msg.Role == "assistant" {
			m := map[string]interface{}{"role": "assistant"}
			var textBuilder strings.Builder
			var toolCalls []map[string]interface{}
			var reasoningContent string
			for _, block := range msg.Content {
				switch block.Type {
				case "text":
					textBuilder.WriteString(block.Text)
				case "tool_use":
					tc := map[string]interface{}{
						"id":   block.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      block.Name,
							"arguments": string(block.Input),
						},
					}
					toolCalls = append(toolCalls, tc)
				case "thinking":
					if opts.withReasoning && block.Thinking != "" {
						reasoningContent += block.Thinking
					}
				}
			}
			text := textBuilder.String()
			if text != "" {
				m["content"] = text
			} else {
				m["content"] = nil
			}
			if len(toolCalls) > 0 {
				m["tool_calls"] = toolCalls
			}
			if reasoningContent != "" {
				m["reasoning_content"] = reasoningContent
			}
			messages = append(messages, m)
			continue
		}

		messages = append(messages, map[string]interface{}{
			"role":    msg.Role,
			"content": openaiConvertContent(msg.Content),
		})
	}
	apiReq["messages"] = messages

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

func (OpenAIAdapter) BuildRequestBody(req *llm.ChatRequest) map[string]interface{} {
	return buildOpenAIRequestBody(req, requestOpts{})
}

func (OpenAIAdapter) ParseErrorResponse(statusCode int, respBody, reqBody []byte) *llm.APIError {
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
		return &llm.APIError{
			Type:    errResp.Error.Type,
			Message: errResp.Error.Message,
			Status:  statusCode,
		}
	}

	return &llm.APIError{
		Type:    fmt.Sprintf("http_%d", statusCode),
		Message: fmt.Sprintf("HTTP %d: %s (req: %s)", statusCode, string(respBody), truncateBytes(reqBody, 200)),
		Status:  statusCode,
	}
}

func (OpenAIAdapter) Headers(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Content-Type":  "application/json",
	}
}

func (OpenAIAdapter) Endpoint() string { return "/chat/completions" }

func (OpenAIAdapter) Features() llm.FeatureSet {
	return llm.FeatureSet{
		SupportsVision: true,
		StreamProtocol: "openai-sse",
	}
}

// ParseStream implements the pure OpenAI SSE streaming protocol.
func (OpenAIAdapter) ParseStream(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	parseOpenAISSEStream(ctx, body, events, openaiChunkHandler)
}

// --- SSE stream framework ---

// chunkHandler parses a single SSE data line and emits events.
// toolCalls is shared mutable state for accumulating streaming tool calls.
type chunkHandler func(data string, events chan<- llm.StreamEvent, toolCalls *map[int]*openaiToolCall)

// parseOpenAISSEStream is the shared SSE scanning loop for OpenAI-protocol providers.
// It handles the scanner lifecycle, line parsing, [DONE] detection, context cancellation,
// and final EventDone. Provider-specific delta interpretation is delegated to handler.
func parseOpenAISSEStream(
	ctx context.Context,
	body io.ReadCloser,
	events chan<- llm.StreamEvent,
	handler chunkHandler,
) {
	defer close(events)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var toolCalls map[int]*openaiToolCall

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- llm.StreamEvent{Type: llm.EventError, Error: ctx.Err()}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())

		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		handler(data, events, &toolCalls)
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Type: llm.EventError, Error: fmt.Errorf("scan stream: %w", err)}
		return
	}

	events <- llm.StreamEvent{Type: llm.EventDone, Data: json.RawMessage(`{"stop_reason": "stop"}`)}
}

// openaiChunkHandler is the standard OpenAI delta parser — no reasoning, no cache metrics.
func openaiChunkHandler(data string, events chan<- llm.StreamEvent, toolCalls *map[int]*openaiToolCall) {
	var chunk openaiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}

	if chunk.Usage != nil {
		raw, _ := json.Marshal(map[string]interface{}{
			"delta": map[string]interface{}{"stop_reason": "stop"},
			"usage": map[string]interface{}{
				"input_tokens":  chunk.Usage.PromptTokens,
				"output_tokens": chunk.Usage.CompletionTokens,
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

	emitToolCallEvents(choice.Delta.ToolCalls, choice.FinishReason, events, toolCalls)
}

// emitToolCallEvents is the shared tool-call accumulation logic.
func emitToolCallEvents(
	deltaCalls []openaiStreamToolCall,
	finishReason string,
	events chan<- llm.StreamEvent,
	toolCalls *map[int]*openaiToolCall,
) {
	if len(deltaCalls) > 0 {
		if *toolCalls == nil {
			*toolCalls = make(map[int]*openaiToolCall)
		}
		for _, tc := range deltaCalls {
			if _, exists := (*toolCalls)[tc.Index]; !exists {
				(*toolCalls)[tc.Index] = &openaiToolCall{ID: tc.ID}
				events <- llm.StreamEvent{Type: llm.EventToolUseStart, Data: mustMarshal(map[string]interface{}{
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"index": tc.Index,
				})}
			}

			existing := (*toolCalls)[tc.Index]
			if tc.ID != "" {
				existing.ID = tc.ID
			}
			if tc.Function.Name != "" {
				existing.Name = tc.Function.Name
			}
			existing.Arguments += tc.Function.Arguments
		}
	}

	if finishReason == "tool_calls" && *toolCalls != nil {
		for i, tc := range *toolCalls {
			raw, _ := json.Marshal(map[string]interface{}{
				"id":    tc.ID,
				"name":  tc.Name,
				"input": json.RawMessage(tc.Arguments),
				"index": i,
			})
			events <- llm.StreamEvent{Type: llm.EventToolUseEnd, Data: raw}
		}
		*toolCalls = nil
	}
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
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiStreamChunk struct {
	Choices []openaiStreamChoice `json:"choices"`
	Usage   *openaiUsage         `json:"usage,omitempty"`
}

type openaiStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openaiStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason"`
}

type openaiStreamDelta struct {
	Role      string                 `json:"role,omitempty"`
	Content   string                 `json:"content,omitempty"`
	ToolCalls []openaiStreamToolCall `json:"tool_calls,omitempty"`
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

func openaiToChatResponse(resp *openaiChatResponse) *llm.ChatResponse {
	result := &llm.ChatResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Usage: llm.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	for _, choice := range resp.Choices {
		var blocks []llm.ContentBlock

		if choice.Message.Content != "" {
			blocks = append(blocks, llm.ContentBlock{Type: "text", Text: choice.Message.Content})
		}

		for _, tc := range choice.Message.ToolCalls {
			blocks = append(blocks, llm.ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}

		result.Messages = append(result.Messages, llm.Message{Role: "assistant", Content: blocks})
		result.StopReason = choice.FinishReason
	}

	return result
}

// --- helpers ---

func decodeRawMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Fallback: raw might be a JSON string literal (e.g. `"hello"` with quotes).
	// Try to trim surrounding quotes if json.Unmarshal failed for other reasons.
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		trimmed = trimmed[1 : len(trimmed)-1]
	}
	return trimmed
}

func openaiConvertContent(blocks []llm.ContentBlock) interface{} {
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}

	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, map[string]interface{}{"type": "text", "text": b.Text})
		case "thinking":
			// Skip thinking blocks in OpenAI format — reasoning is handled
			// separately by provider-specific adapters (e.g. DeepSeek reasoning_content).
			continue
		case "image":
			result = append(result, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]interface{}{
					"url": b.Text,
				},
			})
		case "tool_use":
			continue
		case "tool_result":
			continue
		default:
			continue
		}
	}

	if len(result) == 0 {
		return nil
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

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// Verify interface compliance at compile time.
var _ llm.ProviderAdapter = OpenAIAdapter{}
