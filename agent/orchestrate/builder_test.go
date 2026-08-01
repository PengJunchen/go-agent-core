// Package orchestrate 定义多 Agent 编排接口与类型。
//
// builder_test.go 包含 PlanBuilder 的测试，覆盖：
// - 流式 API 产生正确的计划
// - 各种模式的设置
// - 步骤的条件和输入转换
// - 错误策略设置
// - 并发数设置
package orchestrate

import (
	"context"
	"errors"
	"testing"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
)

// ─── Builder Mock Agent ──────────────────────────────────────────

// builderMockAgent 是 builder 测试用的简单 mock agent。
type builderMockAgent struct{}

func (m *builderMockAgent) Query(_ context.Context, _ loop.AgentInput) (<-chan event.AgentEvent, error) {
	ch := make(chan event.AgentEvent, 1)
	go func() {
		defer close(ch)
		ch <- event.AgentEvent{Type: event.EventCompleted}
	}()
	return ch, nil
}
func (m *builderMockAgent) Interrupt(_ context.Context) error { return nil }
func (m *builderMockAgent) Steer(_ context.Context, _ string) error { return nil }
func (m *builderMockAgent) FollowUp(_ context.Context, _ string) error { return nil }
func (m *builderMockAgent) Status() event.AgentStatus { return event.StatusIdle }
func (m *builderMockAgent) Close() error { return nil }

// ─── Builder 测试 ──────────────────────────────────────────────────

// TestBuilderSequential 测试顺序模式构建。
func TestBuilderSequential(t *testing.T) {
	agent := &builderMockAgent{}
	plan := NewPlan().
		Sequential().
		Step("s1", agent).BuildStep().
		Step("s2", agent).BuildStep().
		Build()

	if plan == nil {
		t.Fatal("plan is nil")
	}
	if plan.Mode != ModeSequential {
		t.Errorf("mode = %v, want %v", plan.Mode, ModeSequential)
	}
	if len(plan.Steps) != 2 {
		t.Errorf("steps count = %d, want 2", len(plan.Steps))
	}
	if plan.Steps[0].Name != "s1" {
		t.Errorf("step[0].Name = %q, want %q", plan.Steps[0].Name, "s1")
	}
	if plan.Steps[1].Name != "s2" {
		t.Errorf("step[1].Name = %q, want %q", plan.Steps[1].Name, "s2")
	}
}

// TestBuilderParallel 测试并行模式构建。
func TestBuilderParallel(t *testing.T) {
	agent := &builderMockAgent{}
	plan := NewPlan().
		Parallel().
		MaxConcurrency(3).
		Step("s1", agent).BuildStep().
		Build()

	if plan.Mode != ModeParallel {
		t.Errorf("mode = %v, want %v", plan.Mode, ModeParallel)
	}
	if plan.MaxConcurrency != 3 {
		t.Errorf("maxConcurrency = %d, want 3", plan.MaxConcurrency)
	}
}

// TestBuilderConditional 测试条件模式构建。
func TestBuilderConditional(t *testing.T) {
	agent := &builderMockAgent{}
	condFn := func(_ context.Context, _ *OrchestrationState) bool { return true }
	transformFn := func(_ context.Context, _ *OrchestrationState) string { return "input" }

	plan := NewPlan().
		Conditional().
		Step("s1", agent).
		WithCondition(condFn).
		WithInputTransform(transformFn).
		BuildStep().
		Build()

	if plan.Mode != ModeConditional {
		t.Errorf("mode = %v, want %v", plan.Mode, ModeConditional)
	}
	if plan.Steps[0].Condition == nil {
		t.Error("step condition is nil")
	}
	if plan.Steps[0].InputTransform == nil {
		t.Error("step input transform is nil")
	}
}

// TestBuilderOnError 测试错误策略设置。
func TestBuilderOnError(t *testing.T) {
	agent := &builderMockAgent{}
	plan := NewPlan().
		Sequential().
		Step("s1", agent).BuildStep().
		OnError(ErrorActionContinue).
		Build()

	if plan.OnStepError == nil {
		t.Fatal("OnStepError is nil")
	}
	action := plan.OnStepError(nil, errors.New("test"))
	if action != ErrorActionContinue {
		t.Errorf("error action = %v, want %v", action, ErrorActionContinue)
	}
}

// TestBuilderOnErrorFunc 测试自定义错误回调。
func TestBuilderOnErrorFunc(t *testing.T) {
	agent := &builderMockAgent{}
	plan := NewPlan().
		Sequential().
		Step("s1", agent).BuildStep().
		OnErrorFunc(func(step *AgentStep, err error) ErrorAction {
			if step.Name == "retry_step" {
				return ErrorActionRetry
			}
			return ErrorActionStop
		}).
		Build()

	if plan.OnStepError == nil {
		t.Fatal("OnStepError is nil")
	}

	// 测试 retry_step
	action := plan.OnStepError(&AgentStep{Name: "retry_step"}, errors.New("err"))
	if action != ErrorActionRetry {
		t.Errorf("action for retry_step = %v, want %v", action, ErrorActionRetry)
	}

	// 测试其他步骤
	action = plan.OnStepError(&AgentStep{Name: "other"}, errors.New("err"))
	if action != ErrorActionStop {
		t.Errorf("action for other = %v, want %v", action, ErrorActionStop)
	}
}

// TestBuilderDefaultMode 测试默认模式（Sequential）。
func TestBuilderDefaultMode(t *testing.T) {
	agent := &builderMockAgent{}
	plan := NewPlan().
		Step("s1", agent).BuildStep().
		Build()

	if plan.Mode != ModeSequential {
		t.Errorf("default mode = %v, want %v", plan.Mode, ModeSequential)
	}
}

// TestBuilderEmptyPlan 测试空步骤的计划。
func TestBuilderEmptyPlan(t *testing.T) {
	plan := NewPlan().Build()
	if plan == nil {
		t.Fatal("plan is nil")
	}
	if len(plan.Steps) != 0 {
		t.Errorf("steps count = %d, want 0", len(plan.Steps))
	}
}

// TestBuilderIntegration 测试 builder 生成的计划可以被编排器执行。
func TestBuilderIntegration(t *testing.T) {
	agent1 := simpleMockAgent("s1", "hello")
	agent2 := simpleMockAgent("s2", "world")

	plan := NewPlan().
		Sequential().
		Step("s1", agent1).BuildStep().
		Step("s2", agent2).
		WithInputTransform(func(_ context.Context, state *OrchestrationState) string {
			r, _ := state.GetResult("s1")
			if r != nil {
				return r.Output
			}
			return ""
		}).
		BuildStep().
		Build()

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	sr, ok := result.State.GetResult("s1")
	if !ok || sr.Output != "hello" {
		t.Errorf("s1 output = %q, want %q", sr.Output, "hello")
	}

	sr, ok = result.State.GetResult("s2")
	if !ok || sr.Output != "world" {
		t.Errorf("s2 output = %q, want %q", sr.Output, "world")
	}
}

// TestBuilderParallelIntegration 测试并行模式的 builder 集成。
func TestBuilderParallelIntegration(t *testing.T) {
	agent1 := simpleMockAgent("s1", "p1")
	agent2 := simpleMockAgent("s2", "p2")

	plan := NewPlan().
		Parallel().
		MaxConcurrency(2).
		Step("s1", agent1).BuildStep().
		Step("s2", agent2).BuildStep().
		Build()

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}
}

// TestBuilderConditionalIntegration 测试条件模式的 builder 集成。
func TestBuilderConditionalIntegration(t *testing.T) {
	agent1 := simpleMockAgent("s1", "c1")
	agent2 := simpleMockAgent("s2", "c2")

	plan := NewPlan().
		Conditional().
		Step("s1", agent1).
		WithCondition(func(_ context.Context, _ *OrchestrationState) bool { return true }).
		BuildStep().
		Step("s2", agent2).
		WithCondition(func(_ context.Context, _ *OrchestrationState) bool { return false }).
		BuildStep().
		Build()

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	// s1 应该有结果
	if _, ok := result.State.GetResult("s1"); !ok {
		t.Error("missing result for s1")
	}
	// s2 不应该有结果
	if _, ok := result.State.GetResult("s2"); ok {
		t.Error("unexpected result for s2 (condition was false)")
	}
}
