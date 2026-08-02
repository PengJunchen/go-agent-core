package session

import (
	"context"
	"testing"

	"github.com/pengjunchen/go-agent-core/config"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// mockSettingsProvider is a mock ModelProvider for settings-driven builder tests.
type mockSettingsProvider struct {
	info *provider.ModelInfo
}

func (m *mockSettingsProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *mockSettingsProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{Role: message.RoleAssistant}, nil
}

func (m *mockSettingsProvider) ModelInfo() *provider.ModelInfo {
	return m.info
}

// BF-001: NewBuilderFromSettings auto-selects Provider when config specifies model.
func TestNewBuilderFromSettings_AutoProvider(t *testing.T) {
	testProvider := &mockSettingsProvider{
		info: &provider.ModelInfo{Provider: "test-settings", ModelName: "test-model"},
	}
	registry.DefaultRegistry.RegisterProvider("test-settings", func(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
		return testProvider, nil
	})

	settings := config.Settings{
		Provider: "test-settings",
		Model: "test-model",
	}

	b := NewBuilderFromSettings(settings, "")

	if b.err != nil {
		t.Fatalf("unexpected error: %v", b.err)
	}
	if b.provider == nil {
		t.Fatal("Provider should be auto-selected from settings")
	}
	info := b.provider.ModelInfo()
	if info.Provider != "test-settings" {
		t.Errorf("Provider = %q, want %q", info.Provider, "test-settings")
	}
}

// BF-002: NewBuilderFromSettings auto-loads MCP servers when config specifies them.
func TestNewBuilderFromSettings_AutoMCP(t *testing.T) {
	settings := config.Settings{
		Extra: map[string]any{
			"mcp_servers": []any{
				map[string]any{
					"name": "settings-stdio",
					"type": "stdio",
					"command": "echo",
				},
			},
		},
	}

	b := NewBuilderFromSettings(settings, "")

	if b.err != nil {
		t.Fatalf("unexpected error: %v", b.err)
	}
	if b.mcpServers == nil {
		t.Fatal("MCPServers should be auto-loaded from settings")
	}

	names := b.mcpServers.Names()
	found := false
	for _, n := range names {
		if n == "settings-stdio" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MCP server %q, got %v", "settings-stdio", names)
	}

	_ = b.mcpServers.Close()
}

// BF-003: NewBuilderFromSettings auto-registers tools when config specifies subset.
func TestNewBuilderFromSettings_AutoTools(t *testing.T) {
	settings := config.Settings{
		Workspace: t.TempDir(),
		Extra: map[string]any{
			"tools": []any{"read_file", "ls"},
		},
	}

	b := NewBuilderFromSettings(settings, "")

	if b.err != nil {
		t.Fatalf("unexpected error: %v", b.err)
	}
	if b.toolRegistry == nil {
		t.Fatal("ToolRegistry should be auto-registered from settings")
	}

	tools, err := b.toolRegistry.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
}

// BF-004: NewBuilderFromSettings backward compatible — only MaxTurns/CompactThreshold.
func TestNewBuilderFromSettings_BackwardCompat(t *testing.T) {
	settings := config.Settings{
		MaxTurns: 42,
		CompactThreshold: 5000,
	}

	b := NewBuilderFromSettings(settings, "")

	if b.err != nil {
		t.Fatalf("unexpected error: %v", b.err)
	}
	if b.provider != nil {
		t.Error("Provider should be nil for backward-compat settings")
	}
	if b.toolRegistry != nil {
		t.Error("ToolRegistry should be nil for backward-compat settings")
	}
	if b.mcpServers != nil {
		t.Error("MCPServers should be nil for backward-compat settings")
	}
	if b.maxTurns != 42 {
		t.Errorf("maxTurns = %d, want 42", b.maxTurns)
	}
	if b.compactThreshold != 5000 {
		t.Errorf("compactThreshold = %d, want 5000", b.compactThreshold)
	}
}

// BF-005: NewBuilderFromSettings with unknown provider stores error in builder.
func TestNewBuilderFromSettings_UnknownProvider(t *testing.T) {
	settings := config.Settings{
		Provider: "nonexistent-provider-xyz",
		Model: "some-model",
	}

	b := NewBuilderFromSettings(settings, "")

	if b.err == nil {
		t.Fatal("expected error for unknown provider")
	}

	// Build should also fail with the stored error.
	_, err := b.Build()
	if err == nil {
		t.Error("Build should fail with stored error")
	}
}

// BF-006: Full config-driven build creates a working Session.
func TestNewBuilderFromSettings_FullBuild(t *testing.T) {
	testProvider := &mockSettingsProvider{
		info: &provider.ModelInfo{Provider: "test-full", ModelName: "full-model"},
	}
	registry.DefaultRegistry.RegisterProvider("test-full", func(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
		return testProvider, nil
	})

	settings := config.Settings{
		Provider: "test-full",
		Model: "full-model",
		MaxTurns: 15,
		CompactThreshold: 3000,
		Workspace: t.TempDir(),
		Extra: map[string]any{
			"tools": []any{"read_file", "glob"},
		},
	}

	b := NewBuilderFromSettings(settings, "")
	if b.err != nil {
		t.Fatalf("unexpected error: %v", b.err)
	}

	// Still need to set ContextManager (required).
	sess, err := b.WithContextManager(NewDefaultContextManager()).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	if sess.maxTurns != 15 {
		t.Errorf("maxTurns = %d, want 15", sess.maxTurns)
	}
	if sess.compactThreshold != 3000 {
		t.Errorf("compactThreshold = %d, want 3000", sess.compactThreshold)
	}
	if sess.Provider() == nil {
		t.Error("Provider should not be nil")
	}
	if sess.ToolRegistry() == nil {
		t.Error("ToolRegistry should not be nil")
	}
}
