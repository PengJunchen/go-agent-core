package registry

import (
	"context"
	"errors"
	"testing"
)

// mockRegistry 是一个最小可用的内存 ToolRegistry 实现，用于验证
// 注册/查询/卸载/重载的接口契约行为（而非仅编译断言）。
type mockRegistry struct {
	tools map[string]ToolDefinition
	reload int
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{tools: make(map[string]ToolDefinition)}
}

func (r *mockRegistry) RegisterTool(_ context.Context, tool ToolDefinition) error {
	if tool.Name == "" {
		return errors.New("tool name is empty")
	}
	if _, exists := r.tools[tool.Name]; exists {
		return errors.New("tool already registered: " + tool.Name)
	}
	r.tools[tool.Name] = tool
	return nil
}

func (r *mockRegistry) UnregisterTool(_ context.Context, name string) error {
	if _, exists := r.tools[name]; !exists {
		return errors.New("tool not found: " + name)
	}
	delete(r.tools, name)
	return nil
}

func (r *mockRegistry) GetTool(_ context.Context, name string) (ToolDefinition, error) {
	tool, exists := r.tools[name]
	if !exists {
		return ToolDefinition{}, errors.New("tool not found: " + name)
	}
	return tool, nil
}

func (r *mockRegistry) ListTools(_ context.Context) ([]ToolDefinition, error) {
	out := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out, nil
}

func (r *mockRegistry) Reload(_ context.Context) error {
	r.reload++
	return nil
}

// Interface-001: ToolRegistry 接口可被 mock 实现。
func TestToolRegistry_InterfaceContract(t *testing.T) {
	var _ ToolRegistry = (*mockRegistry)(nil)
}

// VT-001: 注册后可按名查询。
func TestToolRegistry_RegisterAndGet(t *testing.T) {
	r := newMockRegistry()
	tool := ToolDefinition{
		Name: "search",
		Description: "search the web",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
			return &ToolResult{Content: "ok"}, nil
		},
	}
	if err := r.RegisterTool(context.Background(), tool); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	got, err := r.GetTool(context.Background(), "search")
	if err != nil {
		t.Fatalf("GetTool: %v", err)
	}
	if got.Name != "search" || got.Description != "search the web" {
		t.Errorf("GetTool = %+v, want search/search the web", got)
	}
}

// VT-002: 注册空名工具被拒绝。
func TestToolRegistry_RegisterEmptyName(t *testing.T) {
	r := newMockRegistry()
	err := r.RegisterTool(context.Background(), ToolDefinition{})
	if err == nil {
		t.Error("RegisterTool with empty name should fail")
	}
}

// VT-003: 重复注册同名工具被拒绝。
func TestToolRegistry_DuplicateRegister(t *testing.T) {
	r := newMockRegistry()
	tool := ToolDefinition{Name: "dup"}
	if err := r.RegisterTool(context.Background(), tool); err != nil {
		t.Fatalf("first RegisterTool: %v", err)
	}
	if err := r.RegisterTool(context.Background(), tool); err == nil {
		t.Error("duplicate RegisterTool should fail")
	}
}

// VT-004: 查询不存在的工具返回错误。
func TestToolRegistry_GetMissing(t *testing.T) {
	r := newMockRegistry()
	if _, err := r.GetTool(context.Background(), "nope"); err == nil {
		t.Error("GetTool for missing tool should fail")
	}
}

// VT-005: ListTools 列出全部已注册工具。
func TestToolRegistry_ListTools(t *testing.T) {
	r := newMockRegistry()
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "a"})
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "b"})
	tools, err := r.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Errorf("ListTools count = %d, want 2", len(tools))
	}
}

// VT-006: 卸载工具后查询应失败。
func TestToolRegistry_Unregister(t *testing.T) {
	r := newMockRegistry()
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "rm"})
	if err := r.UnregisterTool(context.Background(), "rm"); err != nil {
		t.Fatalf("UnregisterTool: %v", err)
	}
	if _, err := r.GetTool(context.Background(), "rm"); err == nil {
		t.Error("GetTool after UnregisterTool should fail")
	}
}

// VT-007: 卸载不存在的工具返回错误。
func TestToolRegistry_UnregisterMissing(t *testing.T) {
	r := newMockRegistry()
	if err := r.UnregisterTool(context.Background(), "ghost"); err == nil {
		t.Error("UnregisterTool for missing tool should fail")
	}
}

// VT-008: Reload 被调用且不报错。
func TestToolRegistry_Reload(t *testing.T) {
	r := newMockRegistry()
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if r.reload != 1 {
		t.Errorf("reload count = %d, want 1", r.reload)
	}
}

// VT-009: ToolHandler 可被调用并返回结果（验证函数字段契约）。
func TestToolHandler_Callable(t *testing.T) {
	r := newMockRegistry()
	handler := func(_ context.Context, args map[string]any) (*ToolResult, error) {
		return &ToolResult{Content: args["q"].(string)}, nil
	}
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "echo", Handler: handler})
	tool, _ := r.GetTool(context.Background(), "echo")
	res, err := tool.Handler(context.Background(), map[string]any{"q": "hi"})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if res.Content != "hi" {
		t.Errorf("Handler result = %q, want %q", res.Content, "hi")
	}
}
