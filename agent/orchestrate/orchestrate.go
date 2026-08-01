// Package orchestrate 定义多 Agent 编排接口与类型。
//
// 提供顺序（Sequential）、并行（Parallel）、条件（Conditional）三种编排模式，
// 支持步骤间共享状态、错误策略和流式构建器。
// 编排器通过 loop.LoopAgent 接口驱动子 Agent，不依赖具体实现。
package orchestrate

import (
	"context"
	"sync"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
)

// ─── 编排模式 ─────────────────────────────────────────────────────

// OrchestrationMode 定义子 Agent 的执行方式。
type OrchestrationMode int

const (
	// ModeSequential 顺序执行：步骤逐一运行，前一步的结果可传递给后一步。
	ModeSequential OrchestrationMode = iota
	// ModeParallel 并行执行：所有步骤同时运行，结果汇总收集。
	ModeParallel
	// ModeConditional 条件执行：根据条件函数决定是否执行每个步骤。
	ModeConditional
)

// ─── 错误策略 ─────────────────────────────────────────────────────

// ErrorAction 定义步骤出错时的处理动作。
type ErrorAction int

const (
	// ErrorActionStop 出错即停止编排。
	ErrorActionStop ErrorAction = iota
	// ErrorActionContinue 出错跳过当前步骤，继续后续步骤。
	ErrorActionContinue
	// ErrorActionRetry 出错重试当前步骤。
	ErrorActionRetry
)

// ─── 编排状态 ─────────────────────────────────────────────────────

// OrchestrationState 是步骤间共享的可变状态。
//
// 通过 RWMutex 保证并发安全，并行模式下多个步骤可同时读写。
type OrchestrationState struct {
	mu sync.RWMutex
	Results map[string]*StepResult
	SharedData map[string]any
}

// NewOrchestrationState 创建空的编排状态。
func NewOrchestrationState() *OrchestrationState {
	return &OrchestrationState{
		Results: make(map[string]*StepResult),
		SharedData: make(map[string]any),
	}
}

// SetResult 写入步骤结果。
func (s *OrchestrationState) SetResult(name string, result *StepResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Results[name] = result
}

// GetResult 读取步骤结果。
func (s *OrchestrationState) GetResult(name string) (*StepResult, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.Results[name]
	return r, ok
}

// SetShared 写入共享数据。
func (s *OrchestrationState) SetShared(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SharedData[key] = value
}

// GetShared 读取共享数据。
func (s *OrchestrationState) GetShared(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.SharedData[key]
	return v, ok
}

// ─── 步骤结果 ─────────────────────────────────────────────────────

// StepResult 是单个步骤的执行结果。
type StepResult struct {
	StepName string
	Events []event.AgentEvent
	Error error
	Output string
}

// ─── 步骤定义 ─────────────────────────────────────────────────────

// AgentStep 定义编排中的一个步骤。
type AgentStep struct {
	Name string
	Agent loop.LoopAgent
	Condition func(ctx context.Context, state *OrchestrationState) bool
	InputTransform func(ctx context.Context, state *OrchestrationState) string
}

// ─── 编排计划 ─────────────────────────────────────────────────────

// OrchestrationPlan 定义编排执行计划。
type OrchestrationPlan struct {
	Mode OrchestrationMode
	Steps []AgentStep
	MaxConcurrency int
	OnStepError func(step *AgentStep, err error) ErrorAction
}

// ─── 编排结果 ─────────────────────────────────────────────────────

// OrchestrationStatus 编排最终状态。
type OrchestrationStatus string

const (
	// OrchestrationCompleted 所有步骤成功完成。
	OrchestrationCompleted OrchestrationStatus = "completed"
	// OrchestrationPartial 部分步骤失败。
	OrchestrationPartial OrchestrationStatus = "partial"
	// OrchestrationCancelled 编排被取消。
	OrchestrationCancelled OrchestrationStatus = "cancelled"
)

// OrchestrationResult 编排执行结果。
type OrchestrationResult struct {
	State *OrchestrationState
	Status OrchestrationStatus
	Error error
}

// ─── 编排器接口 ───────────────────────────────────────────────────

// Orchestrator 执行多 Agent 编排计划。
type Orchestrator interface {
	Execute(ctx context.Context, plan *OrchestrationPlan) (*OrchestrationResult, error)
}
