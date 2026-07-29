package toolhook

import (
	"context"
	"testing"
)

// Interface-001: ToolHook 接口可被实现。
func TestToolHook_Interface(t *testing.T) {
	var _ ToolHook = (*noopHook)(nil)
}

// VT-001: HookPipeline Before 按优先级升序执行。
func TestHookPipeline_BeforeOrder(t *testing.T) {
	var order []int
	p := NewHookPipeline()
	p.Register(&orderHook{order: &order, id: 2}, 2)
	p.Register(&orderHook{order: &order, id: 1}, 1)
	p.Register(&orderHook{order: &order, id: 3}, 3)

	_, err := p.Before(context.Background(), &ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("Before execution order = %v, want [1 2 3]", order)
	}
}

// VT-002: HookPipeline Before 阻止执行。
func TestHookPipeline_BeforeBlock(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&blockHook{}, 1)
	p.Register(&orderHook{id: 2}, 2)

	res, err := p.Before(context.Background(), &ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if !res.Block {
		t.Error("expected Block=true")
	}
}

// VT-003: HookPipeline After 按优先级降序执行。
func TestHookPipeline_AfterOrder(t *testing.T) {
	var order []int
	p := NewHookPipeline()
	p.Register(&orderHook{order: &order, id: 1}, 1)
	p.Register(&orderHook{order: &order, id: 2}, 2)

	_, err := p.After(context.Background(), &ToolCall{Name: "test"}, &ToolResult{Content: "ok"})
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	if len(order) != 2 || order[0] != 2 || order[1] != 1 {
		t.Errorf("After execution order = %v, want [2 1]", order)
	}
}

type noopHook struct{}

func (noopHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	return &BeforeResult{Block: false}, nil
}
func (noopHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	return &AfterResult{}, nil
}

type orderHook struct {
	order *[]int
	id int
}

func (h *orderHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	*h.order = append(*h.order, h.id)
	return &BeforeResult{Block: false}, nil
}
func (h *orderHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	*h.order = append(*h.order, h.id)
	return &AfterResult{}, nil
}

type blockHook struct{}

func (blockHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	return &BeforeResult{Block: true, Reason: "blocked"}, nil
}
func (blockHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	return &AfterResult{}, nil
}
