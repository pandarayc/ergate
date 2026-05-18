package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/raydraw/ergate/internal/llm"
)

// Provider represents an LLM API provider.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderDeepSeek  Provider = "deepseek"
)

// PermissionMode controls how tool permissions are handled.
type PermissionMode string

const (
	PermModeAlways PermissionMode = "always"
	PermModeNormal PermissionMode = "normal"
	PermModeBypass PermissionMode = "bypass"
)

// ProviderConfig holds per-provider API settings.
type ProviderConfig struct {
	APIKey          string `mapstructure:"api_key"`
	BaseURL         string `mapstructure:"base_url"`
	Model           string `mapstructure:"model"`
	ReasoningEffort string `mapstructure:"reasoning_effort"` // DeepSeek R1: "max" or ""
	ThinkingBudget  int    `mapstructure:"thinking_budget"`  // Claude extended thinking tokens
}

// Config holds all application configuration.
type Config struct {
	// Active provider name
	APIProvider Provider `mapstructure:"api_provider"`

	// Per-provider configurations (takes priority over flat fields)
	Providers map[string]ProviderConfig `mapstructure:"providers"`

	// Flat provider fields — kept for backward compatibility and single-provider ergonomics
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`

	// Engine settings
	MaxTurns    int     `mapstructure:"max_turns"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	Temperature float64 `mapstructure:"temperature"`

	// Permissions
	PermissionMode PermissionMode `mapstructure:"permission_mode"`

	// Filesystem
	AllowedPaths    []string `mapstructure:"allowed_paths"`
	BlockedCommands []string `mapstructure:"blocked_commands"`

	// Session
	SessionDir string `mapstructure:"session_dir"`

	// UI
	Headless bool   `mapstructure:"headless"`
	Theme    string `mapstructure:"theme"`

	// Feature flags
	EnableMCP bool `mapstructure:"enable_mcp"`

	// Internal paths
	ConfigDir string `mapstructure:"-"`
	DataDir   string `mapstructure:"-"`
}

// ActiveProviderConfig resolves the effective configuration for the active provider.
// Providers map takes priority over flat fields.
func (c *Config) ActiveProviderConfig() ProviderConfig {
	if c.Providers != nil {
		if pc, ok := c.Providers[string(c.APIProvider)]; ok {
			return pc
		}
	}
	return ProviderConfig{
		APIKey:  c.APIKey,
		BaseURL: c.BaseURL,
		Model:   c.Model,
	}
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if c.APIKey == "" {
		return errors.New("api_key is required (set ERGATE_API_KEY env var or api_key in config)")
	}
	if c.Model == "" {
		return errors.New("model is required")
	}
	if c.MaxTurns <= 0 {
		return errors.New("max_turns must be positive")
	}
	if c.MaxTokens <= 0 {
		return errors.New("max_tokens must be positive")
	}
	if c.Temperature < 0 || c.Temperature > 2 {
		return errors.New("temperature must be between 0 and 2")
	}
	if !llm.IsRegistered(string(c.APIProvider)) {
		return fmt.Errorf("unsupported api_provider: %q", c.APIProvider)
	}
	switch c.PermissionMode {
	case PermModeAlways, PermModeNormal, PermModeBypass:
		// valid
	default:
		return fmt.Errorf("unsupported permission_mode: %q", c.PermissionMode)
	}
	return nil
}

// EnsureDirs creates necessary directories.
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.SessionDir, 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	return nil
}

// xdgConfigDir returns the XDG-compliant config directory.
func xdgConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "ergate")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ergate")
}

// xdgDataDir returns the XDG-compliant data directory.
func xdgDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "ergate")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "ergate")
}
