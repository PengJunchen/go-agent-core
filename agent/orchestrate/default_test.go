// Package orchestrate 定义多 Agent 编排接口与类型。
//
// default_test.go 包含 DefaultOrchestrator 的测试，覆盖：
// - 顺序执行：步骤按序运行，状态在步骤间传递
// - 并行执行：步骤并发运行，结果汇总收集
// - 条件执行：仅匹配条件的步骤执行
// - 错误停止：出错即停止编排
// - 错误继续：出错跳过当前步骤，继续后续步骤
// - 错误重试：出错重试当前步骤
// - 共享状态：步骤间可读写共享数据
// - 并发安全：并行编排无数据竞争
// - 上下文取消：编排可被外部取消
package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
)

// ─── Mock LoopAgent ──────────────────────────────────────────────

// mockAgent 是用于测试的 LoopAgent 实现。
//
// 它在 Query 时返回预定义的事件序列，并记录调用顺序。
type mockAgent struct {
	mu sync.Mutex
	responses []mockResponse
	callCount int
	callOrder []string // 记录被调用的 prompt
}

type mockResponse struct {
	prompt string // 如果非空，仅在 prompt 匹配时使用此响应
	events []event.AgentEvent
	queryFn func(ctx context.Context, input loop.AgentInput) (<-chan event.AgentEvent, error) // 自定义 Query 逻辑
	err error // Query 直接返回的错误
}

func newMockAgent(responses ...mockResponse) *mockAgent {
	return &mockAgent{
		responses: responses,
	}
}

// simpleMockAgent 创建一个返回文本事件的简单 mock agent。
func simpleMockAgent(name, text string) *mockAgent {
	return newMockAgent(mockResponse{
		events: []event.AgentEvent{
			{Type: event.EventTurnStart, Payload: text},
			{Type: event.EventTextDelta, Payload: text},
			{Type: event.EventTurnEnd},
			{Type: event.EventCompleted},
		},
	})
}

func (m *mockAgent) Query(ctx context.Context, input loop.AgentInput) (<-chan event.AgentEvent, error) {
	m.mu.Lock()
	_ = m.callCount
	m.callCount++
	m.callOrder = append(m.callOrder, input.Prompt)
	m.mu.Unlock()

	// 查找匹配的响应
	m.mu.Lock()
	var resp *mockResponse
	for i := range m.responses {
		// 如果有自定义 Query 函数，直接使用
		if m.responses[i].queryFn != nil {
			resp = &m.responses[i]
			break
		}
		// 如果 prompt 匹配或 prompt 为空，使用此响应
		if m.responses[i].prompt == "" || m.responses[i].prompt == input.Prompt {
			resp = &m.responses[i]
			break
		}
	}
	m.mu.Unlock()

	// 如果有自定义 Query 函数
	if resp != nil && resp.queryFn != nil {
		return resp.queryFn(ctx, input)
	}

	// 如果 Query 直接返回错误
	if resp != nil && resp.err != nil {
		return nil, resp.err
	}

	// 创建事件通道并发送预定义事件
	ch := make(chan event.AgentEvent, 64)
	go func() {
		defer close(ch)
		if resp != nil {
			for _, evt := range resp.events {
				select {
				case <-ctx.Done():
					return
				case ch <- evt:
				}
			}
		}
	}()

	return ch, nil
}

func (m *mockAgent) Interrupt(_ context.Context) error { return nil }
func (m *mockAgent) Steer(_ context.Context, _ string) error { return nil }
func (m *mockAgent) FollowUp(_ context.Context, _ string) error { return nil }
func (m *mockAgent) Status() event.AgentStatus { return event.StatusIdle }
func (m *mockAgent) Close() error { return nil }

// getCallCount 返回 Query 被调用的次数。
func (m *mockAgent) getCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// getCallOrder 返回 Query 被调用的 prompt 顺序。
func (m *mockAgent) getCallOrder() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.callOrder))
	copy(result, m.callOrder)
	return result
}

// ─── 顺序执行测试 ──────────────────────────────────────────────────

// TestSequentialExecution 测试顺序执行：步骤按序运行，状态在步骤间传递。
func TestSequentialExecution(t *testing.T) {
	agent1 := simpleMockAgent("step1", "result1")
	agent2 := simpleMockAgent("step2", "result2")
	agent3 := simpleMockAgent("step3", "result3")

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "step1", Agent: agent1},
			{Name: "step2", Agent: agent2},
			{Name: "step3", Agent: agent3},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	// 验证所有步骤都有结果
	for _, name := range []string{"step1", "step2", "step3"} {
		sr, ok := result.State.GetResult(name)
		if !ok {
			t.Errorf("missing result for step %q", name)
			continue
		}
		if sr.Error != nil {
			t.Errorf("step %q error: %v", name, sr.Error)
		}
		if sr.Output != "result"+name[4:] {
			t.Errorf("step %q output = %q, want %q", name, sr.Output, "result"+name[4:])
		}
	}

	// 验证每个 agent 被调用一次
	for _, a := range []*mockAgent{agent1, agent2, agent3} {
		if a.getCallCount() != 1 {
			t.Errorf("agent call count = %d, want 1", a.getCallCount())
		}
	}
}

// TestSequentialStatePassing 测试顺序执行中状态传递。
func TestSequentialStatePassing(t *testing.T) {
	agent1 := simpleMockAgent("step1", "hello")
	agent2 := &mockAgent{
		responses: []mockResponse{
			{
				queryFn: func(_ context.Context, input loop.AgentInput) (<-chan event.AgentEvent, error) {
					ch := make(chan event.AgentEvent, 64)
					go func() {
						defer close(ch)
						ch <- event.AgentEvent{Type: event.EventTextDelta, Payload: "got: " + input.Prompt}
						ch <- event.AgentEvent{Type: event.EventCompleted}
					}()
					return ch, nil
				},
			},
		},
	}

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "step1", Agent: agent1},
			{
				Name: "step2",
				Agent: agent2,
				InputTransform: func(_ context.Context, state *OrchestrationState) string {
					r, _ := state.GetResult("step1")
					if r != nil {
						return r.Output
					}
					return ""
				},
			},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	sr, ok := result.State.GetResult("step2")
	if !ok {
		t.Fatal("missing result for step2")
	}
	if sr.Output != "got: hello" {
		t.Errorf("step2 output = %q, want %q", sr.Output, "got: hello")
	}
}

// ─── 并行执行测试 ──────────────────────────────────────────────────

// TestParallelExecution 测试并行执行：步骤并发运行，结果汇总收集。
func TestParallelExecution(t *testing.T) {
	agent1 := simpleMockAgent("step1", "parallel1")
	agent2 := simpleMockAgent("step2", "parallel2")
	agent3 := simpleMockAgent("step3", "parallel3")

	plan := &OrchestrationPlan{
		Mode: ModeParallel,
		Steps: []AgentStep{
			{Name: "step1", Agent: agent1},
			{Name: "step2", Agent: agent2},
			{Name: "step3", Agent: agent3},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	// 验证所有步骤都有结果
	for _, name := range []string{"step1", "step2", "step3"} {
		sr, ok := result.State.GetResult(name)
		if !ok {
			t.Errorf("missing result for step %q", name)
			continue
		}
		if sr.Error != nil {
			t.Errorf("step %q error: %v", name, sr.Error)
		}
	}
}

// TestParallelWithConcurrencyLimit 测试并行模式的并发限制。
func TestParallelWithConcurrencyLimit(t *testing.T) {
	var activeCount atomic.Int32
	var maxActive atomic.Int32

	makeAgent := func(name string) *mockAgent {
		return newMockAgent(mockResponse{
			queryFn: func(_ context.Context, _ loop.AgentInput) (<-chan event.AgentEvent, error) {
				current := activeCount.Add(1)
				// 记录最大并发数
				for {
					old := maxActive.Load()
					if current <= old || maxActive.CompareAndSwap(old, current) {
						break
					}
				}

				ch := make(chan event.AgentEvent, 64)
				go func() {
					defer close(ch)
					time.Sleep(50 * time.Millisecond) // 模拟工作
					ch <- event.AgentEvent{Type: event.EventTextDelta, Payload: name}
					ch <- event.AgentEvent{Type: event.EventCompleted}
					activeCount.Add(-1)
				}()
				return ch, nil
			},
		})
	}

	plan := &OrchestrationPlan{
		Mode: ModeParallel,
		MaxConcurrency: 2,
		Steps: []AgentStep{
			{Name: "a1", Agent: makeAgent("a1")},
			{Name: "a2", Agent: makeAgent("a2")},
			{Name: "a3", Agent: makeAgent("a3")},
			{Name: "a4", Agent: makeAgent("a4")},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	// 验证最大并发数不超过 2
	if maxActive.Load() > 2 {
		t.Errorf("max concurrent = %d, want <= 2", maxActive.Load())
	}
}

// ─── 条件执行测试 ──────────────────────────────────────────────────

// TestConditionalExecution 测试条件执行：仅匹配条件的步骤执行。
func TestConditionalExecution(t *testing.T) {
	agent1 := simpleMockAgent("step1", "conditional1")
	agent2 := simpleMockAgent("step2", "conditional2")
	agent3 := simpleMockAgent("step3", "conditional3")

	plan := &OrchestrationPlan{
		Mode: ModeConditional,
		Steps: []AgentStep{
			{Name: "step1", Agent: agent1, Condition: func(_ context.Context, _ *OrchestrationState) bool { return true }},
			{Name: "step2", Agent: agent2, Condition: func(_ context.Context, _ *OrchestrationState) bool { return false }},
			{Name: "step3", Agent: agent3, Condition: func(_ context.Context, _ *OrchestrationState) bool { return true }},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	// step1 和 step3 应该有结果
	if _, ok := result.State.GetResult("step1"); !ok {
		t.Error("missing result for step1")
	}
	if _, ok := result.State.GetResult("step3"); !ok {
		t.Error("missing result for step3")
	}

	// step2 不应该有结果（条件不满足）
	if _, ok := result.State.GetResult("step2"); ok {
		t.Error("unexpected result for step2 (condition was false)")
	}

	// step2 的 agent 不应该被调用
	if agent2.getCallCount() != 0 {
		t.Errorf("step2 agent call count = %d, want 0", agent2.getCallCount())
	}
}

// ─── 错误处理测试 ──────────────────────────────────────────────────

// TestErrorStop 测试错误停止：出错即停止编排。
func TestErrorStop(t *testing.T) {
	errAgent := newMockAgent(mockResponse{
		err: errors.New("agent error"),
	})
	okAgent := simpleMockAgent("step2", "should not run")

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "failing", Agent: errAgent},
			{Name: "ok", Agent: okAgent},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationPartial {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationPartial)
	}

	if result.Error == nil {
		t.Error("expected error in result")
	}

	// 第二个 agent 不应该被调用
	if okAgent.getCallCount() != 0 {
		t.Errorf("ok agent call count = %d, want 0", okAgent.getCallCount())
	}
}

// TestErrorContinue 测试错误继续：出错跳过当前步骤，继续后续步骤。
func TestErrorContinue(t *testing.T) {
	errAgent := newMockAgent(mockResponse{
		err: errors.New("agent error"),
	})
	okAgent := simpleMockAgent("step2", "continued")

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "failing", Agent: errAgent},
			{Name: "ok", Agent: okAgent},
		},
		OnStepError: func(_ *AgentStep, _ error) ErrorAction {
			return ErrorActionContinue
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// 部分步骤失败
	if result.Status != OrchestrationPartial {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationPartial)
	}

	// 第二个 agent 应该被调用
	if okAgent.getCallCount() != 1 {
		t.Errorf("ok agent call count = %d, want 1", okAgent.getCallCount())
	}

	// step2 应该有成功结果
	sr, ok := result.State.GetResult("ok")
	if !ok {
		t.Fatal("missing result for ok step")
	}
	if sr.Output != "continued" {
		t.Errorf("ok step output = %q, want %q", sr.Output, "continued")
	}
}

// TestErrorRetry 测试错误重试：出错重试当前步骤。
func TestErrorRetry(t *testing.T) {
	var callCount atomic.Int32
	retryAgent := newMockAgent(mockResponse{
		queryFn: func(_ context.Context, _ loop.AgentInput) (<-chan event.AgentEvent, error) {
			count := callCount.Add(1)
			if count < 3 {
				return nil, errors.New("transient error")
			}
			ch := make(chan event.AgentEvent, 64)
			go func() {
				defer close(ch)
				ch <- event.AgentEvent{Type: event.EventTextDelta, Payload: "success after retry"}
				ch <- event.AgentEvent{Type: event.EventCompleted}
			}()
			return ch, nil
		},
	})

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "retry_step", Agent: retryAgent},
		},
		OnStepError: func(_ *AgentStep, _ error) ErrorAction {
			return ErrorActionRetry
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	sr, ok := result.State.GetResult("retry_step")
	if !ok {
		t.Fatal("missing result for retry_step")
	}
	if sr.Output != "success after retry" {
		t.Errorf("retry step output = %q, want %q", sr.Output, "success after retry")
	}
}

// ─── 共享状态测试 ──────────────────────────────────────────────────

// TestSharedState 测试步骤间共享状态读写。
func TestSharedState(t *testing.T) {
	agent1 := simpleMockAgent("step1", "data1")
	agent2 := &mockAgent{
		responses: []mockResponse{
			{
				queryFn: func(_ context.Context, input loop.AgentInput) (<-chan event.AgentEvent, error) {
					ch := make(chan event.AgentEvent, 64)
					go func() {
						defer close(ch)
						ch <- event.AgentEvent{Type: event.EventTextDelta, Payload: "got: " + input.Prompt}
						ch <- event.AgentEvent{Type: event.EventCompleted}
					}()
					return ch, nil
				},
			},
		},
	}

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{
				Name: "producer",
				Agent: agent1,
				InputTransform: func(_ context.Context, state *OrchestrationState) string {
					state.SetShared("key1", "value1")
					return "produce"
				},
			},
			{
				Name: "consumer",
				Agent: agent2,
				InputTransform: func(_ context.Context, state *OrchestrationState) string {
					v, _ := state.GetShared("key1")
					return fmt.Sprintf("shared:%v", v)
				},
			},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	sr, ok := result.State.GetResult("consumer")
	if !ok {
		t.Fatal("missing result for consumer")
	}
	if sr.Output != "got: shared:value1" {
		t.Errorf("consumer output = %q, want %q", sr.Output, "got: shared:value1")
	}
}

// ─── 并发安全测试 ──────────────────────────────────────────────────

// TestConcurrentSafety 测试并行编排无数据竞争。
func TestConcurrentSafety(t *testing.T) {
	const numSteps = 10

	var steps []AgentStep
	for i := 0; i < numSteps; i++ {
		name := fmt.Sprintf("step%d", i)
		steps = append(steps, AgentStep{
			Name: name,
			Agent: simpleMockAgent(name, fmt.Sprintf("result%d", i)),
			InputTransform: func(_ context.Context, state *OrchestrationState) string {
				// 并发读写共享状态
				state.SetShared("writer", time.Now().UnixNano())
				_, _ = state.GetShared("writer")
				return ""
			},
		})
	}

	plan := &OrchestrationPlan{
		Mode: ModeParallel,
		Steps: steps,
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCompleted {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCompleted)
	}

	// 验证所有步骤都有结果
	for i := 0; i < numSteps; i++ {
		name := fmt.Sprintf("step%d", i)
		if _, ok := result.State.GetResult(name); !ok {
			t.Errorf("missing result for %q", name)
		}
	}
}

// ─── 上下文取消测试 ────────────────────────────────────────────────

// TestContextCancellation 测试编排可被外部取消。
func TestContextCancellation(t *testing.T) {
	slowAgent := newMockAgent(mockResponse{
		queryFn: func(ctx context.Context, _ loop.AgentInput) (<-chan event.AgentEvent, error) {
			ch := make(chan event.AgentEvent, 64)
			go func() {
				defer close(ch)
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
					ch <- event.AgentEvent{Type: event.EventCompleted}
				}
			}()
			return ch, nil
		},
	})

	okAgent := simpleMockAgent("step2", "should not run")

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "slow", Agent: slowAgent},
			{Name: "ok", Agent: okAgent},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(ctx, plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.Status != OrchestrationCancelled {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationCancelled)
	}
}

// ─── 计划校验测试 ──────────────────────────────────────────────────

// TestNilPlan 测试 nil 计划。
func TestNilPlan(t *testing.T) {
	orchestrator := NewDefaultOrchestrator()
	_, err := orchestrator.Execute(context.Background(), nil)
	if !errors.Is(err, ErrNilPlan) {
		t.Errorf("error = %v, want ErrNilPlan", err)
	}
}

// TestNoSteps 测试空步骤计划。
func TestNoSteps(t *testing.T) {
	plan := &OrchestrationPlan{Mode: ModeSequential}
	orchestrator := NewDefaultOrchestrator()
	_, err := orchestrator.Execute(context.Background(), plan)
	if !errors.Is(err, ErrNoSteps) {
		t.Errorf("error = %v, want ErrNoSteps", err)
	}
}

// ─── 接口合规测试 ──────────────────────────────────────────────────

// TestInterfaceCompliance 编译时校验 DefaultOrchestrator 实现了 Orchestrator。
func TestInterfaceCompliance(t *testing.T) {
	var _ Orchestrator = (*DefaultOrchestrator)(nil)
}

// ─── EventError 路径测试 ──────────────────────────────────────────

// TestEventErrorInStream 测试步骤执行中事件流出现 EventError。
func TestEventErrorInStream(t *testing.T) {
	errEventAgent := newMockAgent(mockResponse{
		events: []event.AgentEvent{
			{Type: event.EventTurnStart},
			{Type: event.EventError, Error: errors.New("stream error")},
		},
	})

	okAgent := simpleMockAgent("step2", "ok")

	plan := &OrchestrationPlan{
		Mode: ModeSequential,
		Steps: []AgentStep{
			{Name: "err_step", Agent: errEventAgent},
			{Name: "ok_step", Agent: okAgent},
		},
	}

	orchestrator := NewDefaultOrchestrator()
	result, err := orchestrator.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// 默认 ErrorActionStop，应返回 partial
	if result.Status != OrchestrationPartial {
		t.Errorf("status = %v, want %v", result.Status, OrchestrationPartial)
	}

	// ok_step 不应该被执行
	if okAgent.getCallCount() != 0 {
		t.Errorf("ok agent call count = %d, want 0", okAgent.getCallCount())
	}
}
