//go:build integration
//
// Run:
//   DEEPSEEK_API_KEY=sk-xxx go test -tags=integration -run TestFoldVsAutoCompact -v -timeout 5m ./internal/engine/

package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
	"github.com/raydraw/ergate/internal/llm"
	_ "github.com/raydraw/ergate/internal/llm/provider"
	"github.com/raydraw/ergate/internal/tool"
)

// TestDeepSeekCache10Turns runs a 10-turn conversation against DeepSeek
// and logs per-turn cache hit/miss tokens. AutoCompact triggers naturally.
func TestDeepSeekCache10Turns(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run this test")
	}

	cfg := &config.Config{
		APIProvider:    config.ProviderDeepSeek,
		Model:          "deepseek-v4-flash",
		MaxTurns:       5,
		MaxTokens:      8192,
		Temperature:    0.0,
		SessionDir:     os.TempDir(),
		PermissionMode: config.PermModeBypass,
		Providers: map[string]config.ProviderConfig{
			"deepseek": {
				APIKey: apiKey,
				Models: map[string]config.ModelOptions{
					"deepseek-v4-flash": {
						ContextWindow:     32768,
						CostPer1MIn:       1.0,
						CostPer1MInCached: 0.02,
						CostPer1MOut:      2.0,
					},
				},
			},
		},
	}

	client, err := llm.NewLLMClient("deepseek", apiKey, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	todoMgr := tool.NewTodoManager()
	toolReg := tool.NewRegistry()
	tool.RegisterBuiltins(toolReg, todoMgr)

	ectx := engine.Context{
		PermMgr: &bypassPermMgr{},
		PermCtx: tool.PermissionContext{Mode: tool.PermModeBypassPermissions},
	}
	eng := engine.New(cfg, client, toolReg, ectx)

	questions := []string{
		"Read go.mod and tell me what Go version this project needs.",
		"What is the module path?",
		"Read internal/llm/client.go and summarize its main types.",
		"What is the LLMClient interface? List its methods.",
		"Read internal/llm/adapter.go and explain ProviderAdapter.",
		"Compare LLMClient and ProviderAdapter.",
		"Read internal/compact/compact.go. How does AutoCompact work?",
		"What are SnipCompact and MicroCompact?",
		"Read internal/config/config.go. What providers are supported?",
		"Summarize this project's architecture in one paragraph.",
	}

	ctx := context.Background()
	var totalHit, totalMiss, totalIn, totalOut int

	fmt.Println()
	fmt.Println("=============================================================")
	fmt.Println("  DeepSeek Prefix-Cache Benchmark: 10-Turn Conversation")
	fmt.Println("  (AutoCompact — current behavior)")
	fmt.Println("-------------------------------------------------------------")
	fmt.Printf("  %-3s | %-8s | %-8s | %-8s | %-8s | %s\n",
		"#", "In", "Out", "Hit", "Miss", "Hit%")
	fmt.Println("------+----------+----------+----------+----------+----------")

	for i, q := range questions {
		events := make(chan engine.Event, 256)
		errCh := make(chan error, 1)
		go func() { errCh <- eng.Run(ctx, q, events) }()
		for range events {
		}
		if err := <-errCh; err != nil {
			t.Logf("turn %d: %v", i+1, err)
		}

		hitT, missT := eng.CacheUsage()
		inT, outT := eng.TotalUsage()
		turnHit := hitT - totalHit
		turnMiss := missT - totalMiss
		turnIn := inT - totalIn
		turnOut := outT - totalOut
		totalHit, totalMiss = hitT, missT
		totalIn, totalOut = inT, outT

		hitPct := 0.0
		if turnHit+turnMiss > 0 {
			hitPct = float64(turnHit) / float64(turnHit+turnMiss) * 100
		}
		fmt.Printf("  %-3d | %-8d | %-8d | %-8d | %-8d | %5.1f%%\n",
			i+1, turnIn, turnOut, turnHit, turnMiss, hitPct)

		time.Sleep(300 * time.Millisecond)
	}

	fmt.Println("------+----------+----------+----------+----------+----------")
	fmt.Printf("  Tot │ In: %-6d | Out: %-5d | Hit: %-5d | Miss: %-5d\n",
		totalIn, totalOut, totalHit, totalMiss)
	if totalHit+totalMiss > 0 {
		fmt.Printf("      │ Overall hit: %5.1f%%\n", float64(totalHit)/float64(totalHit+totalMiss)*100)
	}
	fmt.Println()
}

// TestFoldVsAutoCompact builds a 3-turn conversation using the raw LLM client
// (no engine), then sends two turn-4 requests with different compaction strategies
// and compares their cache metrics.
func TestFoldVsAutoCompact(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run this test")
	}

	client, err := llm.NewLLMClient("deepseek", apiKey, "")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	model := "deepseek-v4-flash"
	sysPrompt := `You are Ergate, a helpful AI assistant with access to software engineering tools for reading, writing, searching, and executing code.

## Environment
- Working directory: /data/projects/personal/ergate
- Date: 2026-06-12
- Platform: linux

## Instructions
- Use the available tools to help the user with their coding tasks.
- Be concise and direct in your responses.
- When reading code, explain what you find clearly.`

	// Turn 1: read a file
	req1 := &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 4096,
		Messages: []llm.Message{
			llm.NewUserMessage("Read the file go.mod and tell me what Go version is required."),
		},
	}
	assistantText, hit1, miss1, err := streamChat(context.Background(), client, req1)
	if err != nil {
		t.Fatal(err)
	}
	msgs := []llm.Message{
		req1.Messages[0],
		llm.Message{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: assistantText}}},
	}
	t.Logf("turn 1: hit=%d miss=%d", hit1, miss1)

	time.Sleep(2 * time.Second)

	// Turn 2: follow-up question (no tool, just text)
	req2 := &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 4096,
		Messages: append(deepCopy(msgs),
			llm.NewUserMessage("What dependencies does this project have? Be specific about their purposes."),
		),
	}
	assistantText2, hit2, miss2, err := streamChat(context.Background(), client, req2)
	if err != nil {
		t.Fatal(err)
	}
	msgs = append(msgs,
		llm.NewUserMessage("What dependencies does this project have? Be specific about their purposes."),
		llm.Message{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: assistantText2}}},
	)
	t.Logf("turn 2: hit=%d miss=%d", hit2, miss2)

	time.Sleep(2 * time.Second)

	// Turn 3: another follow-up
	req3 := &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 4096,
		Messages: append(deepCopy(msgs),
			llm.NewUserMessage("Explain the project architecture. What are the main packages and how do they fit together?"),
		),
	}
	assistantText3, hit3, miss3, err := streamChat(context.Background(), client, req3)
	if err != nil {
		t.Fatal(err)
	}
	msgs = append(msgs,
		llm.NewUserMessage("Explain the project architecture. What are the main packages and how do they fit together?"),
		llm.Message{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: assistantText3}}},
	)
	t.Logf("turn 3: hit=%d miss=%d", hit3, miss3)

	// Now we have 6 messages (3 turns of user+assistant).
	t.Logf("prefix built: %d messages", len(msgs))

	// Simulate compaction: generate a summary of the first 2 turns.
	summary := summarizeMessages(client, msgs[:4], model)
	t.Logf("summary: %d chars", len(summary))

	// The "tail" = last 2 messages (turn 3's user + assistant).
	// These are the messages that Fold preserves and AutoCompact drops.
	tail := deepCopy(msgs[4:]) // last 2 messages
	user4 := "Based on everything we discussed, what is the most important architectural decision in this project?"

	// ── Strategy comparison ─────────────────────────────────────────
	fmt.Println()
	fmt.Println("=============================================================")
	fmt.Println("  Compaction Strategy A/B Comparison")
	fmt.Println("  (same 3-turn prefix, different turn-4 compaction)")
	fmt.Println("=============================================================")
	fmt.Println()

	// Strategy A: AutoCompact — ALL messages replaced by summary
	reqA := &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 4096,
		Messages: []llm.Message{
			llm.Message{Type: llm.MsgSystem, Role: "system", Subtype: "compact_boundary",
				Content: []llm.ContentBlock{{Type: "text", Text: summary}}},
			llm.NewUserMessage("[Conversation compressed]\n\n" + summary),
			llm.Message{Type: llm.MsgAssistant, Role: "assistant",
				Content: []llm.ContentBlock{{Type: "text", Text: "Understood."}}},
			llm.NewUserMessage(user4),
		},
	}

	// Strategy B: Fold — prefix replaced, tail preserved
	reqB := &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 4096,
		Messages: append(
			[]llm.Message{
				llm.Message{Type: llm.MsgSystem, Role: "system", Subtype: "compact_boundary",
					Content: []llm.ContentBlock{{Type: "text", Text: summary}}},
				llm.NewUserMessage("[Conversation compressed]\n\n" + summary),
				llm.Message{Type: llm.MsgAssistant, Role: "assistant",
					Content: []llm.ContentBlock{{Type: "text", Text: "Understood. Continuing with context."}}},
			},
			append(tail,
				llm.NewUserMessage(user4),
			)...,
		),
	}

	// Strategy B first (Fold — no cache contamination from AutoCompact).
	_, hitB1, missB1, err := streamChat(context.Background(), client, reqB)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Fold: hit=%d miss=%d", hitB1, missB1)

	time.Sleep(2 * time.Second)

	// Strategy A second (AutoCompact — runs after Fold, benefits from cached summary prefix).
	_, hitA1, missA1, err := streamChat(context.Background(), client, reqA)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AutoCompact: hit=%d miss=%d", hitA1, missA1)

	// Print comparison.
	hitA, missA := hitA1, missA1
	hitB, missB := hitB1, missB1

	pctA := 0.0
	if hitA+missA > 0 {
		pctA = float64(hitA) / float64(hitA+missA) * 100
	}
	pctB := 0.0
	if hitB+missB > 0 {
		pctB = float64(hitB) / float64(hitB+missB) * 100
	}

	// Estimate byte sizes of tail messages for both strategies.
	rawTail, _ := json.Marshal(tail)
	tailBytes := len(rawTail)

	fmt.Println()
	fmt.Printf("  %-20s | %-10s | %-10s | %-8s\n", "Strategy", "Hit", "Miss", "Hit%")
	fmt.Println("  ---------------------+------------+------------+----------")
	fmt.Printf("  %-20s | %-10d | %-10d | %5.1f%%\n", "A) AutoCompact", hitA, missA, pctA)
	fmt.Printf("  %-20s | %-10d | %-10d | %5.1f%%\n",
		fmt.Sprintf("B) Fold (tail=%dmsgs,%db)", len(tail), tailBytes), hitB, missB, pctB)
	fmt.Println()

	if hitB > hitA {
		improvement := float64(hitB-hitA) / float64(hitA) * 100
		fmt.Printf("  Fold preserves %d bytes of tail → %d more hit tokens (%.0f%% improvement)\n",
			tailBytes, hitB-hitA, improvement)
	}
	fmt.Println("=============================================================")
}

// streamChat performs a streaming chat request and returns the full text
// plus cache hit/miss token counts extracted from usage events.
func streamChat(ctx context.Context, client llm.LLMClient, req *llm.ChatRequest) (text string, hitTokens, missTokens int, err error) {
	stream, err := client.ChatStream(ctx, req)
	if err != nil {
		return "", 0, 0, err
	}
	var buf strings.Builder
	for evt := range stream {
		switch evt.Type {
		case llm.EventText:
			var d struct{ Text string }
			json.Unmarshal(evt.Data, &d)
			buf.WriteString(d.Text)
		case llm.EventError:
			err = evt.Error
		case llm.EventMessageDelta:
			var d struct {
				Usage struct {
					CacheHitTokens  int `json:"prompt_cache_hit_tokens"`
					CacheMissTokens int `json:"prompt_cache_miss_tokens"`
				} `json:"usage"`
			}
			json.Unmarshal(evt.Data, &d)
			hitTokens += d.Usage.CacheHitTokens
			missTokens += d.Usage.CacheMissTokens
		}
	}
	return buf.String(), hitTokens, missTokens, err
}

func extractText(resp *llm.ChatResponse) string {
	var b strings.Builder
	for _, m := range resp.Messages {
		for _, c := range m.Content {
			if c.Type == "text" {
				b.WriteString(c.Text)
			}
		}
	}
	return b.String()
}

func summarizeMessages(client llm.LLMClient, msgs []llm.Message, model string) string {
	raw, _ := json.Marshal(msgs)
	text := string(raw)
	if len(text) > 30_000 {
		text = text[:30_000]
	}

	req := &llm.ChatRequest{
		Model:    model,
		System:   "You are a summarizer. Be concise.",
		Messages: []llm.Message{llm.NewUserMessage("Summarize this conversation: 1) What was done 2) Current state 3) Key decisions\n\n" + text)},
		MaxTokens: 1000,
	}
	text, _, _, err := streamChat(context.Background(), client, req)
	if err != nil {
		return "summary unavailable"
	}
	return text
}

func deepCopy(src []llm.Message) []llm.Message {
	data, _ := json.Marshal(src)
	var dst []llm.Message
	json.Unmarshal(data, &dst)
	return dst
}

// bypassPermMgr always allows tool execution.
type bypassPermMgr struct{}

func (b *bypassPermMgr) Check(_ context.Context, _ string, _ json.RawMessage) error { return nil }
func (b *bypassPermMgr) Prompt(_ context.Context, _ string, _ string) (bool, error) { return true, nil }
