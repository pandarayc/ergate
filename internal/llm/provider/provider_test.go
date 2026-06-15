package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/raydraw/ergate/internal/llm"
)

// openaiAdapterTestCase holds a request and lets us inspect the built request body.
func buildOpenAIReq(t *testing.T, req *llm.ChatRequest) map[string]interface{} {
	t.Helper()
	adapter := OpenAIAdapter{}
	return adapter.BuildRequestBody(req)
}

// buildDeepSeekReq builds a request body using DeepSeek adapter.
func buildDeepSeekReq(t *testing.T, req *llm.ChatRequest) map[string]interface{} {
	t.Helper()
	adapter := DeepSeekAdapter{}
	return adapter.BuildRequestBody(req)
}

func TestNewLLMClientAnthropic(t *testing.T) {
	client, err := llm.NewLLMClient("anthropic", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	client.Close()
}

func TestNewLLMClientOpenAI(t *testing.T) {
	client, err := llm.NewLLMClient("openai", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	client.Close()
}

func TestNewLLMClientDeepSeek(t *testing.T) {
	client, err := llm.NewLLMClient("deepseek", "test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	client.Close()
}

func TestNewLLMClientUnknown(t *testing.T) {
	_, err := llm.NewLLMClient("unknown", "key", "")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestAnthropicChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Error("missing or wrong API key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("missing anthropic-version header")
		}

		resp := map[string]any{
			"id":          "msg_001",
			"model":       "claude-sonnet-4",
			"stop_reason": "end_turn",
			"content": []map[string]any{
				{"type": "text", "text": "Hello from Claude"},
			},
			"usage": map[string]int{
				"input_tokens":  10,
				"output_tokens": 5,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAnthropicClient("test-key", server.URL)
	defer client.Close()

	req := &llm.ChatRequest{
		Model:     "claude-sonnet-4",
		System:    "You are helpful.",
		Messages:  []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		MaxTokens: 100,
	}

	resp, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("input tokens: got %d, want 10", resp.Usage.InputTokens)
	}
	if len(resp.Messages) == 0 || resp.Messages[0].Content[0].Text != "Hello from Claude" {
		t.Error("unexpected response content")
	}
}

func TestOpenAIChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing or wrong auth header")
		}

		resp := map[string]any{
			"id":    "chatcmpl-001",
			"model": "gpt-4o",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]string{
						"role":    "assistant",
						"content": "Hello from GPT",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewOpenAIClient("test-key", server.URL)
	defer client.Close()

	req := &llm.ChatRequest{
		Model:     "gpt-4o",
		System:    "You are helpful.",
		Messages:  []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		MaxTokens: 100,
	}

	resp, err := client.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("output tokens: got %d, want 5", resp.Usage.OutputTokens)
	}
}

// --- OpenAI adapter BuildRequestBody edge cases ---

func TestOpenAIAdapterAssistantTextAndToolUse(t *testing.T) {
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Read the file"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "text", Text: "Let me check the file"},
					{Type: "tool_use", ID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"/tmp/test"}`)},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})
	assistant := msgs[1]

	// content MUST be a string, not an array
	content, ok := assistant["content"].(string)
	if !ok {
		t.Fatalf("assistant content should be string, got %T: %v", assistant["content"], assistant["content"])
	}
	if content != "Let me check the file" {
		t.Errorf("assistant content = %q, want %q", content, "Let me check the file")
	}

	toolCalls, ok := assistant["tool_calls"].([]map[string]interface{})
	if !ok {
		t.Fatal("assistant should have tool_calls")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(toolCalls))
	}
	if toolCalls[0]["id"] != "call_1" {
		t.Errorf("tool_call id = %v, want call_1", toolCalls[0]["id"])
	}
}

func TestOpenAIAdapterAssistantToolOnly(t *testing.T) {
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "List files"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "tool_use", ID: "call_1", Name: "list", Input: json.RawMessage(`{}`)},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})
	assistant := msgs[1]

	// content must be nil (null in JSON), not an array
	if assistant["content"] != nil {
		t.Errorf("assistant content should be nil when no text blocks, got %T: %v", assistant["content"], assistant["content"])
	}

	toolCalls, ok := assistant["tool_calls"].([]map[string]interface{})
	if !ok {
		t.Fatal("assistant should have tool_calls")
	}
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(toolCalls))
	}
}

func TestOpenAIAdapterToolResultMessages(t *testing.T) {
	// Simulate a full tool-use cycle: user → assistant(tool_use) → tool → assistant(text)
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Read the file"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "tool_use", ID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"test.txt"}`)},
				},
			},
			{
				Role: "user",
				Content: []llm.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Content: json.RawMessage(`"file content here"`), IsError: false},
				},
			},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "text", Text: "Here is the content"},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})

	// msg[0] = user (Read the file)
	// msg[1] = assistant with tool_use → null content + tool_calls
	// msg[2] = tool result
	// msg[3] = assistant with response text

	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	// Verify tool message (msg[2])
	toolMsg := msgs[2]
	if toolMsg["role"] != "tool" {
		t.Errorf("tool msg role = %v, want tool", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id = %v, want call_1", toolMsg["tool_call_id"])
	}
	content, ok := toolMsg["content"].(string)
	if !ok {
		t.Fatalf("tool msg content should be string, got %T", toolMsg["content"])
	}
	if content != "file content here" {
		t.Errorf("tool msg content = %q, want %q", content, "file content here")
	}
}

func TestOpenAIAdapterToolResultSpecialChars(t *testing.T) {
	// Tool result with JSON-like content, quotes, and special characters
	content := `File contains: {"key": "value"} and "quoted text" and newlines`

	encoded, _ := json.Marshal(content)
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "read"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "tool_use", ID: "call_1", Name: "read", Input: json.RawMessage(`{}`)},
				},
			},
			{
				Role: "user",
				Content: []llm.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Content: json.RawMessage(encoded), IsError: false},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})
	toolMsg := msgs[2]

	decoded, ok := toolMsg["content"].(string)
	if !ok {
		t.Fatalf("tool msg content should be string, got %T", toolMsg["content"])
	}
	if decoded != content {
		t.Errorf("tool msg content = %q, want original content", decoded)
	}
}

func TestOpenAIAdapterToolResultEmpty(t *testing.T) {
	encoded, _ := json.Marshal("")
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "run"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "tool_use", ID: "call_1", Name: "exec", Input: json.RawMessage(`{}`)},
				},
			},
			{
				Role: "user",
				Content: []llm.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_1", Content: json.RawMessage(encoded), IsError: false},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})
	toolMsg := msgs[2]

	decoded, ok := toolMsg["content"].(string)
	if !ok {
		t.Fatalf("tool msg content should be string, got %T", toolMsg["content"])
	}
	// Empty string is valid OpenAI content — the message will be sent but provide no info.
	// The adapter must not crash or return garbage.
	if decoded != "" {
		t.Errorf("tool msg content = %q, want empty string", decoded)
	}
}

func TestOpenAIAdapterAssistantTextOnly(t *testing.T) {
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
			{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Hello from assistant"}}},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})
	assistant := msgs[1]

	content, ok := assistant["content"].(string)
	if !ok {
		t.Fatalf("assistant content should be string, got %T", assistant["content"])
	}
	if content != "Hello from assistant" {
		t.Errorf("assistant content = %q, want %q", content, "Hello from assistant")
	}
	// No tool_calls for text-only assistant
	if _, exists := assistant["tool_calls"]; exists {
		t.Error("assistant should not have tool_calls for text-only response")
	}
}

func TestOpenAIAdapterSystemMessagePreserved(t *testing.T) {
	req := &llm.ChatRequest{
		Model:     "gpt-4o",
		System:    "You are a helpful assistant.",
		Messages:  []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	msgs := body["messages"].([]map[string]interface{})

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Errorf("first msg role = %v, want system", msgs[0]["role"])
	}
}

// --- decodeRawMessage edge cases ---

func TestDecodeRawMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    json.RawMessage
		expected string
	}{
		{
			name:     "empty",
			input:    json.RawMessage{},
			expected: "",
		},
		{
			name:     "nil",
			input:    nil,
			expected: "",
		},
		{
			name:     "plain string",
			input:    json.RawMessage(`"hello"`),
			expected: "hello",
		},
		{
			name:     "special chars",
			input:    json.RawMessage(`"hello \"world\" & <stuff>"`),
			expected: `hello "world" & <stuff>`,
		},
		{
			name:     "unquoted raw (fallback trim)",
			input:    json.RawMessage(`"quoted"`),
			expected: "quoted",
		},
		{
			name:     "unquoted without quotes",
			input:    json.RawMessage(`plain`),
			expected: "plain", // can't unmarshal as string, fallback trims but no quotes
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeRawMessage(tt.input)
			if got != tt.expected {
				t.Errorf("decodeRawMessage(%s) = %q, want %q", string(tt.input), got, tt.expected)
			}
		})
	}
}

// --- Full JSON marshalling roundtrip test ---
// Ensures the final JSON sent to the API is valid OpenAI format.

func TestOpenAIAdapterFullJSONOutput(t *testing.T) {
	req := &llm.ChatRequest{
		Model: "gpt-4o",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Read files"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "text", Text: "Let me check"},
					{Type: "tool_use", ID: "call_abc", Name: "read", Input: json.RawMessage(`{"path":"test.go"}`)},
				},
			},
			{
				Role: "user",
				Content: []llm.ContentBlock{
					{Type: "tool_result", ToolUseID: "call_abc", Content: json.RawMessage(`"package main\n\nfunc main() {}"`), IsError: false},
				},
			},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "text", Text: `The file contains: package main`},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildOpenAIReq(t, req)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Unmarshal back to check structure
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	msgsRaw, ok := parsed["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages should be array, got %T", parsed["messages"])
	}
	if len(msgsRaw) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgsRaw))
	}

	// Verify assistant 1 has string content + tool_calls
	asst1 := msgsRaw[1].(map[string]interface{})
	if _, ok := asst1["content"].(string); !ok {
		t.Errorf("assistant1 content type = %T, want string", asst1["content"])
	}
	if _, ok := asst1["tool_calls"]; !ok {
		t.Error("assistant1 should have tool_calls")
	}

	// Verify tool message
	toolMsg := msgsRaw[2].(map[string]interface{})
	if toolMsg["role"] != "tool" {
		t.Errorf("tool msg role = %v, want tool", toolMsg["role"])
	}

	// Verify assistant 2 has string content, no tool_calls
	asst2 := msgsRaw[3].(map[string]interface{})
	if _, ok := asst2["content"].(string); !ok {
		t.Errorf("assistant2 content type = %T, want string", asst2["content"])
	}
	if _, exists := asst2["tool_calls"]; exists {
		t.Error("assistant2 should not have tool_calls")
	}

	// The final JSON must parse without errors — this is what the API receives.
	t.Logf("Generated JSON (%d bytes): %s", len(raw), raw)
}

// --- DeepSeek adapter share the same fix ---

func TestDeepSeekAdapterAssistantTextAndToolUse(t *testing.T) {
	req := &llm.ChatRequest{
		Model: "deepseek-chat",
		Messages: []llm.Message{
			{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "Read the file"}}},
			{
				Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "text", Text: "Let me check"},
					{Type: "thinking", Thinking: "I need to read the file first"},
					{Type: "tool_use", ID: "call_1", Name: "read", Input: json.RawMessage(`{}`)},
				},
			},
		},
		MaxTokens: 100,
	}

	body := buildDeepSeekReq(t, req)
	msgs := body["messages"].([]map[string]interface{})
	assistant := msgs[1]

	// content must be string
	content, ok := assistant["content"].(string)
	if !ok {
		t.Fatalf("assistant content should be string, got %T: %v", assistant["content"], assistant["content"])
	}
	if content != "Let me check" {
		t.Errorf("assistant content = %q, want %q", content, "Let me check")
	}

	// reasoning_content should be present
	if assistant["reasoning_content"] != "I need to read the file first" {
		t.Errorf("reasoning_content = %v, want thinking text", assistant["reasoning_content"])
	}

	// tool_calls should have the tool_use
	if _, ok := assistant["tool_calls"]; !ok {
		t.Error("assistant should have tool_calls")
	}
}

// --- verify valid JSON schema for OpenAI messages ---

func TestOpenAIAdapterJSONSchemaCompliance(t *testing.T) {
	tests := []struct {
		name        string
		messages    []llm.Message
		checkSchema func(t *testing.T, body map[string]interface{})
	}{
		{
			name: "user text only",
			messages: []llm.Message{
				{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}},
			},
			checkSchema: func(t *testing.T, body map[string]interface{}) {
				msgs := body["messages"].([]map[string]interface{})
				if msgs[0]["content"] != "hello" {
					t.Error("user content should be string")
				}
			},
		},
		{
			name: "tool_use then tool_result",
			messages: []llm.Message{
				{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "run"}}},
				{Role: "assistant", Content: []llm.ContentBlock{
					{Type: "tool_use", ID: "t1", Name: "exec", Input: json.RawMessage(`{}`)},
				}},
				{Role: "user", Content: []llm.ContentBlock{
					{Type: "tool_result", ToolUseID: "t1", Content: json.RawMessage(`"ok"`)},
				}},
			},
			checkSchema: func(t *testing.T, body map[string]interface{}) {
				msgs := body["messages"].([]map[string]interface{})
				if msgs[2]["role"] != "tool" {
					t.Errorf("msg[2] role = %v, want tool", msgs[2]["role"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &llm.ChatRequest{
				Model:     "gpt-4o",
				Messages:  tt.messages,
				MaxTokens: 100,
			}
			body := buildOpenAIReq(t, req)
			tt.checkSchema(t, body)

			// Also verify it roundtrips through JSON without error
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}
			// Must be valid JSON
			if !json.Valid(raw) {
				t.Fatal("generated JSON is not valid")
			}
			// Must NOT contain "content":[...] for assistant messages
			if strings.Contains(string(raw), `"content":[`) {
				t.Error("JSON should not contain content array at top level for assistant messages")
			}
		})
	}
}
