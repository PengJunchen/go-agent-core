// Package orchestrate 定义多 Agent 编排接口与类型。
//
// default.go 提供 DefaultOrchestrator 实现，支持顺序/并行/条件三种编排模式。
package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
)

// ─── 错误定义 ────────────────────────────────────────────────────

var (
	// ErrNilPlan 编排计划为 nil。
	ErrNilPlan = errors.New("orchestrate: plan is nil")
	// ErrNoSteps 编排计划没有步骤。
	ErrNoSteps = errors.New("orchestrate: plan has no steps")
	// ErrStepCancelled 步骤因上下文取消而中止。
	ErrStepCancelled = errors.New("orchestrate: step cancelled")
)

// ─── 常量 ────────────────────────────────────────────────────────

const (
	// DefaultMaxConcurrency 并行模式默认最大并发数。
	DefaultMaxConcurrency = 5
	// DefaultRetryCount 重试模式默认最大重试次数。
	DefaultRetryCount = 3
)

// ─── DefaultOrchestrator ─────────────────────────────────────────

// Compile-time check that DefaultOrchestrator implements Orchestrator.
var _ Orchestrator = (*DefaultOrchestrator)(nil)

// DefaultOrchestrator 是 Orchestrator 的默认实现。
type DefaultOrchestrator struct{}

// NewDefaultOrchestrator 创建默认编排器。
func NewDefaultOrchestrator() *DefaultOrchestrator {
	return &DefaultOrchestrator{}
}

// Execute 执行编排计划。
func (o *DefaultOrchestrator) Execute(ctx context.Context, plan *OrchestrationPlan) (*OrchestrationResult, error) {
	if err := validatePlan(plan); err != nil {
		return nil, err
	}

	state := NewOrchestrationState()

	switch plan.Mode {
	case ModeSequential:
		return o.executeSequential(ctx, plan, state)
	case ModeParallel:
		return o.executeParallel(ctx, plan, state)
	case ModeConditional:
		return o.executeConditional(ctx, plan, state)
	default:
		return nil, fmt.Errorf("orchestrate: unknown mode %d", plan.Mode)
	}
}

// ─── 顺序执行 ─────────────────────────────────────────────────────

func (o *DefaultOrchestrator) executeSequential(ctx context.Context, plan *OrchestrationPlan, state *OrchestrationState) (*OrchestrationResult, error) {
	for i := range plan.Steps {
		select {
		case <-ctx.Done():
			return &OrchestrationResult{
				State: state,
				Status: OrchestrationCancelled,
				Error: ctx.Err(),
			}, nil
		default:
		}

		step := &plan.Steps[i]
		result := o.runStep(ctx, step, state)
		state.SetResult(step.Name, result)

		if result.Error != nil {
			action := o.resolveErrorAction(plan, step, result.Error)
			switch action {
			case ErrorActionStop:
				return &OrchestrationResult{
					State: state,
					Status: OrchestrationPartial,
					Error: result.Error,
				}, nil
			case ErrorActionRetry:
				retryResult := o.retryStep(ctx, plan, step, state)
				state.SetResult(step.Name, retryResult)
				if retryResult.Error != nil {
					return &OrchestrationResult{
						State: state,
						Status: OrchestrationPartial,
						Error: retryResult.Error,
					}, nil
				}
			case ErrorActionContinue:
				// 继续执行下一步
			}
		}
	}

	return &OrchestrationResult{
		State: state,
		Status: o.resolveFinalStatus(state),
	}, nil
}

// ─── 并行执行 ─────────────────────────────────────────────────────

func (o *DefaultOrchestrator) executeParallel(ctx context.Context, plan *OrchestrationPlan, state *OrchestrationState) (*OrchestrationResult, error) {
	maxConcurrency := plan.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}

	// 使用信号量控制并发
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var hasError bool
	var mu sync.Mutex

	for i := range plan.Steps {
		// 获取信号量时同时监听上下文取消，避免阻塞时无法响应取消
		select {
		case sem <- struct{}{}:
			// 获取到信号量
		case <-ctx.Done():
			wg.Wait()
			return &OrchestrationResult{
				State: state,
				Status: OrchestrationCancelled,
				Error: ctx.Err(),
			}, nil
		}

		step := &plan.Steps[i]
		wg.Add(1)

		go func(s *AgentStep) {
			defer wg.Done()
			defer func() { <-sem }() // 释放信号量

			result := o.runStep(ctx, s, state)
			state.SetResult(s.Name, result)

			if result.Error != nil {
				mu.Lock()
				hasError = true
				mu.Unlock()
			}
		}(step)
	}

	wg.Wait()

	status := OrchestrationCompleted
	if hasError {
		status = o.resolveFinalStatus(state)
	}

	return &OrchestrationResult{
		State: state,
		Status: status,
	}, nil
}

// ─── 条件执行 ─────────────────────────────────────────────────────

func (o *DefaultOrchestrator) executeConditional(ctx context.Context, plan *OrchestrationPlan, state *OrchestrationState) (*OrchestrationResult, error) {
	for i := range plan.Steps {
		select {
		case <-ctx.Done():
			return &OrchestrationResult{
				State: state,
				Status: OrchestrationCancelled,
				Error: ctx.Err(),
			}, nil
		default:
		}

		step := &plan.Steps[i]

		// 检查条件
		if step.Condition != nil && !step.Condition(ctx, state) {
			continue
		}

		result := o.runStep(ctx, step, state)
		state.SetResult(step.Name, result)

		if result.Error != nil {
			action := o.resolveErrorAction(plan, step, result.Error)
			switch action {
			case ErrorActionStop:
				return &OrchestrationResult{
					State: state,
					Status: OrchestrationPartial,
					Error: result.Error,
				}, nil
			case ErrorActionRetry:
				retryResult := o.retryStep(ctx, plan, step, state)
				state.SetResult(step.Name, retryResult)
				if retryResult.Error != nil {
					return &OrchestrationResult{
						State: state,
						Status: OrchestrationPartial,
						Error: retryResult.Error,
					}, nil
				}
			case ErrorActionContinue:
				// 继续执行下一步
			}
		}
	}

	return &OrchestrationResult{
		State: state,
		Status: o.resolveFinalStatus(state),
	}, nil
}

// ─── 步骤执行 ─────────────────────────────────────────────────────

// runStep 执行单个步骤，收集事件和输出。
func (o *DefaultOrchestrator) runStep(ctx context.Context, step *AgentStep, state *OrchestrationState) *StepResult {
	// 确定输入
	input := ""
	if step.InputTransform != nil {
		input = step.InputTransform(ctx, state)
	}

	// 调用 Agent
	eventCh, err := step.Agent.Query(ctx, loop.AgentInput{
		Prompt: input,
	})
	if err != nil {
		return &StepResult{
			StepName: step.Name,
			Error: err,
		}
	}

	// 收集事件
	var events []event.AgentEvent
	var output string
	for evt := range eventCh {
		events = append(events, evt)
		if evt.Type == event.EventTextDelta {
			if text, ok := evt.Payload.(string); ok {
				output += text
			}
		}
		if evt.Type == event.EventError && evt.Error != nil {
			return &StepResult{
				StepName: step.Name,
				Events: events,
				Error: evt.Error,
				Output: output,
			}
		}
	}

	return &StepResult{
		StepName: step.Name,
		Events: events,
		Output: output,
	}
}

// retryStep 重试步骤执行。
func (o *DefaultOrchestrator) retryStep(ctx context.Context, plan *OrchestrationPlan, step *AgentStep, state *OrchestrationState) *StepResult {
	maxRetries := DefaultRetryCount
	var lastResult *StepResult
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return &StepResult{
				StepName: step.Name,
				Error: ErrStepCancelled,
			}
		default:
		}

		result := o.runStep(ctx, step, state)
		if result.Error == nil {
			return result
		}
		lastResult = result
	}
	// 重试耗尽，返回最后一次失败的结果
	return lastResult
}

// ─── 辅助方法 ─────────────────────────────────────────────────────

// resolveErrorAction 根据计划配置解析错误动作。
func (o *DefaultOrchestrator) resolveErrorAction(plan *OrchestrationPlan, step *AgentStep, err error) ErrorAction {
	if plan.OnStepError != nil {
		return plan.OnStepError(step, err)
	}
	return ErrorActionStop
}

// resolveFinalStatus 根据步骤结果判断最终状态。
func (o *DefaultOrchestrator) resolveFinalStatus(state *OrchestrationState) OrchestrationStatus {
	state.mu.RLock()
	defer state.mu.RUnlock()

	for _, r := range state.Results {
		if r != nil && r.Error != nil {
			return OrchestrationPartial
		}
	}
	return OrchestrationCompleted
}

// validatePlan 校验编排计划。
func validatePlan(plan *OrchestrationPlan) error {
	if plan == nil {
		return ErrNilPlan
	}
	if len(plan.Steps) == 0 {
		return ErrNoSteps
	}
	return nil
}
