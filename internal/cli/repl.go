package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/raydraw/ergate/internal/config"
	"github.com/raydraw/ergate/internal/engine"
)

// LoadConfig loads configuration using the config package.
func LoadConfig(configPath string) (*config.Config, error) {
	return config.Load(configPath)
}

// NewCommandRegistry creates a registry with all built-in commands.
func NewCommandRegistry(cfg *config.Config, eng *engine.Engine) *CommandRegistry {
	reg := &CommandRegistry{commands: make(map[string]*Command), names: make([]string, 0)}
	reg.Register(HelpCmd())
	reg.Register(ExitCmd())
	reg.Register(ClearCmd(eng))
	reg.Register(ModelCmd(cfg))
	reg.Register(UsageCmd(eng))
	reg.Register(ConfigCmd(cfg))
	reg.Register(VersionCmd())
	reg.Register(StatusCmd(cfg, eng))
	reg.Register(CostCmd(eng))
	reg.Register(ThemeCmd())
	return reg
}

// StartREPL runs the interactive read-eval-print loop.
func StartREPL(cfg *config.Config) error {
	if err := cfg.EnsureDirs(); err != nil {
		return fmt.Errorf("prepare dirs: %w", err)
	}

	// Create LLM client, tools, and skills
	client, toolRegistry, skillReg, todoMgr, err := SetupEngine(cfg)
	if err != nil {
		return fmt.Errorf("setup engine: %w", err)
	}
	defer client.Close()


	// Create engine with memory and skills
	eng := CreateEngine(cfg, client, toolRegistry, skillReg, todoMgr)

	// Create command registry
	cmdReg := NewCommandRegistry(cfg, eng)

	scanner := bufio.NewScanner(os.Stdin)

	// Handle interrupt for clean shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break // EOF
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Handle slash commands
		if strings.HasPrefix(input, "/") {
			output, quit, handled := cmdReg.HandleREPL(input, nil)
			if !handled {
				parts := strings.Fields(input)
				fmt.Printf("Unknown command: %s (type /help for available commands)\n", parts[0])
				continue
			}
			fmt.Println(output)
			if quit {
				return nil
			}
			continue
		}

		// Run the query engine
		events := make(chan engine.Event, 64)
		go func() {
			if err := eng.Run(ctx, input, events); err != nil {
				if err != context.Canceled {
					fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
				}
			}
		}()

		// Process events
		for event := range events {
			switch event.Type {
			case engine.EventText:
				fmt.Print(event.Data.(string))

			case engine.EventThinking:
				if data, ok := event.Data.(string); ok {
					fmt.Printf("\033[2m[Thinking] %s\033[0m\n", data)
				}

			case engine.EventToolUse:
				if data, ok := event.Data.(map[string]interface{}); ok {
					fmt.Printf("\n\033[36m[Tool: %s]\033[0m ", data["name"])
					if input, ok := data["input"].(string); ok && input != "" && input != "{}" {
						fmt.Printf("\033[90m%s\033[0m", input)
					}
					fmt.Println()
				}

			case engine.EventToolResult:
				if data, ok := event.Data.(map[string]interface{}); ok {
					if isErr, _ := data["is_error"].(bool); isErr {
						fmt.Printf("\n\033[31m[Tool Error: %s]\033[0m\n", data["content"])
					} else {
						content := data["content"].(string)
						if len(content) > 200 {
							content = content[:200] + "..."
						}
						fmt.Printf("\033[90m[Result: %s]\033[0m\n", content)
					}
				}

			case engine.EventError:
				fmt.Fprintf(os.Stderr, "\033[31mError: %v\033[0m\n", event.Data)

			case engine.EventTurnEnd:
				// Continue to next turn

			case engine.EventDone:
				if data, ok := event.Data.(string); ok && data != "" && data != "max_turns_reached" {
					fmt.Println()
				}

			case engine.EventAborted:
				fmt.Println("\n\033[33m[Interrupted]\033[0m")
			}
		}

		fmt.Println()
	}

	return scanner.Err()
}
