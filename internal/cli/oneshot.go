package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/raydraw/ergate/internal/engine"
)

// RunOneShot executes a single prompt with multi-turn execution and prints results.
// On SIGTERM (e.g. container timeout), it saves the session before exiting so
// the transcript is available for post-mortem analysis.
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
				os.Stdout.Sync() // flush for pipe-based capture (Harbor exec)
			}
		case engine.EventToolUse:
			if data, ok := event.Data.(map[string]any); ok {
				name, _ := data["name"].(string)
				fmt.Fprintf(os.Stderr, "[Tool: %s]\n", name)
			}
		case engine.EventToolResult:
			if data, ok := event.Data.(map[string]any); ok {
				isErr, _ := data["is_error"].(bool)
				if isErr {
					content, _ := data["content"].(string)
					fmt.Fprintf(os.Stderr, "[Error: %s]\n", content)
				}
			}
		case engine.EventError:
			if err, ok := event.Data.(error); ok {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		case engine.EventDone:
			fmt.Fprintln(os.Stderr)
			return nil
		}
	}
	return nil
}
