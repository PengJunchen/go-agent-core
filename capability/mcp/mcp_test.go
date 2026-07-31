package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// mockMCPProvider 实现MCPProvider接口，用于编译契约验证与基本行为测试。
type mockMCPProvider struct {
	tools []ToolRef
	callErr error
	connected bool
}

func (m *mockMCPProvider) Connect(_ context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error) {
	m.connected = true
	cleanups := make([]func(), 0, len(servers))
	for range servers {
		cleanups = append(cleanups, func() { m.connected = false })
	}
	return m.tools, cleanups, nil
}

func (m *mockMCPProvider) Disconnect() error {
	m.connected = false
	return nil
}

func (m *mockMCPProvider) Reload(_ context.Context, _ []MCPServerConfig) ([]ToolRef, []func(), []error) {
	return m.tools, nil, nil
}

func (m *mockMCPProvider) Call(_ context.Context, _, _ string, _ json.RawMessage) (json.RawMessage, error) {
	if m.callErr != nil {
		return nil, m.callErr
	}
	return json.RawMessage(`{"ok":true}`), nil
}

func (m *mockMCPProvider) ListTools(_ context.Context) ([]ToolRef, error) {
	return m.tools, nil
}

// Interface-001: MCPProvider 接口可被 mock 实现。
func TestMCPProvider_InterfaceContract(t *testing.T) {
	var _ MCPProvider = (*mockMCPProvider)(nil)
}

// VT-001: Connect 返回发现的工具与清理函数。
func TestMCPProvider_Connect(t *testing.T) {
	provider := &mockMCPProvider{
		tools: []ToolRef{
			{Name: "search", ServerName: "srv1"},
			{Name: "fetch", ServerName: "srv1"},
		},
	}
	tools, cleanups, errs := provider.Connect(context.Background(), []MCPServerConfig{
		{Name: "srv1", Type: "stdio", Command: "echo"},
	})
	if len(errs) != 0 {
		t.Fatalf("Connect errors = %v, want none", errs)
	}
	if len(tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(tools))
	}
	if len(cleanups) != 1 {
		t.Fatalf("cleanups count = %d, want 1", len(cleanups))
	}
	if !provider.connected {
		t.Error("provider should be connected after Connect")
	}
	cleanups[0]()
	if provider.connected {
		t.Error("provider should be disconnected after cleanup")
	}
}

// VT-002: ListTools 返回已发现工具。
func TestMCPProvider_ListTools(t *testing.T) {
	provider := &mockMCPProvider{
		tools: []ToolRef{{Name: "search", ServerName: "srv1"}},
	}
	tools, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "search" {
		t.Errorf("tools = %v, want [{search}]", tools)
	}
}

// VT-003: Call 成功返回 JSON 结果。
func TestMCPProvider_Call(t *testing.T) {
	provider := &mockMCPProvider{}
	res, err := provider.Call(context.Background(), "srv1", "search", json.RawMessage(`{"q":"x"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got map[string]bool
	if err := json.Unmarshal(res, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got["ok"] {
		t.Errorf("Call result = %s, want {\"ok\":true}", string(res))
	}
}

// VT-004: Call 在底层失败时返回错误。
func TestMCPProvider_CallError(t *testing.T) {
	errBoom := errors.New("server unreachable")
	provider := &mockMCPProvider{callErr: errBoom}
	if _, err := provider.Call(context.Background(), "srv1", "search", nil); err != errBoom {
		t.Errorf("Call error = %v, want %v", err, errBoom)
	}
}

// VT-005: Disconnect 断开连接。
func TestMCPProvider_Disconnect(t *testing.T) {
	provider := &mockMCPProvider{tools: []ToolRef{{Name: "t"}}}
	provider.connected = true
	if err := provider.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if provider.connected {
		t.Error("provider still connected after Disconnect")
	}
}
