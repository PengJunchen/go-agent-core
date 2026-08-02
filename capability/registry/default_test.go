package registry

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// DT-001: 编译时接口断言。
func TestDefaultToolRegistry_InterfaceCheck(t *testing.T) {
	// 已在 default.go 中通过 var _ 声明，此处额外验证实例化。
	var _ ToolRegistry = NewDefaultToolRegistry()
}

// DT-002: 注册后可按名查询。
func TestDefaultToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewDefaultToolRegistry()
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
		t.Errorf("GetTool = {Name=%q, Description=%q}, want search/search the web", got.Name, got.Description)
	}
}

// DT-003: 注册空名工具被拒绝。
func TestDefaultToolRegistry_RegisterEmptyName(t *testing.T) {
	r := NewDefaultToolRegistry()
	err := r.RegisterTool(context.Background(), ToolDefinition{})
	if err == nil {
		t.Error("RegisterTool with empty name should fail")
	}
}

// DT-004: 重复注册同名工具被拒绝。
func TestDefaultToolRegistry_DuplicateRegister(t *testing.T) {
	r := NewDefaultToolRegistry()
	tool := ToolDefinition{Name: "dup"}
	if err := r.RegisterTool(context.Background(), tool); err != nil {
		t.Fatalf("first RegisterTool: %v", err)
	}
	if err := r.RegisterTool(context.Background(), tool); err == nil {
		t.Error("duplicate RegisterTool should fail")
	}
}

// DT-005: 查询不存在的工具返回错误。
func TestDefaultToolRegistry_GetMissing(t *testing.T) {
	r := NewDefaultToolRegistry()
	_, err := r.GetTool(context.Background(), "nope")
	if err == nil {
		t.Error("GetTool for missing tool should fail")
	}
}

// DT-006: ListTools 返回按名称排序的工具列表。
func TestDefaultToolRegistry_ListTools_Sorted(t *testing.T) {
	r := NewDefaultToolRegistry()
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "charlie"})
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "alpha"})
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "bravo"})

	tools, err := r.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("ListTools count = %d, want 3", len(tools))
	}
	// 验证排序
	if tools[0].Name != "alpha" || tools[1].Name != "bravo" || tools[2].Name != "charlie" {
		t.Errorf("ListTools order = %v, want [alpha, bravo, charlie]",
			[]string{tools[0].Name, tools[1].Name, tools[2].Name})
	}
}

// DT-007: ListTools 在空注册表时返回空切片。
func TestDefaultToolRegistry_ListTools_Empty(t *testing.T) {
	r := NewDefaultToolRegistry()
	tools, err := r.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("ListTools count = %d, want 0", len(tools))
	}
}

// DT-008: 卸载工具后查询应失败。
func TestDefaultToolRegistry_Unregister(t *testing.T) {
	r := NewDefaultToolRegistry()
	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "rm"})
	if err := r.UnregisterTool(context.Background(), "rm"); err != nil {
		t.Fatalf("UnregisterTool: %v", err)
	}
	if _, err := r.GetTool(context.Background(), "rm"); err == nil {
		t.Error("GetTool after UnregisterTool should fail")
	}
}

// DT-009: 卸载不存在的工具返回错误。
func TestDefaultToolRegistry_UnregisterMissing(t *testing.T) {
	r := NewDefaultToolRegistry()
	if err := r.UnregisterTool(context.Background(), "ghost"); err == nil {
		t.Error("UnregisterTool for missing tool should fail")
	}
}

// DT-010: Reload 始终返回 nil。
func TestDefaultToolRegistry_Reload(t *testing.T) {
	r := NewDefaultToolRegistry()
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

// DT-011: ToolHandler 可被调用并返回结果。
func TestDefaultToolRegistry_HandlerCallable(t *testing.T) {
	r := NewDefaultToolRegistry()
	handler := func(_ context.Context, args map[string]any) (*ToolResult, error) {
		return &ToolResult{Content: args["q"].(string), IsError: false, Details: map[string]any{"key": "val"}}, nil
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
	if res.IsError {
		t.Error("Handler result IsError should be false")
	}
	if res.Details["key"] != "val" {
		t.Errorf("Handler result Details[key] = %v, want val", res.Details["key"])
	}
}

// DT-012: 并发注册/查询/卸载不会 panic。
func TestDefaultToolRegistry_Concurrent(t *testing.T) {
	r := NewDefaultToolRegistry()
	var wg sync.WaitGroup

	// 并发注册
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "tool-" + string(rune('A'+i%26))
			_ = r.RegisterTool(context.Background(), ToolDefinition{Name: name})
		}(i)
	}

	// 并发查询
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = r.GetTool(context.Background(), "tool-"+string(rune('A'+i%26)))
		}(i)
	}

	// 并发列表
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.ListTools(context.Background())
		}()
	}

	wg.Wait()

	// 并发卸载
	for i := range 10 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = r.UnregisterTool(context.Background(), "tool-"+string(rune('A'+i%26)))
		}(i)
	}

	wg.Wait()
}

// DT-013: 错误信息包含工具名称。
func TestDefaultToolRegistry_ErrorMessages(t *testing.T) {
	r := NewDefaultToolRegistry()

	err := r.RegisterTool(context.Background(), ToolDefinition{})
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty name error should mention 'empty', got: %v", err)
	}

	_ = r.RegisterTool(context.Background(), ToolDefinition{Name: "test-tool"})
	err = r.RegisterTool(context.Background(), ToolDefinition{Name: "test-tool"})
	if !strings.Contains(err.Error(), "test-tool") {
		t.Errorf("duplicate error should include tool name, got: %v", err)
	}

	_, err = r.GetTool(context.Background(), "missing-tool")
	if !strings.Contains(err.Error(), "missing-tool") {
		t.Errorf("get missing error should include tool name, got: %v", err)
	}

	err = r.UnregisterTool(context.Background(), "ghost-tool")
	if !strings.Contains(err.Error(), "ghost-tool") {
		t.Errorf("unregister missing error should include tool name, got: %v", err)
	}
}

// DT-014: RegisterDeferred 注册延迟加载工具，定义立即可用，Handler 按需加载。
func TestDefaultToolRegistry_RegisterDeferred(t *testing.T) {
	r := NewDefaultToolRegistry()

	loadCount := 0
	loader := &testDeferredLoader{
		load: func() (ToolHandler, error) {
			loadCount++
			return func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				return &ToolResult{Content: "deferred-ok"}, nil
			}, nil
		},
	}

	def := ToolDefinition{
		Name: "deferred-tool",
		Description: "A deferred tool",
		Parameters: map[string]any{"type": "object"},
	}

	// 注册后定义应可用
	if err := r.RegisterDeferred(context.Background(), def, loader); err != nil {
		t.Fatalf("RegisterDeferred: %v", err)
	}

	// Loader 不应被立即调用
	if loadCount != 0 {
		t.Errorf("loadCount = %d before invocation, want 0", loadCount)
	}

	// 工具定义可查询
	got, err := r.GetTool(context.Background(), "deferred-tool")
	if err != nil {
		t.Fatalf("GetTool after RegisterDeferred: %v", err)
	}
	if got.Name != "deferred-tool" {
		t.Errorf("got.Name = %q, want %q", got.Name, "deferred-tool")
	}

	// 首次调用 Handler 时加载
	result, err := got.Handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("Handler call: %v", err)
	}
	if result.Content != "deferred-ok" {
		t.Errorf("Handler result = %q, want %q", result.Content, "deferred-ok")
	}
	if loadCount != 1 {
		t.Errorf("loadCount = %d after first call, want 1", loadCount)
	}

	// 第二次调用不应重新加载
	_, _ = got.Handler(context.Background(), nil)
	if loadCount != 1 {
		t.Errorf("loadCount = %d after second call, want 1 (cached)", loadCount)
	}
}

// DT-015: RegisterDeferred 拒绝空名称。
func TestDefaultToolRegistry_RegisterDeferredEmptyName(t *testing.T) {
	r := NewDefaultToolRegistry()
	loader := &testDeferredLoader{
		load: func() (ToolHandler, error) {
			return func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				return &ToolResult{Content: "ok"}, nil
			}, nil
		},
	}
	err := r.RegisterDeferred(context.Background(), ToolDefinition{}, loader)
	if err == nil {
		t.Error("RegisterDeferred with empty name should fail")
	}
}

// DT-016: RegisterDeferred 拒绝重复注册。
func TestDefaultToolRegistry_RegisterDeferredDuplicate(t *testing.T) {
	r := NewDefaultToolRegistry()
	loader := &testDeferredLoader{
		load: func() (ToolHandler, error) {
			return func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				return &ToolResult{Content: "ok"}, nil
			}, nil
		},
	}
	def := ToolDefinition{Name: "dup-deferred"}
	if err := r.RegisterDeferred(context.Background(), def, loader); err != nil {
		t.Fatalf("first RegisterDeferred: %v", err)
	}
	if err := r.RegisterDeferred(context.Background(), def, loader); err == nil {
		t.Error("duplicate RegisterDeferred should fail")
	}
}

// DT-017: RegisterDeferred 加载失败时 Handler 调用返回错误。
func TestDefaultToolRegistry_RegisterDeferredLoadError(t *testing.T) {
	r := NewDefaultToolRegistry()
	loadErr := errors.New("load failed")
	loader := &testDeferredLoader{
		load: func() (ToolHandler, error) {
			return nil, loadErr
		},
	}
	def := ToolDefinition{Name: "load-err-tool"}
	if err := r.RegisterDeferred(context.Background(), def, loader); err != nil {
		t.Fatalf("RegisterDeferred: %v", err)
	}

	got, _ := r.GetTool(context.Background(), "load-err-tool")
	_, err := got.Handler(context.Background(), nil)
	if err == nil {
		t.Error("Handler should return error when Load fails")
	}
}

// DT-018: RegisterDeferred 工具出现在 ListTools 中。
func TestDefaultToolRegistry_RegisterDeferredListed(t *testing.T) {
	r := NewDefaultToolRegistry()
	loader := &testDeferredLoader{
		load: func() (ToolHandler, error) {
			return func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				return &ToolResult{Content: "ok"}, nil
			}, nil
		},
	}
	_ = r.RegisterDeferred(context.Background(), ToolDefinition{Name: "listed-deferred"}, loader)
	tools, err := r.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "listed-deferred" {
		t.Errorf("ListTools = %v, want [listed-deferred]", tools)
	}
}

// testDeferredLoader 是测试用 DeferredLoader 实现。
type testDeferredLoader struct {
	load func() (ToolHandler, error)
}

func (l *testDeferredLoader) Load() (ToolHandler, error) {
	return l.load()
}
