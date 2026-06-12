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
	"path/filepath"
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

// turnRecord holds a single conversation turn.
type turnRecord struct {
	userMsg      llm.Message
	assistantMsg llm.Message
}

// splitMsg holds a split of an assistant response into thinking and output.
type splitMsg struct {
	Thinking string
	Output   string
}

// TestThinkingStripQuality runs a 3-turn task-completion conversation, then
// compares two turn-4 contexts: one with thinking preserved, one with thinking
// stripped + structured summary. Measures whether stripping thinking degrades
// the LLM's ability to recall facts from earlier turns.
func TestThinkingStripQuality(t *testing.T) {
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
	sysPrompt := `You are Ergate, a helpful AI assistant with access to software engineering tools.

## Environment
- Working directory: /data/projects/personal/ergate
- Date: 2026-06-12
- Platform: linux

## Instructions
- Be concise and direct.
- When reading files, report the key facts clearly.
- After completing a task, briefly summarize what you found.`

	// ── Phase 1: 3-turn task-completion conversation ─────────────────
	// Each turn accomplishes a specific goal. We'll use streamChat to collect
	// both text and cache metrics, and manually build the message history.

	var history []turnRecord

	ctx := context.Background()

	// Turn 1: Read and analyze go.mod
	{
		req := &llm.ChatRequest{
			Model: model, System: sysPrompt, MaxTokens: 8192,
			Messages: []llm.Message{
				llm.NewUserMessage("Read the file go.mod. Tell me: 1) Go version 2) Module path 3) All direct dependencies with their versions."),
			},
		}
		text, _, _, err := streamChat(ctx, client, req)
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, turnRecord{
			userMsg:      req.Messages[0],
			assistantMsg: llm.Message{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: text}}},
		})
		t.Logf("turn 1: %d chars response", len(text))
		time.Sleep(2 * time.Second)
	}

	// Turn 2: Analyze internal/llm/client.go
	{
		var msgs []llm.Message
		for _, h := range history {
			msgs = append(msgs, h.userMsg, h.assistantMsg)
		}
		msgs = append(msgs, llm.NewUserMessage(
			"Read internal/llm/client.go. List every type, interface, and constant defined in this file."))

		req := &llm.ChatRequest{
			Model: model, System: sysPrompt, MaxTokens: 8192, Messages: msgs,
		}
		text, _, _, err := streamChat(ctx, client, req)
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, turnRecord{
			userMsg:      msgs[len(msgs)-1],
			assistantMsg: llm.Message{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: text}}},
		})
		t.Logf("turn 2: %d chars response", len(text))
		time.Sleep(2 * time.Second)
	}

	// Turn 3: Analyze internal/compact/compact.go
	{
		var msgs []llm.Message
		for _, h := range history {
			msgs = append(msgs, h.userMsg, h.assistantMsg)
		}
		msgs = append(msgs, llm.NewUserMessage(
			"Read internal/compact/compact.go. Explain: 1) What SnipCompact does 2) What AutoCompact does 3) What constants are defined."))

		req := &llm.ChatRequest{
			Model: model, System: sysPrompt, MaxTokens: 8192, Messages: msgs,
		}
		text, _, _, err := streamChat(ctx, client, req)
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, turnRecord{
			userMsg:      msgs[len(msgs)-1],
			assistantMsg: llm.Message{Type: llm.MsgAssistant, Role: "assistant", Content: []llm.ContentBlock{{Type: "text", Text: text}}},
		})
		t.Logf("turn 3: %d chars response", len(text))
		time.Sleep(2 * time.Second)
	}

	// ── Phase 2: Build two turn-4 contexts ───────────────────────────

	// Simulate thinking: for each assistant message, split out a "thinking" portion.
	// In real R1, this would be reasoning_content. Here we use a heuristic:
	// first 30% of the response is "thinking" (analysis), rest is "output".
	var splitHistory []splitMsg
	for _, h := range history {
		text := extractTextFromMsg(h.assistantMsg)
		cut := len(text) / 3 // rough split: first 1/3 = thinking
		if cut < 20 {
			cut = 0
		}
		splitHistory = append(splitHistory, splitMsg{
			Thinking: text[:cut],
			Output:   text[cut:],
		})
	}

	// Build base message list (turns 1-3, without thinking blocks)
	var baseMsgs []llm.Message
	for i, h := range history {
		baseMsgs = append(baseMsgs, h.userMsg)
		if splitHistory[i].Thinking != "" {
			baseMsgs = append(baseMsgs, llm.Message{
				Type: llm.MsgAssistant, Role: "assistant",
				Content: []llm.ContentBlock{
					{Type: "thinking", Thinking: splitHistory[i].Thinking},
					{Type: "text", Text: splitHistory[i].Output},
				},
			})
		} else {
			baseMsgs = append(baseMsgs, h.assistantMsg)
		}
	}

	// Generate structured summary of turns 1-3 (ReCAP-style)
	summary := generateStructuredSummary(t, client, model, history, splitHistory)
	t.Logf("structured summary: %d chars", len(summary))

	// Variant A: Full context with thinking preserved
	followUp := "Based on everything you analyzed in the previous turns, what is the single most important architectural decision in this project? Answer in 2-3 sentences."
	var reqA = &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 2048,
		Messages: append(deepCopy(baseMsgs), llm.NewUserMessage(followUp)),
	}

	// Variant B: Compaction with thinking stripped, structured summary only
	foldedMsgs := []llm.Message{
		{Type: llm.MsgSystem, Role: "system", Subtype: "compact_boundary",
			Content: []llm.ContentBlock{{Type: "text", Text: summary}}},
		llm.NewUserMessage("[Context from previous turns]\n\n" + summary),
		{Type: llm.MsgAssistant, Role: "assistant",
			Content: []llm.ContentBlock{{Type: "text", Text: "Understood. I have the context from the previous analysis."}}},
		llm.NewUserMessage(followUp),
	}
	var reqB = &llm.ChatRequest{
		Model: model, System: sysPrompt, MaxTokens: 2048, Messages: foldedMsgs,
	}

	// ── Phase 3: Compare ─────────────────────────────────────────────
	fmt.Println()
	fmt.Println("=============================================================")
	fmt.Println("  Thinking Strip Quality Test")
	fmt.Println("  Compare: Full history (with thinking) vs Structured summary")
	fmt.Println("=============================================================")

	// Variant A first
	textA, _, _, err := streamChat(ctx, client, reqA)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)

	// Variant B second
	textB, _, _, err := streamChat(ctx, client, reqB)
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println()
	fmt.Println("─── Variant A: Full history (thinking preserved) ───")
	fmt.Printf("  Context: %d messages, ~%d chars\n", len(reqA.Messages), estimateCharLen(reqA.Messages))
	fmt.Println("  Response:")
	for _, line := range wrapLines(textA, 70) {
		fmt.Printf("    %s\n", line)
	}

	fmt.Println()
	fmt.Println("─── Variant B: Structured summary (thinking stripped) ───")
	fmt.Printf("  Context: %d messages, ~%d chars\n", len(reqB.Messages), estimateCharLen(reqB.Messages))
	fmt.Println("  Response:")
	for _, line := range wrapLines(textB, 70) {
		fmt.Printf("    %s\n", line)
	}

	// ── Phase 4: Report ──────────────────────────────────────────────
	fmt.Println()
	fmt.Println("─── Comparison ───")
	fmt.Printf("  Variant A: %d chars response (%d messages context)\n", len(textA), len(reqA.Messages))
	fmt.Printf("  Variant B: %d chars response (%d messages context)\n", len(textB), len(reqB.Messages))
	fmt.Printf("  Context reduction: %.0f%% less messages\n",
		float64(len(reqA.Messages)-len(reqB.Messages))/float64(len(reqA.Messages))*100)
	fmt.Println("=============================================================")
}

func generateStructuredSummary(t *testing.T, client llm.LLMClient, model string,
	history []turnRecord, split []splitMsg) string {
	t.Helper()

	// Build a prompt that asks for structured summary
	var facts strings.Builder
	for i, h := range history {
		fmt.Fprintf(&facts, "Turn %d: User asked: %s\n", i+1, extractTextFromMsg(h.userMsg))
		fmt.Fprintf(&facts, "Assistant found: %s\n", split[i].Output)
	}

	req := &llm.ChatRequest{
		Model:    model,
		System:   "You are a context summarizer. Output ONLY a structured summary. No preamble.",
		Messages: []llm.Message{llm.NewUserMessage(fmt.Sprintf(
			`Summarize these conversation turns into a structured context block:

%s

Format your response EXACTLY like this:
[已完成]
- Task 1: <what was done, key findings>
- Task 2: <what was done, key findings>

[关键发现]
- Finding 1
- Finding 2

[当前状态]
- What the user is trying to do overall`, facts.String()))},
		MaxTokens: 800,
	}
	text, _, _, err := streamChat(context.Background(), client, req)
	if err != nil {
		return "summary unavailable"
	}
	return text
}

func extractTextFromMsg(m llm.Message) string {
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func estimateCharLen(msgs []llm.Message) int {
	raw, _ := json.Marshal(msgs)
	return len(raw)
}

func wrapLines(s string, width int) []string {
	var lines []string
	for len(s) > width {
		lines = append(lines, s[:width])
		s = s[width:]
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	return lines
}

// TestFoldCompactVsAutoCompact runs two 5-turn conversations: one with
// FoldCompact (keepTail=3) and one with AutoCompact (keepTail=0), comparing
// cache metrics and response quality.
func TestFoldCompactVsAutoCompact(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run this test")
	}

	questions := []string{
		"Read go.mod and tell me: 1) Go version 2) Module path 3) All direct dependencies.",
		"Read internal/llm/client.go. List every type, interface, and constant defined.",
		"Read internal/compact/compact.go. What does AutoCompact do?",
		"Based on everything you read, what is the most important architectural decision?",
		"Summarize the entire project architecture in one paragraph.",
	}

	fmt.Println()
	fmt.Println("=============================================================")
	fmt.Println("  FoldCompact vs AutoCompact Comparison")
	fmt.Println("=============================================================")
	fmt.Println()

	// ── Run A: AutoCompact (keepTail=0) ──────────────────────────
	{
		t.Log("=== AutoCompact (keepTail=0) ===")
		eng, client := newTestEngine(t, apiKey, "deepseek-v4-flash", 0.20, 0)
		defer client.Close()

		hitA, missA := runConversation(t, eng, questions)
		pctA := 0.0
		if hitA+missA > 0 {
			pctA = float64(hitA) / float64(hitA+missA) * 100
		}
		cc := eng.CompactCount()
		fmt.Printf("  AutoCompact (keepTail=0):\n")
		fmt.Printf("    compact triggered: %d times\n", cc)
		fmt.Printf("    cache_hit:  %d  cache_miss: %d  hit_rate: %5.1f%%\n", hitA, missA, pctA)
		fmt.Println()
	}

	time.Sleep(3 * time.Second)

	// ── Run B: FoldCompact (keepTail=3) ─────────────────────────
	{
		t.Log("=== FoldCompact (keepTail=3) ===")
		eng, client := newTestEngine(t, apiKey, "deepseek-v4-flash", 0.20, 3)
		defer client.Close()

		hitB, missB := runConversation(t, eng, questions)
		pctB := 0.0
		if hitB+missB > 0 {
			pctB = float64(hitB) / float64(hitB+missB) * 100
		}
		cc := eng.CompactCount()
		fmt.Printf("  FoldCompact (keepTail=3):\n")
		fmt.Printf("    compact triggered: %d times\n", cc)
		fmt.Printf("    cache_hit:  %d  cache_miss: %d  hit_rate: %5.1f%%\n", hitB, missB, pctB)
		fmt.Println()
	}

	fmt.Println("=============================================================")
}

// newTestEngine creates an engine with forced-early compaction for testing.
func newTestEngine(t *testing.T, apiKey, model string, threshold float64, keepTail int) (*engine.Engine, llm.LLMClient) {
	t.Helper()

	cfg := &config.Config{
		APIProvider:     config.ProviderDeepSeek,
		Model:           model,
		MaxTurns:        5,
		MaxTokens:       8192,
		Temperature:     0.0,
		SessionDir:      os.TempDir(),
		PermissionMode:  config.PermModeBypass,
		CompactThreshold: threshold,
		CompactKeepTail:  keepTail,
		Providers: map[string]config.ProviderConfig{
			"deepseek": {
				APIKey: apiKey,
				Models: map[string]config.ModelOptions{
					model: {
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

	todoMgr := tool.NewTodoManager()
	toolReg := tool.NewRegistry()
	tool.RegisterBuiltins(toolReg, todoMgr)

	ectx := engine.Context{
		PermMgr: &bypassPermMgr{},
		PermCtx: tool.PermissionContext{Mode: tool.PermModeBypassPermissions},
	}
	return engine.New(cfg, client, toolReg, ectx), client
}

// runConversation runs a list of questions through the engine and returns
// total cache hit/miss token counts.
func runConversation(t *testing.T, eng *engine.Engine, questions []string) (hit, miss int) {
	ctx := context.Background()
	var lastHit, lastMiss int

	for _, q := range questions {
		events := make(chan engine.Event, 256)
		errCh := make(chan error, 1)
		go func() { errCh <- eng.Run(ctx, q, events) }()
		for range events {
		}
		if err := <-errCh; err != nil {
			t.Logf("run: %v", err)
		}

		h, m := eng.CacheUsage()
		t.Logf("  turn: hit=%d miss=%d (+%d/+%d)", h, m, h-lastHit, m-lastMiss)
		lastHit, lastMiss = h, m
		time.Sleep(500 * time.Millisecond)
	}

	return lastHit, lastMiss
}

// TestFoldCompactAnswerQuality compares response quality between AutoCompact
// and FoldCompact on a fact-recall task. 3 turns build specific knowledge,
// compaction triggers, then turn 4 asks a question that requires the tail facts.
func TestFoldCompactAnswerQuality(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run this test")
	}

	// Build knowledge: 3 turns that read and analyze, building conceptual understanding.
	knowledgeQuestions := []string{
		"Read internal/llm/client.go. What is the overall design pattern used? How does this project abstract different LLM providers?",
		"Read internal/compact/compact.go. Why does the project have multiple compaction layers (SnipCompact, MicroCompact, AutoCompact)? What problem does each solve?",
		"Read internal/config/config.go. How does the config system support multiple LLM providers? What is the Compat field for?",
	}

	// The critical test: conceptual synthesis across all 3 turns.
	// AutoCompact only has a summary → may lose the reasoning behind design choices.
	// FoldCompact has the tail → should retain the "why" behind each decision.
	recallQuestion := "Based on your earlier analysis of client.go, compact.go, and config.go: what are the 3 most important design decisions in this project, and WHY was each made? Do NOT re-read the files — answer from your analysis."

	fmt.Println()
	fmt.Println("=============================================================")
	fmt.Println("  Answer Quality Comparison: FoldCompact vs AutoCompact")
	fmt.Println("=============================================================")
	fmt.Println()

	// ── Run A: AutoCompact ──────────────────────────────────────────
	var answerA string
	{
		t.Log("=== AutoCompact ===")
		eng, client := newTestEngine(t, apiKey, "deepseek-v4-flash", 0.20, 0)
		defer client.Close()

		answerA = runWithRecall(t, eng, knowledgeQuestions, recallQuestion)
	}

	time.Sleep(3 * time.Second)

	// ── Run B: FoldCompact ─────────────────────────────────────────
	var answerB string
	{
		t.Log("=== FoldCompact ===")
		eng, client := newTestEngine(t, apiKey, "deepseek-v4-flash", 0.20, 3)
		defer client.Close()

		answerB = runWithRecall(t, eng, knowledgeQuestions, recallQuestion)
	}

	fmt.Println()
	fmt.Println("─── Recall Question ───")
	fmt.Printf("  %s\n", recallQuestion)
	fmt.Println()
	fmt.Println("─── A) AutoCompact (summary only) ───")
	for _, line := range wrapLines(answerA, 70) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	fmt.Println("─── B) FoldCompact (tail preserved) ───")
	for _, line := range wrapLines(answerB, 70) {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()
	fmt.Println("─── Comparison ───")
	fmt.Printf("  A: %d chars  |  B: %d chars\n", len(answerA), len(answerB))
	fmt.Println("=============================================================")
}

// runWithRecall runs knowledge questions, then a recall question, and returns
// the recall answer text.
func runWithRecall(t *testing.T, eng *engine.Engine, knowledge []string, recall string) string {
	ctx := context.Background()

	for i, q := range knowledge {
		events := make(chan engine.Event, 256)
		errCh := make(chan error, 1)
		go func() { errCh <- eng.Run(ctx, q, events) }()
		for range events {
		}
		if err := <-errCh; err != nil {
			t.Logf("knowledge turn %d: %v", i+1, err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Run recall question and collect the text answer.
	events := make(chan engine.Event, 256)
	errCh := make(chan error, 1)
	var answer strings.Builder
	go func() {
		errCh <- eng.Run(ctx, recall, events)
	}()
	for evt := range events {
		if evt.Type == engine.EventText {
			if text, ok := evt.Data.(string); ok {
				answer.WriteString(text)
			}
		}
	}
	if err := <-errCh; err != nil {
		t.Logf("recall: %v", err)
	}
	return answer.String()
}

// TestPruneCompact runs a conversation with large file reads and verifies
// that PruneCompact archives old tool results and frees context space.
func TestPruneCompact(t *testing.T) {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("set DEEPSEEK_API_KEY to run this test")
	}

	// Use a very low prune threshold and early compaction to trigger pruning.
	eng, client := newTestEngine(t, apiKey, "deepseek-v4-flash", 0.30, 3)
	defer client.Close()

	ctx := context.Background()
	questions := []string{
		// Read a large file to generate a big tool result.
		"Read internal/llm/provider/openai_adapter.go. Show the complete file content.",
		// Another read to build up context.
		"Read internal/engine/engine.go. What does the handleToolResult function do?",
		// Third read to trigger compaction + pruning.
		"Read internal/compact/compact.go. What layers of compaction exist?",
		// Check if previous details are still accessible post-prune.
		"Based on what you read before: what does openai_adapter.go's BuildRequestBody do?",
	}

	var lastHit, lastMiss int
	for i, q := range questions {
		events := make(chan engine.Event, 256)
		errCh := make(chan error, 1)
		go func() { errCh <- eng.Run(ctx, q, events) }()
		for range events {
		}
		if err := <-errCh; err != nil {
			t.Logf("turn %d: %v", i+1, err)
		}
		h, m := eng.CacheUsage()
		cc := eng.CompactCount()
		t.Logf("turn %d: hit=%d miss=%d (+%d/+%d) compact=%d",
			i+1, h, m, h-lastHit, m-lastMiss, cc)
		lastHit, lastMiss = h, m
		time.Sleep(500 * time.Millisecond)
	}

	// Check that prune files were created.
	entries, _ := os.ReadDir(filepath.Join(".ergate", "tool-results"))
	pruneCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "prune_") {
			pruneCount++
		}
	}

	fmt.Println()
	fmt.Println("=============================================================")
	fmt.Println("  PruneCompact Test")
	fmt.Println("=============================================================")
	fmt.Printf("  Compaction count: %d\n", eng.CompactCount())
	fmt.Printf("  Prune files created: %d\n", pruneCount)
	hit, miss := eng.CacheUsage()
	if hit+miss > 0 {
		fmt.Printf("  Overall hit rate: %5.1f%%\n", float64(hit)/float64(hit+miss)*100)
	}
	fmt.Println("=============================================================")
}

// bypassPermMgr always allows tool execution.
type bypassPermMgr struct{}

func (b *bypassPermMgr) Check(_ context.Context, _ string, _ json.RawMessage) error { return nil }
func (b *bypassPermMgr) Prompt(_ context.Context, _ string, _ string) (bool, error) { return true, nil }
