package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	appconfig "github.com/raydraw/ergate/internal/config"
)

var (
	configPath string
	headless   bool
	modelName  string
	prompt     string
	resume     bool
	version    = "0.1.0"
)

// RootCmd returns the root cobra command.
func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ergate",
		Short: "Ergate — AI-powered software engineering CLI",
		Long: `Ergate is an AI-powered CLI tool for software engineering tasks.
It can read and write files, execute shell commands, search code, and more.

Default mode is interactive TUI. Use -p for one-shot queries, --headless for raw REPL.`,
		Version: version,
		RunE:    runApp,
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to config file")
	cmd.Flags().BoolVar(&headless, "headless", false, "Run in headless mode (raw REPL, no TUI)")
	cmd.Flags().StringVarP(&modelName, "model", "m", "", "Override the model name")
	cmd.Flags().StringVarP(&prompt, "prompt", "p", "", "One-shot query (non-interactive)")
	cmd.Flags().BoolVarP(&resume, "resume", "r", false, "Resume the latest session")
	cmd.Flags().BoolP("version", "v", false, "Show version")

	cmd.AddCommand(BenchCmd())

	return cmd
}

// runApp is the default command handler.
func runApp(cmd *cobra.Command, args []string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if modelName != "" {
		cfg.Model = modelName
	}

	// One-shot mode
	if prompt != "" {
		return runOneShot(cfg, prompt)
	}

	// Auto-detect: headless if stdin not a terminal
	if headless || cfg.Headless || !isTerminal() {
		return runHeadless(cfg)
	}

	return runTUI(cfg)
}

// runHeadless starts the raw terminal REPL.
func runHeadless(cfg *appconfig.Config) error {
	fmt.Println("Ergate v" + version + " — AI-powered software engineering CLI")
	fmt.Println("Type /help for available commands, /exit to quit.")
	fmt.Println()
	return StartREPL(cfg)
}

// runTUI starts the bubbletea TUI.
func runTUI(cfg *appconfig.Config) error {
	client, registry, skillReg, todoMgr, err := SetupEngine(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	eng := CreateEngine(cfg, client, registry, skillReg, todoMgr)
	return StartTUI(cfg, eng, resume)
}

// runOneShot executes a single prompt and prints the result.
func runOneShot(cfg *appconfig.Config, prompt string) error {
	client, registry, skillReg, todoMgr, err := SetupEngine(cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	eng := CreateEngine(cfg, client, registry, skillReg, todoMgr)
	return RunOneShot(eng, prompt)
}

// isTerminal checks if stdin is a terminal.
func isTerminal() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}
