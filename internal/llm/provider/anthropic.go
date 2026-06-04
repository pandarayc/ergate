package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/raydraw/ergate/internal/llm"
)

func init() {
	llm.Register(anthropicProvider{})
}

type anthropicProvider struct{}

func (anthropicProvider) Name() string          { return "anthropic" }
func (anthropicProvider) DefaultBaseURL() string { return "https://api.anthropic.com/v1" }
func (anthropicProvider) NewClient(apiKey, baseURL string) llm.LLMClient {
	return NewAnthropicClient(apiKey, baseURL)
}

// AnthropicClient implements llm.LLMClient for the Anthropic Messages API.
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	adapter    llm.ProviderAdapter
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
func (c *AnthropicClient) Adapter() llm.ProviderAdapter { return c.adapter }

// Chat sends a non-streaming request to the Anthropic API.
func (c *AnthropicClient) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
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

	return anthropicToChatResponse(&result), nil
}

// ChatStream sends a streaming request to the Anthropic API.
func (c *AnthropicClient) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
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
		return nil, c.adapter.ParseErrorResponse(resp.StatusCode, respBody, body)
	}

	events := make(chan llm.StreamEvent, 64)
	go c.adapter.ParseStream(ctx, resp.Body, events)
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
