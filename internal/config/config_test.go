package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadExample 验证 Viper 能读取 config 目录下的示例配置。
func TestLoadExample(t *testing.T) {
	cfg, err := Load("../../config/config.example.yaml")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.LLM.Enabled {
		t.Fatal("LLM.Enabled = false, want true")
	}
	if cfg.LLM.Provider != "openai-compatible" {
		t.Fatalf("LLM.Provider = %q, want %q", cfg.LLM.Provider, "openai-compatible")
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("LLM.Model = %q, want %q", cfg.LLM.Model, "gpt-4o-mini")
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("LLM.BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}
	if cfg.LLM.ContextWindow != 128000 || cfg.LLM.MaxTokens != 16384 {
		t.Fatalf("LLM limits = (%d, %d)", cfg.LLM.ContextWindow, cfg.LLM.MaxTokens)
	}
	if cfg.LLM.Temperature == nil || *cfg.LLM.Temperature != 0.2 {
		t.Fatalf("LLM.Temperature = %#v, want 0.2", cfg.LLM.Temperature)
	}
}

// TestLoadRuntimeEnvPath 验证运行时配置优先读取 OOPS_CONFIG。
func TestLoadRuntimeEnvPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte(`llm:
  model: "custom-model"
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv(EnvPath, path)

	cfg, err := LoadRuntime()
	if err != nil {
		t.Fatalf("LoadRuntime() error = %v", err)
	}
	if cfg.LLM.Model != "custom-model" {
		t.Fatalf("LLM.Model = %q, want %q", cfg.LLM.Model, "custom-model")
	}
}
