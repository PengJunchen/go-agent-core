package config

import (
	"context"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// mockLoaderProvider 用于 loader 测试的 mock ModelProvider。
type mockLoaderProvider struct {
	info *provider.ModelInfo
}

func newMockLoaderProvider(info *provider.ModelInfo) *mockLoaderProvider {
	return &mockLoaderProvider{info: info}
}

func (m *mockLoaderProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent)
	close(ch)
	return ch, nil
}

func (m *mockLoaderProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{Role: message.RoleAssistant}, nil
}

func (m *mockLoaderProvider) ModelInfo() *provider.ModelInfo {
	return m.info
}

// LD-001: Config with model auto-selects Provider from registry.
func TestLoadAndAssemble_AutoSelectProvider(t *testing.T) {
	// 注册一个 mock provider 到全局注册表。
	testProvider := newMockLoaderProvider(&provider.ModelInfo{
		Provider: "test-loader",
		ModelName: "test-model-v1",
	})
	registry.DefaultRegistry.RegisterProvider("test-loader", func(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
		return testProvider, nil
	})

	settings := Settings{
		Provider: "test-loader",
		Model: "test-model-v1",
	}

	ac, err := LoadAndAssemble(settings)
	if err != nil {
		t.Fatalf("LoadAndAssemble: %v", err)
	}

	if ac.Provider == nil {
		t.Fatal("Provider should not be nil when provider is configured")
	}
	info := ac.Provider.ModelInfo()
	if info.Provider != "test-loader" {
		t.Errorf("Provider.ModelInfo().Provider = %q, want %q", info.Provider, "test-loader")
	}
	if info.ModelName != "test-model-v1" {
		t.Errorf("Provider.ModelInfo().ModelName = %q, want %q", info.ModelName, "test-model-v1")
	}
}

// LD-002: Config with MCP servers auto-loads them.
func TestLoadAndAssemble_AutoLoadMCPServers(t *testing.T) {
	settings := Settings{
		Extra: map[string]any{
			"mcp_servers": []any{
				map[string]any{
					"name": "test-stdio",
					"type": "stdio",
					"command": "echo",
					"args": []any{"hello"},
				},
			},
		},
	}

	ac, err := LoadAndAssemble(settings)
	if err != nil {
		t.Fatalf("LoadAndAssemble: %v", err)
	}

	if ac.MCPServers == nil {
		t.Fatal("MCPServers should not be nil when mcp_servers is configured")
	}

	names := ac.MCPServers.Names()
	found := false
	for _, n := range names {
		if n == "test-stdio" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MCP server %q in registry, got %v", "test-stdio", names)
	}

	// Cleanup
	_ = ac.MCPServers.Close()
}

// LD-003: Config with tools auto-registers them.
func TestLoadAndAssemble_AutoRegisterTools(t *testing.T) {
	settings := Settings{
		Workspace: t.TempDir(),
		Extra: map[string]any{
			"tools": []any{"read_file", "glob", "grep"},
		},
	}

	ac, err := LoadAndAssemble(settings)
	if err != nil {
		t.Fatalf("LoadAndAssemble: %v", err)
	}

	if ac.ToolRegistry == nil {
		t.Fatal("ToolRegistry should not be nil when tools is configured")
	}

	tools, err := ac.ToolRegistry.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	for _, name := range []string{"read_file", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("tool %q not found in registry", name)
		}
	}
}

// LD-004: Config without Provider/MCP/tools returns nil components (backward compat).
func TestLoadAndAssemble_BackwardCompat(t *testing.T) {
	settings := Settings{
		MaxTurns: 42,
		CompactThreshold: 5000,
	}

	ac, err := LoadAndAssemble(settings)
	if err != nil {
		t.Fatalf("LoadAndAssemble: %v", err)
	}

	if ac.Provider != nil {
		t.Error("Provider should be nil when not configured")
	}
	if ac.ToolRegistry != nil {
		t.Error("ToolRegistry should be nil when not configured")
	}
	if ac.MCPServers != nil {
		t.Error("MCPServers should be nil when not configured")
	}
}

// LD-005: Unknown provider returns an error.
func TestLoadAndAssemble_UnknownProvider(t *testing.T) {
	settings := Settings{
		Provider: "nonexistent-provider-xyz",
		Model: "some-model",
	}

	_, err := LoadAndAssemble(settings)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

// LD-006: Unknown tool name returns an error.
func TestLoadAndAssemble_UnknownTool(t *testing.T) {
	settings := Settings{
		Workspace: t.TempDir(),
		Extra: map[string]any{
			"tools": []any{"nonexistent_tool_xyz"},
		},
	}

	_, err := LoadAndAssemble(settings)
	if err == nil {
		t.Error("expected error for unknown tool name")
	}
}

// LD-007: MCP server missing name returns an error.
func TestLoadAndAssemble_MCPMissingName(t *testing.T) {
	settings := Settings{
		Extra: map[string]any{
			"mcp_servers": []any{
				map[string]any{
					"type": "stdio",
					"command": "echo",
				},
			},
		},
	}

	_, err := LoadAndAssemble(settings)
	if err == nil {
		t.Error("expected error for MCP server missing name")
	}
}

// LD-008: parseToolNames rejects non-array values.
func TestParseToolNames_InvalidType(t *testing.T) {
	_, err := parseToolNames("not an array")
	if err == nil {
		t.Error("expected error for non-array tools value")
	}
}

// LD-009: parseMCPServers rejects non-array values.
func TestParseMCPServers_InvalidType(t *testing.T) {
	_, err := parseMCPServers("not an array")
	if err == nil {
		t.Error("expected error for non-array mcp_servers value")
	}
}
