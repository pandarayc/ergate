package config

import (
	"os"
	"path/filepath"
	"testing"

	_ "github.com/raydraw/ergate/internal/llm/provider" // register providers for Validate
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.APIProvider != ProviderAnthropic {
		t.Errorf("default provider: got %q, want %q", cfg.APIProvider, ProviderAnthropic)
	}
	if cfg.MaxTurns != 25 {
		t.Errorf("default max_turns: got %d, want 25", cfg.MaxTurns)
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("default max_tokens: got %d, want 8192", cfg.MaxTokens)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{"no api key", &Config{APIKey: "", Model: "test", MaxTurns: 1, MaxTokens: 1, APIProvider: ProviderAnthropic, PermissionMode: PermModeNormal}},
		{"no model", &Config{Model: "", MaxTurns: 1, MaxTokens: 1, APIProvider: ProviderAnthropic, PermissionMode: PermModeNormal}},
		{"bad max_turns", &Config{Model: "x", MaxTurns: 0, MaxTokens: 1, APIProvider: ProviderAnthropic, PermissionMode: PermModeNormal}},
		{"bad provider", &Config{Model: "x", MaxTurns: 1, MaxTokens: 1, APIProvider: "unknown", PermissionMode: PermModeNormal}},
		{"bad permission", &Config{Model: "x", MaxTurns: 1, MaxTokens: 1, APIProvider: ProviderAnthropic, PermissionMode: "bad"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("ERGATE_API_KEY", "test-key-123")
	os.Setenv("ERGATE_MODEL", "test-model")
	defer func() {
		os.Unsetenv("ERGATE_API_KEY")
		os.Unsetenv("ERGATE_MODEL")
	}()

	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "test-key-123" {
		t.Errorf("API key: got %q, want %q", cfg.APIKey, "test-key-123")
	}
	if cfg.Model != "test-model" {
		t.Errorf("model: got %q, want %q", cfg.Model, "test-model")
	}
}

func TestLoadFromYAML(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte("model: yaml-model\nmax_turns: 10\napi_key: yaml-key\npermission_mode: always\napi_provider: openai\n")
	os.WriteFile(filepath.Join(dir, "config.yaml"), yaml, 0o644)

	cfg, err := Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "yaml-model" {
		t.Errorf("model: got %q, want %q", cfg.Model, "yaml-model")
	}
	if cfg.MaxTurns != 10 {
		t.Errorf("max_turns: got %d, want 10", cfg.MaxTurns)
	}
}

func TestEnsureDirs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SessionDir = filepath.Join(t.TempDir(), "sessions")
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.SessionDir); err != nil {
		t.Error("session dir not created")
	}
}
