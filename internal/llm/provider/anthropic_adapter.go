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

const anthropicVersion = "2023-06-01"

// AnthropicAdapter implements llm.ProviderAdapter for the Anthropic Messages API.
// Also serves as the adapter for DeepSeek's Anthropic-compatible endpoint.
type AnthropicAdapter struct {
	// BaseURL is used to detect DeepSeek vs real Anthropic for thinking params.
	BaseURL string
}

func (a AnthropicAdapter) BuildRequestBody(req *llm.ChatRequest) map[string]interface{} {
	apiReq := map[string]interface{}{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
	}

	if req.Temperature > 0 {
		apiReq["temperature"] = req.Temperature
	}

	if req.System != "" {
		apiReq["system"] = anthropicSystemPrompt(req.System)
	}

	// Extended thinking: DeepSeek and Anthropic use different parameters.
	// - Anthropic: {"thinking": {"type": "enabled", "budget_tokens": N}}
	// - DeepSeek:  thinking is enabled by default; budget_tokens is not supported.
	//   Simply omit the thinking block — DeepSeek handles effort automatically.
	if req.ThinkingBudget > 0 && !strings.Contains(a.BaseURL, "deepseek") {
		apiReq["thinking"] = map[string]interface{}{
			"type":         "enabled",
			"budget_tokens": req.ThinkingBudget,
		}
	}

	messages := make([]map[string]interface{}, 0, len(req.Messages))
	for _, msg := range req.Messages {
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": anthropicConvertContent(msg.Content),
		}
		messages = append(messages, m)
	}
	apiReq["messages"] = messages

	if len(req.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]interface{}{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": json.RawMessage(t.InputSchema),
			})
		}
		apiReq["tools"] = tools
	}

	return apiReq
}

const cacheBoundary = "<!-- CACHE_BOUNDARY: content above is stable and cacheable -->"

// anthropicSystemPrompt returns either a plain string or, when the prompt
// includes the cache boundary marker, a two-element array with the stable
// prefix marked for caching.
func anthropicSystemPrompt(prompt string) interface{} {
	idx := strings.Index(prompt, cacheBoundary)
	if idx < 0 {
		return prompt
	}

	stable := strings.TrimRight(prompt[:idx], "\n ")
	dynamic := prompt[idx+len(cacheBoundary):]

	blocks := []map[string]interface{}{
		{
			"type": "text",
			"text": stable,
			"cache_control": map[string]interface{}{
				"type": "ephemeral",
			},
		},
	}
	if dynamic = strings.TrimSpace(dynamic); dynamic != "" {
		blocks = append(blocks, map[string]interface{}{
			"type": "text",
			"text": dynamic,
		})
	}
	return blocks
}

func anthropicConvertContent(blocks []llm.ContentBlock) interface{} {
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}

	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		item := map[string]interface{}{"type": b.Type}
		switch b.Type {
		case "text":
			item["text"] = b.Text
			if b.Cached != nil {
				item["cache_control"] = b.Cached
			}
		case "thinking":
			item["thinking"] = b.Thinking
		case "tool_use":
			item["id"] = b.ID
			item["name"] = b.Name
			item["input"] = b.Input
		case "tool_result":
			item["tool_use_id"] = b.ToolUseID
			item["content"] = b.Content
			if b.IsError {
				item["is_error"] = true
			}
		case "image":
			item["source"] = map[string]interface{}{
				"type":       "base64",
				"media_type": "image/png",
				"data":       b.Text,
			}
		}
		result = append(result, item)
	}
	return result
}

func (AnthropicAdapter) ParseErrorResponse(statusCode int, respBody, reqBody []byte) *llm.APIError {
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
		return &llm.APIError{
			Type:    errResp.Error.Type,
			Message: fmt.Sprintf("%s (req: %s)", errResp.Error.Message, truncateBytes(reqBody, 200)),
			Status:  statusCode,
		}
	}

	return &llm.APIError{
		Type:    fmt.Sprintf("http_%d", statusCode),
		Message: fmt.Sprintf("HTTP %d: %s (req: %s)", statusCode, string(respBody), truncateBytes(reqBody, 200)),
		Status:  statusCode,
	}
}

func (AnthropicAdapter) Headers(apiKey string) map[string]string {
	return map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": anthropicVersion,
		"Content-Type":      "application/json",
	}
}

func (AnthropicAdapter) Endpoint() string { return "/messages" }

func (AnthropicAdapter) Features() llm.FeatureSet {
	return llm.FeatureSet{
		SupportsThinking: true,
		StreamProtocol:   "anthropic-sse",
	}
}

// ParseStream implements the Anthropic SSE streaming protocol.
func (AnthropicAdapter) ParseStream(ctx context.Context, body io.ReadCloser, events chan<- llm.StreamEvent) {
	defer close(events)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var currentEvent string
	var currentData strings.Builder
	var messageStopSeen bool
	var inputJSON strings.Builder
	var currentBlockType string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- llm.StreamEvent{Type: llm.EventError, Error: ctx.Err()}
			return
		default:
		}

		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "event: "):
			currentEvent = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			currentData.WriteString(strings.TrimPrefix(line, "data: "))
		case line == "":
			if currentData.Len() == 0 {
				continue
			}

			data := currentData.String()

			switch currentEvent {
			case "message_start":
				var evt anthropicMessageStartEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					rawMsg, _ := json.Marshal(evt.Message)
					events <- llm.StreamEvent{Type: llm.EventMessageStart, Data: rawMsg}
				}
			case "content_block_start":
				var evt anthropicContentBlockStartEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					currentBlockType = evt.ContentBlock.Type
					if evt.ContentBlock.Type == "tool_use" {
						inputJSON.Reset()
						raw, _ := json.Marshal(map[string]interface{}{
							"id":    evt.ContentBlock.ID,
							"name":  evt.ContentBlock.Name,
							"index": evt.Index,
						})
						events <- llm.StreamEvent{Type: llm.EventToolUseStart, Data: raw}
					}
				}
			case "content_block_delta":
				var evt anthropicContentBlockDeltaEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					switch evt.Delta.Type {
					case "text_delta":
						raw, _ := json.Marshal(map[string]string{"text": evt.Delta.Text})
						events <- llm.StreamEvent{Type: llm.EventText, Data: raw}
					case "thinking_delta":
						raw, _ := json.Marshal(map[string]string{"thinking": evt.Delta.Thinking})
						events <- llm.StreamEvent{Type: llm.EventThinking, Data: raw}
					case "input_json_delta":
						inputJSON.WriteString(evt.Delta.PartialJSON)
					}
				}
			case "content_block_stop":
				var evt anthropicContentBlockStopEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					if currentBlockType == "tool_use" {
						raw, _ := json.Marshal(map[string]interface{}{
							"index": evt.Index,
							"input": json.RawMessage(inputJSON.String()),
						})
						events <- llm.StreamEvent{Type: llm.EventToolUseEnd, Data: raw}
					}
				}
			case "message_delta":
				messageStopSeen = true
				var evt anthropicMessageDeltaEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					raw, _ := json.Marshal(evt)
					events <- llm.StreamEvent{Type: llm.EventMessageDelta, Data: raw}
				}
			case "message_stop":
				if !messageStopSeen {
					events <- llm.StreamEvent{Type: llm.EventDone, Data: json.RawMessage(`{"stop_reason": "end_turn"}`)}
				}
			case "error":
				var evt anthropicErrorEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					events <- llm.StreamEvent{Type: llm.EventError,
						Error: &llm.APIError{
							Type:    evt.Error.Type,
							Message: evt.Error.Message,
							Status:  respStatus(evt.Error.Type),
						},
					}
				}
			case "ping":
			}

			currentEvent = ""
			currentData.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		events <- llm.StreamEvent{Type: llm.EventError, Error: fmt.Errorf("scan stream: %w", err)}
		return
	}

	events <- llm.StreamEvent{Type: llm.EventDone, Data: json.RawMessage(`{"stop_reason": "end_turn"}`)}
}

// --- Anthropic API types ---

type anthropicResponse struct {
	ID         string             `json:"id"`
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Content    []anthropicContent `json:"content"`
	Usage      anthropicUsage     `json:"usage"`
}

type anthropicContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicMessageStartEvent struct {
	Message anthropicResponse `json:"message"`
}

type anthropicContentBlockStartEvent struct {
	Index        int              `json:"index"`
	ContentBlock anthropicContent `json:"content_block"`
}

type anthropicContentBlockDeltaEvent struct {
	Index int            `json:"index"`
	Delta anthropicDelta `json:"delta"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type anthropicContentBlockStopEvent struct {
	Index int `json:"index"`
}

type anthropicMessageDeltaEvent struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage anthropicUsage `json:"usage"`
}

type anthropicErrorEvent struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func anthropicToChatResponse(resp *anthropicResponse) *llm.ChatResponse {
	result := &llm.ChatResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		StopReason: resp.StopReason,
		Usage: llm.Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}

	var blocks []llm.ContentBlock
	for _, content := range resp.Content {
		block := llm.ContentBlock{Type: content.Type}
		switch content.Type {
		case "text":
			block.Text = content.Text
		case "tool_use":
			block.ID = content.ID
			block.Name = content.Name
			block.Input = content.Input
		}
		blocks = append(blocks, block)
	}

	result.Messages = []llm.Message{{Role: "assistant", Content: blocks}}
	return result
}

func respStatus(errType string) int {
	switch {
	case strings.Contains(errType, "rate_limit"):
		return 429
	case strings.Contains(errType, "overloaded"):
		return 529
	case strings.Contains(errType, "authentication"):
		return 401
	case strings.Contains(errType, "permission"):
		return 403
	case strings.Contains(errType, "not_found"):
		return 404
	case strings.Contains(errType, "invalid_request"):
		return 400
	default:
		return 500
	}
}

// Verify interface compliance at compile time.
var _ llm.ProviderAdapter = AnthropicAdapter{}
