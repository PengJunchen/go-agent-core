// Package subagent 定义 SubAgent 及其事件代理机制。
//
// SubAgent 包装 LoopAgent 实现有监督的执行，将子 Agent 的事件
// 转发为 SubAgentEvent 供父 Agent 消费。支持 Run/Wait/Interrupt/
// Send/Close 生命周期和事件流代理。
package subagent

import (
	"context"
	"errors"
	"sync"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
)

// SubAgentEvent 包装 AgentEvent，附加 SubAgent 上下文。
type SubAgentEvent struct {
	AgentName string // 子 Agent 名称
	EventType event.EventType // 原始事件类型
	Payload any // 原始载荷
	Timestamp int64 // 原始时间戳
	Original event.AgentEvent // 完整原始事件
}

// SubAgentResult 子 Agent 执行结果。
type SubAgentResult struct {
	Name string
	Output string
	Error error
	Events []SubAgentEvent
}

// SubAgent 包装 LoopAgent 实现有监督的执行。
type SubAgent interface {
	// Name 返回子 Agent 名称。
	Name() string
	// Run 启动异步执行。
	Run(ctx context.Context, input string) error
	// Send 追加后续消息（委托给 FollowUp），并继续代理事件。
	Send(ctx context.Context, message string) error
	// Interrupt 中断当前执行。
	Interrupt(ctx context.Context) error
	// Wait 阻塞等待执行完成，返回结果。
	Wait(ctx context.Context) (*SubAgentResult, error)
	// Events 返回事件流通道。
	Events() <-chan SubAgentEvent
	// Close 释放资源。
	Close() error
}

// 错误定义
var (
	ErrSubAgentClosed = errors.New("sub-agent is closed")
	ErrSubAgentRunning = errors.New("sub-agent is already running")
	ErrSubAgentNotRun = errors.New("sub-agent has not been started")
)

// compile-time check
var _ SubAgent = (*DefaultSubAgent)(nil)

// DefaultSubAgent 是 SubAgent 的默认实现。
//
// 它包装一个 loop.LoopAgent，在 Run 时调用 Query 并消费事件通道，
// 将每个 AgentEvent 转换为 SubAgentEvent 转发到自身的 eventCh。
// Send 调用 FollowUp 并启动新的事件代理 goroutine。
type DefaultSubAgent struct {
	mu sync.Mutex
	name string
	agent loop.LoopAgent

	eventCh chan SubAgentEvent
	result *SubAgentResult
	done chan struct{}
	closed bool
	running bool
	wg sync.WaitGroup // tracks proxyEvents goroutines
}

// NewDefaultSubAgent 创建 DefaultSubAgent。
func NewDefaultSubAgent(name string, agent loop.LoopAgent) *DefaultSubAgent {
	return &DefaultSubAgent{
		name: name,
		agent: agent,
		eventCh: make(chan SubAgentEvent, 256),
		done: make(chan struct{}),
	}
}

// Name 返回子 Agent 名称。
func (s *DefaultSubAgent) Name() string {
	return s.name
}

// Run 启动异步执行。
//
// 调用 agent.Query，在 goroutine 中消费事件并转发到 SubAgentEvent 通道。
func (s *DefaultSubAgent) Run(ctx context.Context, input string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSubAgentClosed
	}
	if s.running {
		s.mu.Unlock()
		return ErrSubAgentRunning
	}
	s.running = true
	s.mu.Unlock()

	ch, err := s.agent.Query(ctx, loop.AgentInput{
		Prompt: input,
		SessionID: s.name,
	})
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return err
	}

	// 重置 done channel 和 result（在锁内写入，与 Wait/proxyEvents 同步）
	s.mu.Lock()
	s.done = make(chan struct{})
	s.result = nil
	s.mu.Unlock()

	s.wg.Add(1)
	go s.proxyEvents(ch)

	return nil
}

// Send 追加后续消息（委托给 FollowUp）。
//
// FollowUp 在 DefaultLoopAgent 内部触发新一轮查询，事件由 Run 启动的
// dispatch 循环消费。因此 Send 无需额外启动事件代理 goroutine。
func (s *DefaultSubAgent) Send(ctx context.Context, message string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSubAgentClosed
	}
	s.mu.Unlock()

	return s.agent.FollowUp(ctx, message)
}

// Interrupt 中断当前执行。
func (s *DefaultSubAgent) Interrupt(ctx context.Context) error {
	return s.agent.Interrupt(ctx)
}

// Wait 阻塞等待执行完成，返回结果。
func (s *DefaultSubAgent) Wait(ctx context.Context) (*SubAgentResult, error) {
	s.mu.Lock()
	if !s.running && s.result == nil && !s.closed {
		s.mu.Unlock()
		return nil, ErrSubAgentNotRun
	}
	done := s.done
	s.mu.Unlock()

	select {
	case <-done:
		s.mu.Lock()
		r := s.result
		s.mu.Unlock()
		return r, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Events 返回事件流通道。
func (s *DefaultSubAgent) Events() <-chan SubAgentEvent {
	return s.eventCh
}

// Close 释放资源。
func (s *DefaultSubAgent) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// 等待所有代理 goroutine 完成
	s.wg.Wait()

	close(s.eventCh)
	return s.agent.Close()
}

// proxyEvents 消费 Agent 事件通道，转发为 SubAgentEvent。
func (s *DefaultSubAgent) proxyEvents(ch <-chan event.AgentEvent) {
	var collected []SubAgentEvent
	var output string

	defer func() {
		s.mu.Lock()
		s.result = &SubAgentResult{
			Name: s.name,
			Output: output,
			Events: collected,
		}
		// 提取错误（如果有）
		for i := len(collected) - 1; i >= 0; i-- {
			if collected[i].EventType == event.EventError {
				s.result.Error = collected[i].Original.Error
				break
			}
		}
		// 通知等待者：close(done) 在锁内执行，防止与 Run 中创建新 done 竞争
		close(s.done)
		s.running = false
		s.mu.Unlock()

		s.wg.Done()
	}()

	for evt := range ch {
		sae := SubAgentEvent{
			AgentName: s.name,
			EventType: evt.Type,
			Payload: evt.Payload,
			Timestamp: evt.Timestamp,
			Original: evt,
		}

		// 累积文本输出
		if evt.Type == event.EventTextDelta {
			if text, ok := evt.Payload.(string); ok {
				output += text
			}
		}

		collected = append(collected, sae)

		// 非阻塞转发
		s.mu.Lock()
		eventCh := s.eventCh
		s.mu.Unlock()

		select {
		case eventCh <- sae:
		default:
			// 通道满时丢弃（与 DefaultLoopAgent 一致）
		}
	}
}
