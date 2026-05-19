package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func init() {
	Register(anthropicProvider{})
}

type anthropicProvider struct{}

func (anthropicProvider) Name() string          { return "anthropic" }
func (anthropicProvider) DefaultBaseURL() string { return "https://api.anthropic.com/v1" }
func (anthropicProvider) NewClient(apiKey, baseURL string) LLMClient {
	return NewAnthropicClient(apiKey, baseURL)
}

// AnthropicClient implements LLMClient for the Anthropic Messages API.
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	adapter    ProviderAdapter
}

// NewAnthropicClient creates a new Anthropic API client.
func NewAnthropicClient(apiKey, baseURL string) *AnthropicClient {
	return &AnthropicClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		adapter: AnthropicAdapter{},
	}
}

// Adapter returns the provider adapter for feature introspection.
func (c *AnthropicClient) Adapter() ProviderAdapter { return c.adapter }

// Chat sends a non-streaming request to the Anthropic API.
func (c *AnthropicClient) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return toChatResponse(&result), nil
}

// ChatStream sends a streaming request to the Anthropic API.
func (c *AnthropicClient) ChatStream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	reqBody := c.adapter.BuildRequestBody(req)
	reqBody["stream"] = true

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
		os.WriteFile("/tmp/ergate_req_body.json", body, 0644)
		return nil, c.adapter.ParseErrorResponse(resp.StatusCode, respBody, body)
	}

	events := make(chan StreamEvent, 64)
	go c.readSSEStream(ctx, resp.Body, events)
	return events, nil
}

func (c *AnthropicClient) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

func (c *AnthropicClient) setHeaders(req *http.Request) {
	for k, v := range c.adapter.Headers(c.apiKey) {
		req.Header.Set(k, v)
	}
}

// readSSEStream reads Server-Sent Events from the response body.
func (c *AnthropicClient) readSSEStream(ctx context.Context, body io.ReadCloser, events chan<- StreamEvent) {
	defer close(events)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	var currentEvent string
	var currentData strings.Builder
	var messageStopSeen bool
	var inputJSON strings.Builder     // accumulates tool_use input JSON across deltas
	var currentBlockType string        // tracks the type of the current content block

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: EventError, Error: ctx.Err()}
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
					events <- StreamEvent{Type: EventMessageStart, Data: rawMsg}
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
						events <- StreamEvent{Type: EventToolUseStart, Data: raw}
					}
				}
			case "content_block_delta":
				var evt anthropicContentBlockDeltaEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					switch evt.Delta.Type {
					case "text_delta":
						raw, _ := json.Marshal(map[string]string{"text": evt.Delta.Text})
						events <- StreamEvent{Type: EventText, Data: raw}
					case "thinking_delta":
						raw, _ := json.Marshal(map[string]string{"thinking": evt.Delta.Thinking})
						events <- StreamEvent{Type: EventThinking, Data: raw}
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
						events <- StreamEvent{Type: EventToolUseEnd, Data: raw}
					}
				}
			case "message_delta":
				messageStopSeen = true
				var evt anthropicMessageDeltaEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					raw, _ := json.Marshal(evt)
					events <- StreamEvent{Type: EventMessageDelta, Data: raw}
				}
			case "message_stop":
				if !messageStopSeen {
					events <- StreamEvent{Type: EventDone, Data: json.RawMessage(`{"stop_reason": "end_turn"}`)}
				}
			case "error":
				var evt anthropicErrorEvent
				if err := json.Unmarshal([]byte(data), &evt); err == nil {
					events <- StreamEvent{Type: EventError,
						Error: &APIError{
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
		events <- StreamEvent{Type: EventError, Error: fmt.Errorf("scan stream: %w", err)}
		return
	}

	events <- StreamEvent{Type: EventDone, Data: json.RawMessage(`{"stop_reason": "end_turn"}`)}
}
