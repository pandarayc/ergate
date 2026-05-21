package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const anthropicVersion = "2023-06-01"

// AnthropicAdapter implements ProviderAdapter for the Anthropic Messages API.
type AnthropicAdapter struct{}

func (AnthropicAdapter) BuildRequestBody(req *ChatRequest) map[string]interface{} {
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

	// Extended thinking
	if req.ThinkingBudget > 0 {
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

func anthropicConvertContent(blocks []ContentBlock) interface{} {
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

func (AnthropicAdapter) ParseErrorResponse(statusCode int, respBody, reqBody []byte) *APIError {
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error.Message != "" {
		return &APIError{
			Type:    errResp.Error.Type,
			Message: fmt.Sprintf("%s (req: %s)", errResp.Error.Message, truncateBytes(reqBody, 200)),
			Status:  statusCode,
		}
	}

	return &APIError{
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

func (AnthropicAdapter) Features() FeatureSet {
	return FeatureSet{
		SupportsThinking: true,
		StreamProtocol:   "anthropic-sse",
	}
}

func truncateBytes(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
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

func toChatResponse(resp *anthropicResponse) *ChatResponse {
	result := &ChatResponse{
		ID:         resp.ID,
		Model:      resp.Model,
		StopReason: resp.StopReason,
		Usage: Usage{
			InputTokens:  resp.Usage.InputTokens,
			OutputTokens: resp.Usage.OutputTokens,
		},
	}

	var blocks []ContentBlock
	for _, content := range resp.Content {
		block := ContentBlock{Type: content.Type}
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

	result.Messages = []Message{{Role: "assistant", Content: blocks}}
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
var _ ProviderAdapter = AnthropicAdapter{}
