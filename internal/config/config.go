package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/raydraw/ergate/internal/llm"
)

// Provider represents an LLM API provider label.
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

// CompatType declares the API protocol a provider speaks.
const (
	CompatAnthropic = "anthropic"
	CompatOpenAI    = "openai"
)

// ModelOptions holds per-model configuration.
type ModelOptions struct {
	ReasoningEffort string `mapstructure:"reasoning_effort"` // DeepSeek R1: "" | "max" | "high" | ...
	ThinkingBudget  int    `mapstructure:"thinking_budget"`  // Claude extended thinking tokens
}

// ProviderConfig holds per-provider transport and model catalog.
type ProviderConfig struct {
	Compat  string                  `mapstructure:"compat"`   // API protocol: "anthropic" | "openai"
	APIKey  string                  `mapstructure:"api_key"`
	BaseURL string                  `mapstructure:"base_url"`
	Models  map[string]ModelOptions `mapstructure:"models"`
}

// Config holds all application configuration.
type Config struct {
	// Active provider label — key into Providers map
	APIProvider Provider `mapstructure:"api_provider"`

	// Per-provider configurations
	Providers map[string]ProviderConfig `mapstructure:"providers"`

	// Flat fields — backward compatible, overridden by provider config
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

	// Sub-agent model (falls back to Model if empty)
	SubagentModel string `mapstructure:"subagent_model"`

	// Feature flags
	EnableMCP bool `mapstructure:"enable_mcp"`

	// Internal paths
	ConfigDir string `mapstructure:"-"`
	DataDir   string `mapstructure:"-"`
}

// ActiveProviderConfig resolves the provider configuration for the active provider.
func (c *Config) ActiveProviderConfig() ProviderConfig {
	if c.Providers != nil {
		if pc, ok := c.Providers[string(c.APIProvider)]; ok {
			return pc
		}
	}
	return ProviderConfig{
		APIKey:  c.APIKey,
		BaseURL: c.BaseURL,
	}
}

// ActiveModelOptions returns per-model options for the currently selected model.
func (c *Config) ActiveModelOptions() ModelOptions {
	if c.Providers != nil {
		if pc, ok := c.Providers[string(c.APIProvider)]; ok {
			if pc.Models != nil {
				if mo, ok := pc.Models[c.Model]; ok {
					return mo
				}
			}
		}
	}
	return ModelOptions{}
}

// SubagentModelName returns the model for sub-agents, falling back to the main model.
func (c *Config) SubagentModelName() string {
	if c.SubagentModel != "" {
		return c.SubagentModel
	}
	return c.Model
}

// CompatProvider returns the protocol type to use for LLM client creation.
// Falls back to api_provider if compat is not set in provider config.
func (c *Config) CompatProvider() string {
	if pc := c.ActiveProviderConfig(); pc.Compat != "" {
		return pc.Compat
	}
	return string(c.APIProvider)
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if pc := c.ActiveProviderConfig(); pc.APIKey == "" && c.APIKey == "" {
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
	compat := c.CompatProvider()
	if !llm.IsRegistered(compat) {
		return fmt.Errorf("unsupported protocol %q (check api_provider or compat in config)", compat)
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
