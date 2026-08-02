package config

import (
	"os"
	"path/filepath"
	"testing"
)

// ─── CF-001: Default settings have correct values ─────────────

func TestSettings_Defaults(t *testing.T) {
	m := NewSettingsManager()
	s := m.Get()

	if s.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", s.Provider, "openai")
	}
	if s.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", s.Model, "gpt-4o")
	}
	if s.MaxTurns != 20 {
		t.Errorf("MaxTurns = %d, want 20", s.MaxTurns)
	}
	if s.CompactThreshold != 0 {
		t.Errorf("CompactThreshold = %d, want 0", s.CompactThreshold)
	}
	if s.Workspace != "" {
		t.Errorf("Workspace = %q, want empty", s.Workspace)
	}
	if s.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", s.APIKey)
	}
}

// ─── CF-002: LoadProject merges from .go-agent/settings.json ──

func TestSettings_LoadProject(t *testing.T) {
	// 创建临时项目目录
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, ".go-agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// 写入项目配置文件
	configJSON := `{
		"provider": "anthropic",
		"model": "claude-3-opus",
		"max_turns": 30,
		"compact_threshold": 5000
	}`
	configPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewSettingsManager()
	if err := m.LoadProject(tmpDir); err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	s := m.Get()
	if s.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", s.Provider, "anthropic")
	}
	if s.Model != "claude-3-opus" {
		t.Errorf("Model = %q, want %q", s.Model, "claude-3-opus")
	}
	if s.MaxTurns != 30 {
		t.Errorf("MaxTurns = %d, want 30", s.MaxTurns)
	}
	if s.CompactThreshold != 5000 {
		t.Errorf("CompactThreshold = %d, want 5000", s.CompactThreshold)
	}
}

// ─── CF-003: LoadEnv reads from environment variables ─────────

func TestSettings_LoadEnv(t *testing.T) {
	// 设置环境变量
	t.Setenv("GO_AGENT_PROVIDER", "gemini")
	t.Setenv("GO_AGENT_MODEL", "gemini-pro")
	t.Setenv("GO_AGENT_MAX_TURNS", "50")
	t.Setenv("GO_AGENT_WORKSPACE", "/tmp/workspace")
	t.Setenv("GO_AGENT_COMPACT_THRESHOLD", "8000")
	t.Setenv("GO_AGENT_API_KEY", "test-api-key")

	m := NewSettingsManager()
	m.LoadEnv()

	s := m.Get()
	if s.Provider != "gemini" {
		t.Errorf("Provider = %q, want %q", s.Provider, "gemini")
	}
	if s.Model != "gemini-pro" {
		t.Errorf("Model = %q, want %q", s.Model, "gemini-pro")
	}
	if s.MaxTurns != 50 {
		t.Errorf("MaxTurns = %d, want 50", s.MaxTurns)
	}
	if s.Workspace != "/tmp/workspace" {
		t.Errorf("Workspace = %q, want %q", s.Workspace, "/tmp/workspace")
	}
	if s.CompactThreshold != 8000 {
		t.Errorf("CompactThreshold = %d, want 8000", s.CompactThreshold)
	}
	if s.APIKey != "test-api-key" {
		t.Errorf("APIKey = %q, want %q", s.APIKey, "test-api-key")
	}
}

// ─── CF-004: Merge priority: project > global > defaults ───────

func TestSettings_MergePriority(t *testing.T) {
	// 创建全局配置目录
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	globalDir := filepath.Join(homeDir, ".go-agent")
	if err := os.MkdirAll(globalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	globalJSON := `{
		"provider": "global-provider",
		"model": "global-model",
		"max_turns": 10
	}`
	if err := os.WriteFile(filepath.Join(globalDir, "settings.json"), []byte(globalJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// 创建项目配置目录
	projectDir := t.TempDir()
	projectAgentDir := filepath.Join(projectDir, ".go-agent")
	if err := os.MkdirAll(projectAgentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	projectJSON := `{
		"model": "project-model",
		"max_turns": 15
	}`
	if err := os.WriteFile(filepath.Join(projectAgentDir, "settings.json"), []byte(projectJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewSettingsManager()

	// 加载全局配置
	if err := m.LoadGlobal(); err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	// 加载项目配置（覆盖全局）
	if err := m.LoadProject(projectDir); err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	s := m.Get()

	// provider 来自全局（项目未设置）
	if s.Provider != "global-provider" {
		t.Errorf("Provider = %q, want %q (from global)", s.Provider, "global-provider")
	}
	// model 被项目覆盖
	if s.Model != "project-model" {
		t.Errorf("Model = %q, want %q (from project)", s.Model, "project-model")
	}
	// max_turns 被项目覆盖
	if s.MaxTurns != 15 {
		t.Errorf("MaxTurns = %d, want 15 (from project)", s.MaxTurns)
	}
}

// ─── CF-005: CLI flags override everything (via Set) ──────────

func TestSettings_CLIOverride(t *testing.T) {
	// 先设置一些环境变量作为基础
	t.Setenv("GO_AGENT_PROVIDER", "env-provider")
	t.Setenv("GO_AGENT_MODEL", "env-model")

	m := NewSettingsManager()
	m.LoadEnv()

	// CLI 参数覆盖（通过 Set）
	m.Set(Settings{
		Provider: "cli-provider",
		Model: "cli-model",
		MaxTurns: 100,
	})

	s := m.Get()
	if s.Provider != "cli-provider" {
		t.Errorf("Provider = %q, want %q (from CLI)", s.Provider, "cli-provider")
	}
	if s.Model != "cli-model" {
		t.Errorf("Model = %q, want %q (from CLI)", s.Model, "cli-model")
	}
	if s.MaxTurns != 100 {
		t.Errorf("MaxTurns = %d, want 100 (from CLI)", s.MaxTurns)
	}
}

// ─── Extra map merge ──────────────────────────────────────────

func TestSettings_ExtraMerge(t *testing.T) {
	m := NewSettingsManager()

	m.Merge(Settings{
		Extra: map[string]any{
			"key1": "value1",
			"key2": 42,
		},
	})

	// 再合并，不覆盖已有 key
	m.Merge(Settings{
		Extra: map[string]any{
			"key2": 999, // 不会覆盖（mergeSettings 中 Extra 做的是添加，会覆盖同名 key）
			"key3": "value3",
		},
	})

	s := m.Get()
	if s.Extra["key1"] != "value1" {
		t.Errorf("Extra[key1] = %v, want %q", s.Extra["key1"], "value1")
	}
	if s.Extra["key2"] != 999 {
		t.Errorf("Extra[key2] = %v, want 999", s.Extra["key2"])
	}
	if s.Extra["key3"] != "value3" {
		t.Errorf("Extra[key3] = %v, want %q", s.Extra["key3"], "value3")
	}
}

// ─── LoadProject nonexistent file ─────────────────────────────

func TestSettings_LoadProjectNonexistentFile(t *testing.T) {
	m := NewSettingsManager()
	// 不存在的项目目录，不应报错
	if err := m.LoadProject("/nonexistent/path"); err != nil {
		t.Errorf("LoadProject nonexistent: expected nil error, got %v", err)
	}

	// 默认值应保持不变
	s := m.Get()
	if s.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", s.Provider, "openai")
	}
}

// ─── Get returns a copy (immutability) ────────────────────────

func TestSettings_GetReturnsCopy(t *testing.T) {
	m := NewSettingsManager()
	m.Merge(Settings{
		Extra: map[string]any{"key": "value"},
	})

	s1 := m.Get()
	s1.Extra["key"] = "modified"

	s2 := m.Get()
	if s2.Extra["key"] != "value" {
		t.Errorf("Extra[key] = %v, want %q (Get should return a copy)", s2.Extra["key"], "value")
	}
}

// ─── CF-006: $ENV_VAR interpolation in config files ───────────

func TestSettings_EnvInterpolation(t *testing.T) {
	// 设置环境变量
	t.Setenv("TEST_ENV_VAR", "secret-key-12345")

	// 创建临时项目目录
	tmpDir := t.TempDir()
	agentDir := filepath.Join(tmpDir, ".go-agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// 写入包含 $TEST_ENV_VAR 的项目配置文件
	configJSON := `{
		"provider": "anthropic",
		"model": "claude-3-opus",
		"api_key": "$TEST_ENV_VAR"
	}`
	configPath := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m := NewSettingsManager()
	if err := m.LoadProject(tmpDir); err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	s := m.Get()
	if s.APIKey != "secret-key-12345" {
		t.Errorf("APIKey = %q, want %q (interpolated from $TEST_ENV_VAR)", s.APIKey, "secret-key-12345")
	}
	if s.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", s.Provider, "anthropic")
	}
}

// ─── mergeSettings function tests ─────────────────────────────

func TestSettings_MergeSettingsFunction(t *testing.T) {
	dst := Settings{
		Provider: "openai",
		Model: "gpt-4o",
		MaxTurns: 20,
	}

	src := Settings{
		Provider: "anthropic",
		// Model 未设置，应保留 dst 的值
		MaxTurns: 30,
	}

	result := mergeSettings(dst, src)

	if result.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", result.Provider, "anthropic")
	}
	if result.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q (preserved from dst)", result.Model, "gpt-4o")
	}
	if result.MaxTurns != 30 {
		t.Errorf("MaxTurns = %d, want 30", result.MaxTurns)
	}
}
