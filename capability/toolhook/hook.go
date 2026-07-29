// Package toolhook 定义工具钩子管道。
//
// ToolHook 提供 Before/After 双钩子，支持阻止执行、修改结果、提前终止。
// 这是 的 beforeToolCall/afterToolCall 等价能力，
// 弥补了 仅观察无法拦截的缺陷。
//
// HookPipeline 按优先级顺序执行 Before（升序），After（降序），
// 任一 Before 返回 Block=true 则阻止执行。
package toolhook

import "context"

// ToolHook 是工具钩子接口。
type ToolHook interface {
	// Before 在工具执行前调用。返回 Block=true 阻止执行。
	Before(ctx context.Context, call *ToolCall) (*BeforeResult, error)
	// After 在工具执行后调用。可修改结果或提前终止循环。
	After(ctx context.Context, call *ToolCall, result *ToolResult) (*AfterResult, error)
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
	hooks []HookRegistration
}

// NewHookPipeline 构造空管道。
func NewHookPipeline() *HookPipeline {
	return &HookPipeline{}
}

// Register 注册一个钩子。
func (p *HookPipeline) Register(hook ToolHook, priority int) {
	p.hooks = append(p.hooks, HookRegistration{Hook: hook, Priority: priority})
}

// Before 按优先级升序执行所有 Before 钩子。
func (p *HookPipeline) Before(ctx context.Context, call *ToolCall) (*BeforeResult, error) {
	sorted := p.sortedAsc()
	for _, reg := range sorted {
		res, err := reg.Hook.Before(ctx, call)
		if err != nil {
			return nil, err
		}
		if res != nil && (res.Block || res.Terminate) {
			return res, nil
		}
		if res != nil && res.ModifiedCall != nil {
			call = res.ModifiedCall
		}
	}
	return &BeforeResult{Block: false}, nil
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
	out := make([]HookRegistration, len(p.hooks))
	copy(out, p.hooks)
	sortByPriority(out, true)
	return out
}

func (p *HookPipeline) sortedDesc() []HookRegistration {
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
