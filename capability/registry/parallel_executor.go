package registry

import (
	"context"
	"sync"
)

// ExecutionMode 控制多个工具调用的执行方式。
type ExecutionMode int

const (
	// ExecutionSequential 逐个执行工具调用。
	ExecutionSequential ExecutionMode = iota
	// ExecutionParallel 并发执行可并行的工具调用。
	ExecutionParallel
)

// ToolCall 表示一次工具调用请求。
type ToolCall struct {
	ID string
	Name string
	Arguments map[string]any
	SessionID string
	TurnID string
}

// ToolExecutionResult 包含单次工具执行的结果。
type ToolExecutionResult struct {
	Call ToolCall
	Result *ToolResult
	Error error
}

// ExecutionNotifier receives notifications about tool execution progress.
// L2 (capability) layer defines this interface; L1 (agent) layer provides
// an adapter that converts updates into AgentEvents, avoiding L2→L1 dependency.
type ExecutionNotifier interface {
	NotifyToolExecution(update ToolExecutionUpdate)
}

// ToolExecutionUpdate represents a tool execution progress update.
type ToolExecutionUpdate struct {
	ToolCallID string
	ToolName string
	Status string // "started", "progress", "completed", "failed"
	Error error
	Progress float64 // 0.0 - 1.0，可选进度指示
	Message string // 可选状态消息
}

// ParallelToolExecutor 在安全时并行执行多个工具调用。
type ParallelToolExecutor struct {
	mode ExecutionMode
	maxConcurrent int // 最大并行数（0 = 不限）
	notifier ExecutionNotifier // 可选，nil 表示不发送通知
}

// NewParallelToolExecutor 创建一个指定模式的执行器。
// notifier 为可选参数，nil 表示不发送工具执行通知。
func NewParallelToolExecutor(mode ExecutionMode, maxConcurrent int, notifier ...ExecutionNotifier) *ParallelToolExecutor {
	var n ExecutionNotifier
	if len(notifier) > 0 {
		n = notifier[0]
	}
	return &ParallelToolExecutor{
		mode: mode,
		maxConcurrent: maxConcurrent,
		notifier: n,
	}
}

// ExecuteTools 执行一批工具调用。
// ExecutionParallel 模式下，标记为 ParallelSafe 的工具并发执行，
// 未标记的工具始终串行执行。结果按输入顺序返回。
func (e *ParallelToolExecutor) ExecuteTools(ctx context.Context, calls []ToolCall, reg ToolRegistry) []*ToolExecutionResult {
	results := make([]*ToolExecutionResult, len(calls))

	if e.mode == ExecutionSequential || len(calls) == 0 {
		for i, call := range calls {
			results[i] = e.executeOne(ctx, call, reg)
		}
		return results
	}

	// ExecutionParallel: 分组——并行安全 vs 串行
	type indexedCall struct {
		idx int
		call ToolCall
	}
	var parallel []indexedCall
	var sequential []indexedCall

	for i, call := range calls {
		tool, err := reg.GetTool(ctx, call.Name)
		if err != nil {
			// 工具未找到，直接记录错误结果
			e.notify(ToolExecutionUpdate{
				ToolCallID: call.ID,
				ToolName: call.Name,
				Status: "failed",
				Error: err,
			})
			results[i] = &ToolExecutionResult{
				Call: call,
				Error: err,
			}
			continue
		}
		if tool.ParallelSafe {
			parallel = append(parallel, indexedCall{idx: i, call: call})
		} else {
			sequential = append(sequential, indexedCall{idx: i, call: call})
		}
	}

	// 并行执行 ParallelSafe 的调用
	if len(parallel) > 0 {
		max := e.maxConcurrent
		if max <= 0 {
			max = len(parallel)
		}
		sem := make(chan struct{}, max)
		var wg sync.WaitGroup

		for _, ic := range parallel {
			wg.Add(1)
			go func(ic indexedCall) {
				defer wg.Done()
				select {
				case <-ctx.Done():
					e.notify(ToolExecutionUpdate{
						ToolCallID: ic.call.ID,
						ToolName: ic.call.Name,
						Status: "failed",
						Error: ctx.Err(),
					})
					results[ic.idx] = &ToolExecutionResult{
						Call: ic.call,
						Error: ctx.Err(),
					}
					return
				case sem <- struct{}{}:
				}
				defer func() { <-sem }()
				results[ic.idx] = e.executeOne(ctx, ic.call, reg)
			}(ic)
		}
		wg.Wait()
	}

	// 串行执行非 ParallelSafe 的调用
	for _, ic := range sequential {
		select {
		case <-ctx.Done():
			e.notify(ToolExecutionUpdate{
				ToolCallID: ic.call.ID,
				ToolName: ic.call.Name,
				Status: "failed",
				Error: ctx.Err(),
			})
			results[ic.idx] = &ToolExecutionResult{
				Call: ic.call,
				Error: ctx.Err(),
			}
			return results
		default:
		}
		results[ic.idx] = e.executeOne(ctx, ic.call, reg)
	}

	return results
}

// executeOne 执行单个工具调用。
func (e *ParallelToolExecutor) executeOne(ctx context.Context, call ToolCall, reg ToolRegistry) *ToolExecutionResult {
	select {
	case <-ctx.Done():
		e.notify(ToolExecutionUpdate{
			ToolCallID: call.ID,
			ToolName: call.Name,
			Status: "failed",
			Error: ctx.Err(),
		})
		return &ToolExecutionResult{
			Call: call,
			Error: ctx.Err(),
		}
	default:
	}

	// 通知工具执行开始
	e.notify(ToolExecutionUpdate{
		ToolCallID: call.ID,
		ToolName: call.Name,
		Status: "started",
	})

	tool, err := reg.GetTool(ctx, call.Name)
	if err != nil {
		e.notify(ToolExecutionUpdate{
			ToolCallID: call.ID,
			ToolName: call.Name,
			Status: "failed",
			Error: err,
		})
		return &ToolExecutionResult{
			Call: call,
			Error: err,
		}
	}

	result, err := tool.Handler(ctx, call.Arguments)
	if err != nil {
		e.notify(ToolExecutionUpdate{
			ToolCallID: call.ID,
			ToolName: call.Name,
			Status: "failed",
			Error: err,
		})
		return &ToolExecutionResult{
			Call: call,
			Error: err,
		}
	}

	e.notify(ToolExecutionUpdate{
		ToolCallID: call.ID,
		ToolName: call.Name,
		Status: "completed",
		Progress: 1.0, // completed means full progress
	})
	return &ToolExecutionResult{
		Call: call,
		Result: result,
	}
}

// notify 安全地发送工具执行通知。
func (e *ParallelToolExecutor) notify(update ToolExecutionUpdate) {
	if e.notifier != nil {
		e.notifier.NotifyToolExecution(update)
	}
}
