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
	llm.Register(openaiProvider{})
}

type openaiProvider struct{}

func (openaiProvider) Name() string           { return "openai" }
func (openaiProvider) DefaultBaseURL() string { return "https://api.openai.com/v1" }
func (openaiProvider) NewClient(apiKey, baseURL string) llm.LLMClient {
	return NewOpenAIClient(apiKey, baseURL)
}

// OpenAIClient implements llm.LLMClient for OpenAI-compatible Chat Completions API.
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	adapter    llm.ProviderAdapter
}

// NewOpenAIClient creates a new OpenAI-compatible API client with the default OpenAI adapter.
func NewOpenAIClient(apiKey, baseURL string) *OpenAIClient {
	return NewOpenAIClientWithAdapter(apiKey, baseURL, OpenAIAdapter{})
}

// NewOpenAIClientWithAdapter creates a client with a custom adapter for protocol extensions
// (e.g. DeepSeek reasoning_content, cache metrics).
func NewOpenAIClientWithAdapter(apiKey, baseURL string, adapter llm.ProviderAdapter) *OpenAIClient {
	return &OpenAIClient{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		adapter: adapter,
	}
}

// Adapter returns the provider adapter for feature introspection.
func (c *OpenAIClient) Adapter() llm.ProviderAdapter { return c.adapter }

// Chat sends a non-streaming request.
func (c *OpenAIClient) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
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

	return openaiToChatResponse(&result), nil
}

// ChatStream sends a streaming request.
func (c *OpenAIClient) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
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

	events := make(chan llm.StreamEvent, 64)
	go c.adapter.ParseStream(ctx, resp.Body, events)
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
