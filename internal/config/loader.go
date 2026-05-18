package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Load reads configuration from file + environment variables.
// Precedence (lowest to highest): defaults → config file → env vars.
func Load(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	v := viper.New()

	// Config file
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(cfg.ConfigDir)
		v.AddConfigPath(".")
	}

	// Map env vars
	v.SetEnvPrefix("ERGATE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Bind specific env vars to config keys
	_ = v.BindEnv("api_key")
	_ = v.BindEnv("api_provider")
	_ = v.BindEnv("base_url")
	_ = v.BindEnv("model")
	_ = v.BindEnv("max_turns")
	_ = v.BindEnv("max_tokens")
	_ = v.BindEnv("temperature")
	_ = v.BindEnv("permission_mode")
	_ = v.BindEnv("headless")

	// Read config file (ignore if not found)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Also ignore "file not found" when a specific path was given
			if os.IsNotExist(err) {
				// Config file is optional, proceed with defaults + env
			} else {
				return nil, fmt.Errorf("read config: %w", err)
			}
		}
	}

	// Unmarshal into struct
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	// Override from specific env vars
	if k := os.Getenv("ERGATE_API_KEY"); k != "" {
		cfg.APIKey = k
	}
	if k := os.Getenv("ANTHROPIC_API_KEY"); k != "" && cfg.APIKey == "" {
		cfg.APIKey = k
	}
	if k := os.Getenv("OPENAI_API_KEY"); k != "" && cfg.APIKey == "" {
		cfg.APIKey = k
	}
	if u := os.Getenv("ERGATE_BASE_URL"); u != "" {
		cfg.BaseURL = u
	}
	if m := os.Getenv("ERGATE_MODEL"); m != "" {
		cfg.Model = m
	}

	// Resolve api_key and base_url from provider config (flat fields take priority)
	pc := cfg.ActiveProviderConfig()
	if pc.APIKey != "" {
		cfg.APIKey = pc.APIKey
	}
	if pc.BaseURL != "" {
		cfg.BaseURL = pc.BaseURL
	}

	// Resolve session dir
	if cfg.SessionDir == "" || cfg.SessionDir == xdgDataDir()+"/sessions" {
		cfg.SessionDir = filepath.Join(cfg.DataDir, "sessions")
	}
	cfg.ConfigDir = cfg.getConfigDir(configPath)
	cfg.DataDir = getDataDir()

	return cfg, cfg.Validate()
}

func (c *Config) getConfigDir(configPath string) string {
	if configPath != "" {
		if abs, err := filepath.Abs(filepath.Dir(configPath)); err == nil {
			return abs
		}
	}
	return xdgConfigDir()
}

func getDataDir() string {
	return xdgDataDir()
}
