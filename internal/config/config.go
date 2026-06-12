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

	// Model metadata (optional, for accurate metrics display)
	ContextWindow    int     `mapstructure:"context_window"`     // max context tokens (e.g. 128000, 1000000)
	CostPer1MIn      float64 `mapstructure:"cost_per_1m_in"`     // input cost per 1M tokens
	CostPer1MOut     float64 `mapstructure:"cost_per_1m_out"`    // output cost per 1M tokens
	CostPer1MInCached  float64 `mapstructure:"cost_per_1m_in_cached"`  // cached input cost per 1M tokens
	CostPer1MOutCached float64 `mapstructure:"cost_per_1m_out_cached"` // cached output cost per 1M tokens
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

	// Compaction
	CompactThreshold  float64 `mapstructure:"compact_threshold"`   // fraction of context window, default 0.8
	CompactKeepTail   int     `mapstructure:"compact_keep_tail"`    // messages to preserve at end, default 3
	CompactPruneBytes int     `mapstructure:"compact_prune_bytes"`  // tool result > this will be archived, default 4096

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

	// Monitoring
	CacheStatsFile string `mapstructure:"cache_stats_file"` // e.g. "ergate_cache.log" — per-turn cache metrics

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
// If the provider name is registered directly (e.g. "deepseek", "openai", "anthropic"),
// it returns that name. Otherwise it falls back to the compat field for protocol-override
// scenarios (e.g. a proxy using the openai protocol).
func (c *Config) CompatProvider() string {
	name := string(c.APIProvider)
	if llm.IsRegistered(name) {
		return name
	}
	if pc := c.ActiveProviderConfig(); pc.Compat != "" {
		return pc.Compat
	}
	return name
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
