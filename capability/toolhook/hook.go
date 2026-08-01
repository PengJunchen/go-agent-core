// Package toolhook 定义工具钩子管道。
//
// ToolHook 提供 Before/After 双钩子，支持阻止执行、修改结果、提前终止。
// ArgumentsPreparer 可选接口提供 PrepareArguments 能力，
// 允许在工具执行前预处理参数（如 LLM 填充缺失参数、校验/转换参数）。
//
// 这是 的 beforeToolCall/afterToolCall/prepareArguments 等价能力，
// 弥补了 仅观察无法拦截的缺陷。
//
// HookPipeline 按优先级顺序执行 Before（升序），After（降序），
// 任一 Before 返回 Block=true 则阻止执行。
// PrepareArguments 按优先级升序执行，返回 error 表示校验失败。
package toolhook

import (
	"context"
	"sync"
)

// ToolHook 是工具钩子接口。
type ToolHook interface {
	// Before 在工具执行前调用。返回 Block=true 阻止执行。
	// 返回 Defer=true 表示推迟决策到 PrepareArguments 阶段。
	Before(ctx context.Context, call *ToolCall) (*BeforeResult, error)
	// After 在工具执行后调用。可修改结果或提前终止循环。
	After(ctx context.Context, call *ToolCall, result *ToolResult) (*AfterResult, error)
}

// ArgumentsPreparer 是可选接口，提供参数预处理能力。
//
// ToolHook 实现者可选择实现此接口以支持参数预处理。
// HookPipeline.PrepareArguments 通过类型断言检测实现。
// 未实现此接口的钩子在 PrepareArguments 阶段被跳过（no-op）。
type ArgumentsPreparer interface {
	// PrepareArguments 在 Before 之后、工具执行前调用。
	// 返回修改后的 ToolCall（可为同一个指针），或返回 error 表示校验失败。
	PrepareArguments(ctx context.Context, call *ToolCall) (*ToolCall, error)
}

// ToolCall 描述一次工具调用。
type ToolCall struct {
	ID string
	Name string
	Arguments map[string]any
	SessionID string
	TurnID string
}

// ToolResult 描述工具执行结果。
type ToolResult struct {
	Content string
	IsError bool
	Details map[string]any
	Metadata map[string]any
}

// BeforeResult 是 Before 钩子的返回值。
type BeforeResult struct {
	Block bool
	ModifiedCall *ToolCall
	Terminate bool
	// Defer 表示推迟决策到 PrepareArguments 阶段。
	// 当 Defer=true 时，不立即阻止也不立即放行，
	// 最终决策由 PrepareArguments 的返回值决定：
	// - 返回 nil error → 放行（参数可能被修改）
	// - 返回 error → 视为校验失败，阻止执行
	Defer bool
	Reason string
}

// AfterResult 是 After 钩子的返回值。
type AfterResult struct {
	ModifiedResult *ToolResult
	Terminate bool
}

// HookRegistration 注册一个钩子及其优先级。
type HookRegistration struct {
	Hook ToolHook
	Priority int // 数值越小优先级越高（越先执行 Before）
}

// HookPipeline 是钩子管道实现。
type HookPipeline struct {
	mu sync.RWMutex
	hooks []HookRegistration
}

// NewHookPipeline 构造空管道。
func NewHookPipeline() *HookPipeline {
	return &HookPipeline{}
}

// Register 注册一个钩子。
func (p *HookPipeline) Register(hook ToolHook, priority int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hooks = append(p.hooks, HookRegistration{Hook: hook, Priority: priority})
}

// Before 按优先级升序执行所有 Before 钩子。
//
// 执行逻辑：
// - 任一钩子返回 Block=true → 立即返回 Block
// - 任一钩子返回 Terminate=true → 立即返回 Terminate
// - 任一钩子返回 Defer=true → 记录延迟，继续执行后续钩子
// - ModifiedCall 非空时更新当前调用参数
// - 全部执行完毕后，汇总 Defer 状态返回
func (p *HookPipeline) Before(ctx context.Context, call *ToolCall) (*BeforeResult, error) {
	sorted := p.sortedAsc()
	var deferred bool
	for _, reg := range sorted {
		res, err := reg.Hook.Before(ctx, call)
		if err != nil {
			return nil, err
		}
		if res != nil && (res.Block || res.Terminate) {
			return res, nil
		}
		if res != nil && res.Defer {
			deferred = true
		}
		if res != nil && res.ModifiedCall != nil {
			call = res.ModifiedCall
		}
	}
	result := &BeforeResult{Block: false}
	if deferred {
		result.Defer = true
	}
	// 携带最终修改后的 call，使调用方可获取钩子对参数的修改
	result.ModifiedCall = call
	return result, nil
}

// PrepareArguments 按优先级升序执行所有实现了 ArgumentsPreparer 的钩子。
//
// 仅当 Before 未阻止时调用。每个钩子可修改 ToolCall（如填充缺失参数、
// 转换参数类型），返回 error 表示校验失败，应阻止工具执行。
//
// 未实现 ArgumentsPreparer 的钩子自动跳过（等效 no-op）。
func (p *HookPipeline) PrepareArguments(ctx context.Context, call *ToolCall) (*ToolCall, error) {
	sorted := p.sortedAsc()
	for _, reg := range sorted {
		if preparer, ok := reg.Hook.(ArgumentsPreparer); ok {
			modifiedCall, err := preparer.PrepareArguments(ctx, call)
			if err != nil {
				return nil, err
			}
			if modifiedCall != nil {
				call = modifiedCall
			}
		}
	}
	return call, nil
}

// After 按优先级降序执行所有 After 钩子。
func (p *HookPipeline) After(ctx context.Context, call *ToolCall, result *ToolResult) (*AfterResult, error) {
	sorted := p.sortedDesc()
	for _, reg := range sorted {
		res, err := reg.Hook.After(ctx, call, result)
		if err != nil {
			return nil, err
		}
		if res != nil && res.Terminate {
			return res, nil
		}
		if res != nil && res.ModifiedResult != nil {
			result = res.ModifiedResult
		}
	}
	return &AfterResult{}, nil
}

func (p *HookPipeline) sortedAsc() []HookRegistration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]HookRegistration, len(p.hooks))
	copy(out, p.hooks)
	sortByPriority(out, true)
	return out
}

func (p *HookPipeline) sortedDesc() []HookRegistration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]HookRegistration, len(p.hooks))
	copy(out, p.hooks)
	sortByPriority(out, false)
	return out
}

func sortByPriority(hooks []HookRegistration, asc bool) {
	for i := 1; i < len(hooks); i++ {
		for j := i; j > 0; j-- {
			cond := hooks[j].Priority < hooks[j-1].Priority
			if !asc {
				cond = hooks[j].Priority > hooks[j-1].Priority
			}
			if cond {
				hooks[j], hooks[j-1] = hooks[j-1], hooks[j]
			} else {
				break
			}
		}
	}
}
