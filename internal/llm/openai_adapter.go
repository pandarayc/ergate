package llm

import (
	"encoding/json"
	"fmt"
)

// OpenAIAdapter implements ProviderAdapter for OpenAI-compatible Chat Completions API.
type OpenAIAdapter struct{}

func (OpenAIAdapter) BuildRequestBody(req *ChatRequest) map[string]interface{} {
	apiReq := map[string]interface{}{
		"model":       req.Model,
		"max_tokens":  req.MaxTokens,
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
		if msg.Role == "user" {
			// Split tool_result blocks into separate role:"tool" messages (OpenAI format)
			var textBlocks []ContentBlock
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
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": openaiConvertContent(msg.Content),
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

func (OpenAIAdapter) ParseErrorResponse(statusCode int, respBody, reqBody []byte) *APIError {
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
			Status:  statusCode,
		}
	}

	return &APIError{
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

func (OpenAIAdapter) Features() FeatureSet {
	return FeatureSet{
		SupportsReasoning: true,
		SupportsVision:    true,
		StreamProtocol:    "openai-sse",
	}
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

func openaiConvertContent(blocks []ContentBlock) interface{} {
	if len(blocks) == 1 && blocks[0].Type == "text" {
		return blocks[0].Text
	}

	result := make([]map[string]interface{}, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			result = append(result, map[string]interface{}{"type": "text", "text": b.Text})
		case "image":
			result = append(result, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]interface{}{
					"url": b.Text,
				},
			})
		}
	}
	return result
}

// Verify interface compliance at compile time.
var _ ProviderAdapter = OpenAIAdapter{}
