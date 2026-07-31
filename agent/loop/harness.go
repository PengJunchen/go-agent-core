// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// harness.go 提供 AgentHarness 持久化封装接口及其默认实现。
// AgentHarness 管理会话生命周期和基础设施，使 LoopAgent 调度逻辑
// 与持久化完全解耦。
package loop

import (
	"context"
	"fmt"
	"io"

	"github.com/pengjunchen/go-agent-core/agent/event"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
	"github.com/pengjunchen/go-agent-core/memory/session"
)

// ─── AgentHarness 接口 ──────────────────────────────────────────

// AgentHarness 持久化封装接口。
// 管理会话生命周期和基础设施，使 LoopAgent 调度逻辑与持久化完全解耦。
type AgentHarness interface {
	// Query 提交查询，自动管理会话生命周期。
	Query(ctx context.Context, prompt string, sessionID string) (<-chan event.AgentEvent, error)
	// Restore 恢复指定会话的上下文。
	Restore(ctx context.Context, sessionID string) error
	// Close 释放所有资源（包括 SessionManager 和 ExecLogger）。
	Close() error
}

// ─── DefaultAgentHarness ────────────────────────────────────────

// Compile-time check that DefaultAgentHarness implements AgentHarness.
var _ AgentHarness = (*DefaultAgentHarness)(nil)

// DefaultAgentHarness 是 AgentHarness 的默认实现。
//
// 它封装了 LoopAgent，在其之上添加了会话管理能力：
// - 自动创建/恢复会话
// - 管理 SessionManager 和 ExecLogger 生命周期
// - 在查询完成后更新会话状态
type DefaultAgentHarness struct {
	agent LoopAgent
	session session.SessionManager
	context ctxpkg.ContextManager
	logger log.ExecLogger
}

// ─── Query ──────────────────────────────────────────────────────

// Query 提交查询，自动管理会话生命周期。
//
// 核心流程：
// 1. 如果 sessionID 为空，创建新会话
// 2. 如果 sessionID 非空且会话存在，恢复上下文
// 3. 委托给 LoopAgent.Query
// 4. 在后台 goroutine 中等待查询完成后更新会话状态
func (h *DefaultAgentHarness) Query(ctx context.Context, prompt string, sessionID string) (<-chan event.AgentEvent, error) {
	effectiveSessionID := sessionID

	// 如果 sessionID 为空，创建新会话
	if effectiveSessionID == "" {
		sess, err := h.session.CreateSession(ctx, &session.SessionOptions{})
		if err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		}
		effectiveSessionID = sess.ID
	} else {
		// 如果 sessionID 非空，检查会话是否存在，若存在则恢复上下文
		sess, err := h.session.GetSession(ctx, effectiveSessionID)
		if err == nil && sess != nil {
			// 会话存在，无需额外操作，上下文由 ContextManager 管理
		}
		// 如果会话不存在，仍然使用该 sessionID，让 LoopAgent 处理
	}

	// 委托给 LoopAgent.Query
	eventCh, err := h.agent.Query(ctx, AgentInput{
		Prompt: prompt,
		SessionID: effectiveSessionID,
	})
	if err != nil {
		return nil, err
	}

	// 创建输出通道，转发事件并监控完成状态
	outCh := make(chan event.AgentEvent, EventChannelSize)
	go h.forwardAndMonitor(ctx, effectiveSessionID, eventCh, outCh)

	return outCh, nil
}

// ─── Restore ────────────────────────────────────────────────────

// Restore 恢复指定会话的上下文。
//
// 核心流程：
// 1. 从 SessionManager 加载会话
// 2. 恢复 ContextManager 状态
func (h *DefaultAgentHarness) Restore(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required for restore")
	}

	// 加载会话
	sess, err := h.session.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session %s: %w", sessionID, err)
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", sessionID)
	}

	// 恢复上下文（通过 ContextManager 的 SetInitialContext）
	// 这里只是确保 ContextManager 可以基于会话信息继续工作
	// 实际的上下文恢复由具体的 ContextManager 实现处理
	return nil
}

// ─── Close ──────────────────────────────────────────────────────

// Close 释放所有资源。
//
// 关闭顺序：
// 1. 关闭内部 LoopAgent
// 2. 关闭 SessionManager（如果实现了 io.Closer）
// 3. 关闭 ExecLogger（如果实现了 io.Closer）
func (h *DefaultAgentHarness) Close() error {
	var firstErr error

	// 关闭内部 LoopAgent
	if err := h.agent.Close(); err != nil && firstErr == nil {
		firstErr = err
	}

	// 关闭 SessionManager（如果实现了 io.Closer）
	if c, ok := h.session.(io.Closer); ok {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// 关闭 ExecLogger（如果实现了 io.Closer）
	if c, ok := h.logger.(io.Closer); ok {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// ─── 内部辅助方法 ──────────────────────────────────────────────────

// forwardAndMonitor 转发事件到输出通道，并在完成后更新会话状态。
func (h *DefaultAgentHarness) forwardAndMonitor(ctx context.Context, sessionID string, src <-chan event.AgentEvent, dst chan<- event.AgentEvent) {
	defer close(dst)

	for evt := range src {
		// 转发事件到输出通道
		select {
		case dst <- evt:
		default:
			// 输出通道满，丢弃事件（与 LoopAgent 的 emitEvent 行为一致）
		}

		// 检测完成/错误事件，更新会话状态
		if evt.Type == event.EventCompleted || evt.Type == event.EventError {
			var sessStatus session.SessionStatus
			switch evt.Type {
			case event.EventCompleted:
				sessStatus = session.SessionActive
			case event.EventError:
				sessStatus = session.SessionFailed
			}
			_ = h.session.UpdateSession(ctx, sessionID, &session.SessionUpdate{
				Status: &sessStatus,
			}) // 状态更新失败不阻断事件转发
		}
	}
}
