package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"os/signal"
	"syscall"

	"github.com/raydraw/ergate/internal/engine"
)

// RunOneShot executes a single prompt with multi-turn execution and prints results.
// On SIGTERM (e.g. container timeout), it saves the session before exiting so
// the transcript is available for post-mortem analysis.
// truncateToolInput extracts key params from tool input JSON for logging.
// Returns at most 200 characters.
func truncateToolInput(toolName, input string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		if len(input) > 200 {
			return input[:200] + "..."
		}
		return input
	}
	switch toolName {
	case "Bash":
		if cmd, ok := parsed["command"].(string); ok {
			if len(cmd) > 200 {
				return cmd[:200] + "..."
			}
			return cmd
		}
	case "Evaluate":
		aspect, _ := parsed["aspect"].(string)
		cmd, _ := parsed["command"].(string)
		s := cmd
		if aspect != "" {
			s = aspect + ": " + cmd
		}
		if len(s) > 200 {
			return s[:200] + "..."
		}
		return s
	case "Write", "Edit":
		if fp, ok := parsed["file_path"].(string); ok {
			return fp
		}
	case "WebSearch":
		if q, ok := parsed["query"].(string); ok {
			if len(q) > 200 {
				return q[:200] + "..."
			}
			return q
		}
	case "WebFetch":
		if u, ok := parsed["url"].(string); ok {
			return u
		}
	}
	if len(input) > 200 {
		return input[:200] + "..."
	}
	return input
}

func RunOneShot(eng *engine.Engine, prompt string) error {
	events := make(chan engine.Event, 128)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Catch SIGTERM/SIGINT: cancel the context so eng.Run returns cleanly,
	// triggering deferred session save + transcript write.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	go func() {
		if err := eng.Run(ctx, prompt, events); err != nil {
			if err != context.Canceled {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		}
	}()

	for event := range events {
		switch event.Type {
		case engine.EventText:
			if text, ok := event.Data.(string); ok {
				fmt.Print(text)
				os.Stdout.Sync()
			}
		case engine.EventThinking:
			if text, ok := event.Data.(string); ok {
				fmt.Fprintf(os.Stderr, "[Thinking] %s\n", text)
			}
		case engine.EventToolUse:
			if data, ok := event.Data.(map[string]any); ok {
				name, _ := data["name"].(string)
				input, _ := data["input"].(string)
				switch name {
				case "Bash", "Evaluate", "WebSearch", "WebFetch", "Write", "Edit":
					if input != "" {
						fmt.Fprintf(os.Stderr, "[Tool: %s] %s\n", name, truncateToolInput(name, input))
					} else {
						fmt.Fprintf(os.Stderr, "[Tool: %s]\n", name)
					}
				default:
					fmt.Fprintf(os.Stderr, "[Tool: %s]\n", name)
				}
			}
		case engine.EventToolResult:
			if data, ok := event.Data.(map[string]any); ok {
				isErr, _ := data["is_error"].(bool)
				content, _ := data["content"].(string)
				if isErr {
					fmt.Fprintf(os.Stderr, "[Tool Error: %s]\n", content)
				} else if content != "" {
					firstLine := content
					if idx := strings.IndexByte(content, '\n'); idx >= 0 {
						firstLine = content[:idx]
					}
					if len(firstLine) > 200 {
						firstLine = firstLine[:200] + "..."
					}
					fmt.Fprintf(os.Stderr, "[Tool Result] %s\n", firstLine)
				}
			}
		case engine.EventTurnEnd:
			fmt.Fprintf(os.Stderr, "[Turn %d end]\n", event.Turn)
		case engine.EventError:
			if err, ok := event.Data.(error); ok {
				fmt.Fprintf(os.Stderr, "[Error: %v]\n", err)
			}
		case engine.EventAborted:
			fmt.Fprintf(os.Stderr, "[Aborted: %v]\n", event.Data)
		case engine.EventDone:
			fmt.Fprintln(os.Stderr)
			return nil
		}
	}
	return nil
}
