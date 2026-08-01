// Package orchestrate 定义多 Agent 编排接口与类型。
//
// builder.go 提供 OrchestrationPlan 的流式构建器。
package orchestrate

import (
	"context"

	"github.com/pengjunchen/go-agent-core/agent/loop"
)

// ─── PlanBuilder ──────────────────────────────────────────────────

// PlanBuilder 是 OrchestrationPlan 的流式构建器。
//
// 提供链式 API 以简化编排计划组装：
//
//	plan := NewPlan().
//	 Sequential().
//	 Step("research", researchAgent).BuildStep().
//	 Step("write", writeAgent).BuildStep().
//	 MaxConcurrency(3).
//	 Build()
type PlanBuilder struct {
	mode OrchestrationMode
	steps []AgentStep
	maxConcurrency int
	onStepError func(step *AgentStep, err error) ErrorAction
	err error
}

// NewPlan 创建一个新的 PlanBuilder。
func NewPlan() *PlanBuilder {
	return &PlanBuilder{
		mode: ModeSequential,
	}
}

// Sequential 设置为顺序模式。
func (b *PlanBuilder) Sequential() *PlanBuilder {
	b.mode = ModeSequential
	return b
}

// Parallel 设置为并行模式。
func (b *PlanBuilder) Parallel() *PlanBuilder {
	b.mode = ModeParallel
	return b
}

// Conditional 设置为条件模式。
func (b *PlanBuilder) Conditional() *PlanBuilder {
	b.mode = ModeConditional
	return b
}

// Step 开始定义一个步骤，返回 StepBuilder。
func (b *PlanBuilder) Step(name string, agent loop.LoopAgent) *StepBuilder {
	return &StepBuilder{
		parent: b,
		step: AgentStep{
			Name: name,
			Agent: agent,
		},
	}
}

// MaxConcurrency 设置并行模式的最大并发数。
func (b *PlanBuilder) MaxConcurrency(n int) *PlanBuilder {
	b.maxConcurrency = n
	return b
}

// OnError 设置步骤出错时的默认动作。
func (b *PlanBuilder) OnError(action ErrorAction) *PlanBuilder {
	b.onStepError = func(_ *AgentStep, _ error) ErrorAction {
		return action
	}
	return b
}

// OnErrorFunc 设置步骤出错时的回调函数。
func (b *PlanBuilder) OnErrorFunc(fn func(step *AgentStep, err error) ErrorAction) *PlanBuilder {
	b.onStepError = fn
	return b
}

// Build 构建 OrchestrationPlan。
func (b *PlanBuilder) Build() *OrchestrationPlan {
	if b.err != nil {
		return nil
	}

	return &OrchestrationPlan{
		Mode: b.mode,
		Steps: b.steps,
		MaxConcurrency: b.maxConcurrency,
		OnStepError: b.onStepError,
	}
}

// ─── StepBuilder ──────────────────────────────────────────────────

// StepBuilder 是单个步骤的流式构建器。
type StepBuilder struct {
	parent *PlanBuilder
	step AgentStep
}

// WithCondition 设置步骤的条件函数（条件模式下使用）。
func (sb *StepBuilder) WithCondition(fn func(ctx context.Context, state *OrchestrationState) bool) *StepBuilder {
	sb.step.Condition = fn
	return sb
}

// WithInputTransform 设置步骤的输入转换函数。
func (sb *StepBuilder) WithInputTransform(fn func(ctx context.Context, state *OrchestrationState) string) *StepBuilder {
	sb.step.InputTransform = fn
	return sb
}

// BuildStep 完成步骤定义，将步骤添加到 PlanBuilder 并返回 PlanBuilder。
func (sb *StepBuilder) BuildStep() *PlanBuilder {
	sb.parent.steps = append(sb.parent.steps, sb.step)
	return sb.parent
}
