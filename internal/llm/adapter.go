package llm

import (
	"context"
	"io"
)

// ProviderAdapter abstracts provider-specific message formatting, error parsing,
// HTTP headers, feature flags, and SSE stream parsing. Each provider implements this once.
type ProviderAdapter interface {
	// BuildRequestBody converts a generic ChatRequest to the provider's API request format.
	BuildRequestBody(req *ChatRequest) map[string]interface{}

	// ParseErrorResponse reads a non-200 response body into an APIError.
	// respBody is already read from the response; reqBody is the original request for context.
	ParseErrorResponse(statusCode int, respBody, reqBody []byte) *APIError

	// Headers returns provider-specific HTTP headers for the given API key.
	Headers(apiKey string) map[string]string

	// Endpoint returns the API path, e.g. "/messages" or "/chat/completions".
	Endpoint() string

	// Features returns the capability set for this provider.
	Features() FeatureSet

	// ParseStream reads a streaming SSE response body and emits StreamEvents.
	// The implementation owns the body lifecycle — it must close body on return.
	// It must close the events channel on return.
	ParseStream(ctx context.Context, body io.ReadCloser, events chan<- StreamEvent)
}

// FeatureSet declares provider capabilities used by the engine and TUI
// to enable/disable features conditionally.
type FeatureSet struct {
	SupportsThinking  bool
	SupportsReasoning bool
	SupportsVision    bool
	StreamProtocol    string // "anthropic-sse" or "openai-sse"
}
