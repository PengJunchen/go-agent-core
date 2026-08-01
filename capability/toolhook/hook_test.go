package toolhook

import (
	"context"
	"errors"
	"testing"
)

// Interface-001: ToolHook 接口可被实现。
func TestToolHook_Interface(t *testing.T) {
	var _ ToolHook = (*noopHook)(nil)
}

// Interface-002: ArgumentsPreparer 可选接口可被实现。
func TestArgumentsPreparer_Interface(t *testing.T) {
	var _ ToolHook = (*prepareArgsHook)(nil)
	var _ ArgumentsPreparer = (*prepareArgsHook)(nil)
}

// Interface-003: ToolHook 不实现 ArgumentsPreparer 时，管道跳过。
func TestToolHook_WithoutArgumentsPreparer(t *testing.T) {
	var _ ToolHook = (*noopHook)(nil)
	// noopHook 不实现 ArgumentsPreparer，但仍是合法的 ToolHook
	hook := &noopHook{}
	if _, ok := any(hook).(ArgumentsPreparer); ok {
		t.Error("noopHook should not implement ArgumentsPreparer")
	}
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

// VT-004: HookPipeline Before Defer 模式。
func TestHookPipeline_BeforeDefer(t *testing.T) {
	var order []int
	p := NewHookPipeline()
	p.Register(&deferHook{}, 1)
	p.Register(&orderHook{order: &order, id: 2}, 2)

	res, err := p.Before(context.Background(), &ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if res.Block {
		t.Error("Defer should not block")
	}
	if !res.Defer {
		t.Error("expected Defer=true")
	}
}

// VT-005: Before 中 Block 优先于 Defer。
func TestHookPipeline_BeforeBlockOverridesDefer(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&deferHook{}, 1)
	p.Register(&blockHook{}, 2)

	res, err := p.Before(context.Background(), &ToolCall{Name: "test"})
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if !res.Block {
		t.Error("Block should take precedence over Defer")
	}
}

// ─── PrepareArguments 测试 ──────────────────────────────────────────

// TestPrepareArguments_ModifyArgs 验证钩子修改参数。
func TestPrepareArguments_ModifyArgs(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&prepareArgsHook{
		modifyFn: func(_ context.Context, call *ToolCall) (*ToolCall, error) {
			call.Arguments["injected_key"] = "injected_value"
			return call, nil
		},
	}, 1)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{"original_key": "original_value"},
	}

	modified, err := p.PrepareArguments(context.Background(), call)
	if err != nil {
		t.Fatalf("PrepareArguments: %v", err)
	}
	if modified.Arguments["injected_key"] != "injected_value" {
		t.Errorf("expected injected_key=injected_value, got %v", modified.Arguments["injected_key"])
	}
	if modified.Arguments["original_key"] != "original_value" {
		t.Errorf("original key should be preserved, got %v", modified.Arguments["original_key"])
	}
}

// TestPrepareArguments_ValidationError 验证钩子返回错误时工具不执行。
func TestPrepareArguments_ValidationError(t *testing.T) {
	validationErr := errors.New("missing required argument: user_id")
	p := NewHookPipeline()
	p.Register(&prepareArgsHook{
		modifyFn: func(_ context.Context, _ *ToolCall) (*ToolCall, error) {
			return nil, validationErr
		},
	}, 1)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{},
	}

	modified, err := p.PrepareArguments(context.Background(), call)
	if err == nil {
		t.Fatal("expected error from PrepareArguments")
	}
	if !errors.Is(err, validationErr) {
		t.Errorf("expected validation error, got %v", err)
	}
	if modified != nil {
		t.Error("expected nil ToolCall on error")
	}
}

// TestPrepareArguments_DeferMode 验证 Before 返回 Defer 后，PrepareArguments 放行。
func TestPrepareArguments_DeferMode(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&deferPrepareHook{}, 1)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{"key": "value"},
	}

	// Before 阶段：钩子返回 Defer
	beforeRes, err := p.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if beforeRes.Block {
		t.Error("Defer mode should not block in Before phase")
	}
	if !beforeRes.Defer {
		t.Error("expected Defer=true from Before")
	}

	// PrepareArguments 阶段：钩子放行
	modified, err := p.PrepareArguments(context.Background(), call)
	if err != nil {
		t.Fatalf("PrepareArguments: %v", err)
	}
	if modified == nil {
		t.Fatal("expected non-nil ToolCall after PrepareArguments")
	}
}

// TestPrepareArguments_DeferModeBlocked 验证 Before 返回 Defer 后，
// PrepareArguments 返回错误时视为校验失败。
func TestPrepareArguments_DeferModeBlocked(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&deferPrepareBlockHook{}, 1)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{},
	}

	// Before 阶段：钩子返回 Defer
	beforeRes, err := p.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if !beforeRes.Defer {
		t.Error("expected Defer=true from Before")
	}

	// PrepareArguments 阶段：钩子返回错误
	_, err = p.PrepareArguments(context.Background(), call)
	if err == nil {
		t.Fatal("expected error from PrepareArguments (validation failure)")
	}
}

// TestPrepareArguments_PipelineOrder 验证执行顺序是 Before → Prepare → Execute → After。
func TestPrepareArguments_PipelineOrder(t *testing.T) {
	var order []string

	p := NewHookPipeline()
	p.Register(&trackingHook{phase: &order, name: "hook1"}, 1)
	p.Register(&trackingHook{phase: &order, name: "hook2"}, 2)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{},
	}

	// Before
	_, err := p.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}

	// PrepareArguments
	_, err = p.PrepareArguments(context.Background(), call)
	if err != nil {
		t.Fatalf("PrepareArguments: %v", err)
	}

	// 模拟 Execute（不在管道内）

	// After
	_, err = p.After(context.Background(), call, &ToolResult{Content: "ok"})
	if err != nil {
		t.Fatalf("After: %v", err)
	}

	expected := []string{
		"hook1:before", "hook2:before",
		"hook1:prepare", "hook2:prepare",
		"hook2:after", "hook1:after",
	}
	if len(order) != len(expected) {
		t.Fatalf("execution order length = %d, want %d; got %v", len(order), len(expected), order)
	}
	for i, phase := range expected {
		if order[i] != phase {
			t.Errorf("order[%d] = %q, want %q", i, order[i], phase)
		}
	}
}

// TestHookPipeline_PrepareArgumentsInChain 验证多钩子链式执行。
func TestHookPipeline_PrepareArgumentsInChain(t *testing.T) {
	p := NewHookPipeline()

	// 钩子1：添加 default_region
	p.Register(&prepareArgsHook{
		modifyFn: func(_ context.Context, call *ToolCall) (*ToolCall, error) {
			call.Arguments["default_region"] = "us-east-1"
			return call, nil
		},
	}, 1)

	// 钩子2：添加 default_limit 并校验
	p.Register(&prepareArgsHook{
		modifyFn: func(_ context.Context, call *ToolCall) (*ToolCall, error) {
			if _, ok := call.Arguments["default_region"]; !ok {
				return nil, errors.New("default_region not set")
			}
			call.Arguments["default_limit"] = 100
			return call, nil
		},
	}, 2)

	// 钩子3：非 ArgumentsPreparer，应被跳过
	p.Register(&noopHook{}, 3)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{},
	}

	modified, err := p.PrepareArguments(context.Background(), call)
	if err != nil {
		t.Fatalf("PrepareArguments: %v", err)
	}
	if modified.Arguments["default_region"] != "us-east-1" {
		t.Errorf("expected default_region=us-east-1, got %v", modified.Arguments["default_region"])
	}
	if modified.Arguments["default_limit"] != 100 {
		t.Errorf("expected default_limit=100, got %v", modified.Arguments["default_limit"])
	}
}

// TestHookPipeline_PrepareArgumentsInChain_ErrorStopsChain 验证链中错误停止后续钩子。
func TestHookPipeline_PrepareArgumentsInChain_ErrorStopsChain(t *testing.T) {
	p := NewHookPipeline()

	// 钩子1：校验失败
	p.Register(&prepareArgsHook{
		modifyFn: func(_ context.Context, _ *ToolCall) (*ToolCall, error) {
			return nil, errors.New("validation failed: missing user_id")
		},
	}, 1)

	// 钩子2：不应执行
	p.Register(&prepareArgsHook{
		modifyFn: func(_ context.Context, call *ToolCall) (*ToolCall, error) {
			call.Arguments["should_not_be_set"] = true
			return call, nil
		},
	}, 2)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{},
	}

	_, err := p.PrepareArguments(context.Background(), call)
	if err == nil {
		t.Fatal("expected error from PrepareArguments")
	}
	if _, exists := call.Arguments["should_not_be_set"]; exists {
		t.Error("hook2 should not have been executed after hook1 error")
	}
}

// TestHookPipeline_PrepareArguments_SkipsNonPreparer 验证不实现 ArgumentsPreparer 的钩子被跳过。
func TestHookPipeline_PrepareArguments_SkipsNonPreparer(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&noopHook{}, 1) // 不实现 ArgumentsPreparer

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{"key": "value"},
	}

	modified, err := p.PrepareArguments(context.Background(), call)
	if err != nil {
		t.Fatalf("PrepareArguments: %v", err)
	}
	if modified.Arguments["key"] != "value" {
		t.Errorf("arguments should be unchanged, got %v", modified.Arguments)
	}
}

// TestHookPipeline_BeforeDeferWithPrepareArguments 验证完整 Defer 流程：
// Before(Defer) → PrepareArguments(放行) → After。
func TestHookPipeline_BeforeDeferWithPrepareArguments(t *testing.T) {
	p := NewHookPipeline()
	p.Register(&deferPrepareHook{}, 1)

	call := &ToolCall{
		ID: "tc-1",
		Name: "test_tool",
		Arguments: map[string]any{"key": "value"},
	}

	// Step 1: Before — Defer
	beforeRes, err := p.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if !beforeRes.Defer {
		t.Fatal("expected Defer=true")
	}

	// Step 2: PrepareArguments — approve
	modified, err := p.PrepareArguments(context.Background(), call)
	if err != nil {
		t.Fatalf("PrepareArguments: %v", err)
	}

	// Step 3: After — 正常
	afterRes, err := p.After(context.Background(), modified, &ToolResult{Content: "ok"})
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	if afterRes.Terminate {
		t.Error("After should not terminate")
	}
}

// ─── 测试辅助类型 ──────────────────────────────────────────────────

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

type deferHook struct{}

func (deferHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	return &BeforeResult{Defer: true, Reason: "deferred to PrepareArguments"}, nil
}
func (deferHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	return &AfterResult{}, nil
}

// prepareArgsHook 同时实现 ToolHook 和 ArgumentsPreparer。
type prepareArgsHook struct {
	modifyFn func(ctx context.Context, call *ToolCall) (*ToolCall, error)
}

func (h *prepareArgsHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	return &BeforeResult{Block: false}, nil
}
func (h *prepareArgsHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	return &AfterResult{}, nil
}
func (h *prepareArgsHook) PrepareArguments(ctx context.Context, call *ToolCall) (*ToolCall, error) {
	if h.modifyFn == nil {
		return call, nil
	}
	return h.modifyFn(ctx, call)
}

// deferPrepareHook 实现 Before 返回 Defer，PrepareArguments 放行。
type deferPrepareHook struct{}

func (deferPrepareHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	return &BeforeResult{Defer: true, Reason: "deferred"}, nil
}
func (deferPrepareHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	return &AfterResult{}, nil
}
func (deferPrepareHook) PrepareArguments(_ context.Context, call *ToolCall) (*ToolCall, error) {
	return call, nil
}

// deferPrepareBlockHook 实现 Before 返回 Defer，PrepareArguments 阻止。
type deferPrepareBlockHook struct{}

func (deferPrepareBlockHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	return &BeforeResult{Defer: true, Reason: "deferred"}, nil
}
func (deferPrepareBlockHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	return &AfterResult{}, nil
}
func (deferPrepareBlockHook) PrepareArguments(_ context.Context, _ *ToolCall) (*ToolCall, error) {
	return nil, errors.New("validation failed in PrepareArguments")
}

// trackingHook 跟踪执行阶段，同时实现 ToolHook 和 ArgumentsPreparer。
type trackingHook struct {
	phase *[]string
	name string
}

func (h *trackingHook) Before(_ context.Context, _ *ToolCall) (*BeforeResult, error) {
	*h.phase = append(*h.phase, h.name+":before")
	return &BeforeResult{Block: false}, nil
}
func (h *trackingHook) After(_ context.Context, _ *ToolCall, _ *ToolResult) (*AfterResult, error) {
	*h.phase = append(*h.phase, h.name+":after")
	return &AfterResult{}, nil
}
func (h *trackingHook) PrepareArguments(_ context.Context, call *ToolCall) (*ToolCall, error) {
	*h.phase = append(*h.phase, h.name+":prepare")
	return call, nil
}
