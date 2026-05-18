package config

// DefaultConfig returns a Config with safe default values.
func DefaultConfig() *Config {
	return &Config{
		APIProvider:    ProviderAnthropic,
		Model:          "claude-sonnet-4-20250514",
		MaxTurns:       25,
		MaxTokens:      8192,
		Temperature:    0,
		PermissionMode: PermModeNormal,
		SessionDir:     xdgDataDir() + "/sessions",
		Theme:          "dark",
		ConfigDir:      xdgConfigDir(),
		DataDir:        xdgDataDir(),
	}
}
