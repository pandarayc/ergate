package engine

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/llm"
	"github.com/raydraw/ergate/internal/tool"
)

// mockLLMClient implements llm.LLMClient for testing.
type mockLLMClient struct {
	responses []*llm.ChatResponse
	callCount int
}

func (m *mockLLMClient) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.callCount < len(m.responses) {
		resp := m.responses[m.callCount]
		m.callCount++
		return resp, nil
	}
	return &llm.ChatResponse{
		Messages: []llm.Message{{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Done."}}}},
	}, nil
}

func (m *mockLLMClient) ChatStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamEvent, error) {
	events := make(chan llm.StreamEvent, 64)

	go func() {
		defer close(events)

		if m.callCount < len(m.responses) {
			resp := m.responses[m.callCount]
			m.callCount++

			for _, msg := range resp.Messages {
				for _, block := range msg.Content {
					switch block.Type {
					case "text":
						data, _ := json.Marshal(map[string]string{"text": block.Text})
						events <- llm.StreamEvent{Type: llm.EventText, Data: data}
					case "tool_use":
						data, _ := json.Marshal(map[string]interface{}{
							"id":    block.ID,
							"name":  block.Name,
							"input": block.Input,
						})
						events <- llm.StreamEvent{Type: llm.EventToolUseStart, Data: data}
						events <- llm.StreamEvent{Type: llm.EventToolUseEnd, Data: data}
					}
				}
			}

			// Emit usage
			usageData, _ := json.Marshal(map[string]interface{}{
				"delta": map[string]string{"stop_reason": "end_turn"},
				"usage": map[string]int{"input_tokens": 10, "output_tokens": 5},
			})
			events <- llm.StreamEvent{Type: llm.EventMessageDelta, Data: usageData}
		}

		events <- llm.StreamEvent{Type: llm.EventDone}
	}()

	return events, nil
}

func (m *mockLLMClient) Close() error { return nil }

func (m *mockLLMClient) Adapter() llm.ProviderAdapter { return nil }

// echoTool is a test tool that echoes its input.
type echoTool struct {
	tool.BaseTool
}

func newEchoTool() *echoTool {
	return &echoTool{
		BaseTool: tool.NewBaseTool(
			"echo",
			"Echoes the input back",
			json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
			tool.WithConcurrencySafe(),
		),
	}
}

type echoInput struct {
	Message string `json:"message"`
}

func (t *echoTool) Execute(ctx context.Context, input json.RawMessage, exec *tool.ExecContext) (*tool.ToolResult, error) {
	var in echoInput
	json.Unmarshal(input, &in)
	return &tool.ToolResult{
		Success: true,
		Content: "Echo: " + in.Message,
	}, nil
}

func TestEngineTextOnlyResponse(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxTurns = 5

	mock := &mockLLMClient{
		responses: []*llm.ChatResponse{
			{
				Messages: []llm.Message{
					{
						Role: "assistant",
						Content: []llm.ContentBlock{
							{Type: "text", Text: "Hello, how can I help?"},
						},
					},
				},
			},
		},
	}

	reg := tool.NewRegistry()
	eng := New(cfg, mock, reg, Context{})

	events := make(chan Event, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = eng.Run(ctx, "Hi", events)
	}()

	var gotText bool
	for event := range events {
		if event.Type == EventText {
			if s, ok := event.Data.(string); ok && s == "Hello, how can I help?" {
				gotText = true
			}
		}
	}

	if !gotText {
		t.Error("Expected text response 'Hello, how can I help?'")
	}
}

func TestEngineToolUseResponse(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxTurns = 5

	mock := &mockLLMClient{
		responses: []*llm.ChatResponse{
			{
				Messages: []llm.Message{
					{
						Role: "assistant",
						Content: []llm.ContentBlock{
							{
								Type: "tool_use",
								ID:   "tu_001",
								Name: "echo",
								Input: json.RawMessage(`{"message":"hello world"}`),
							},
						},
					},
				},
			},
		},
	}

	reg := tool.NewRegistry()
	reg.Register(newEchoTool())

	eng := New(cfg, mock, reg, Context{})

	events := make(chan Event, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = eng.Run(ctx, "Echo hello world", events)
	}()

	var gotToolUse, gotToolResult bool
	for event := range events {
		switch event.Type {
		case EventToolUse:
			if data, ok := event.Data.(map[string]interface{}); ok {
				if data["name"] == "echo" {
					gotToolUse = true
				}
			}
		case EventToolResult:
			if data, ok := event.Data.(map[string]interface{}); ok {
				if data["name"] == "echo" {
					gotToolResult = true
				}
			}
		}
	}

	if !gotToolUse {
		t.Error("Expected tool use event for 'echo'")
	}
	if !gotToolResult {
		t.Error("Expected tool result event for 'echo'")
	}
}

func TestEngineMessagesPersistence(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxTurns = 5

	mock := &mockLLMClient{
		responses: []*llm.ChatResponse{
			{
				Messages: []llm.Message{
					{Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: "Response 1"}}},
				},
			},
		},
	}

	reg := tool.NewRegistry()
	eng := New(cfg, mock, reg, Context{})

	events := make(chan Event, 64)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = eng.Run(ctx, "Hello", events) }()
	for range events {
	}

	msgs := eng.Messages()
	if len(msgs) < 2 {
		t.Errorf("Expected at least 2 messages (user + assistant), got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("Expected first message to be 'user', got %q", msgs[0].Role)
	}
	if msgs[1].Role != "assistant" {
		t.Errorf("Expected second message to be 'assistant', got %q", msgs[1].Role)
	}
}
