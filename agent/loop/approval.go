// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// approval.go 提供 Human-in-the-Loop (HITL) 审批系统：
// - HITLManager 管理审批工作流（请求审批、缓存决策）
// - ApprovalHook 集成 ToolHook 管道，在工具执行前请求审批
// - ApprovalHandler/ApprovalHandlerFunc 定义审批处理接口
//
//
// 审批决策链：缓存查找 → ApprovalHandler 请求 → 缓存写入。
package loop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
)

// ─── 审批决策类型 ────────────────────────────────────────────────

// ApprovalDecision 表示审批请求的结果。
type ApprovalDecision int

const (
	// ApprovalApprove 批准执行。
	ApprovalApprove ApprovalDecision = iota
	// ApprovalDeny 拒绝执行。
	ApprovalDeny
	// ApprovalTimeout 审批超时。
	ApprovalTimeout
)

// ─── 审批请求与响应 ──────────────────────────────────────────────

// ApprovalRequest 表示一次审批请求。
type ApprovalRequest struct {
	ToolCallID string
	ToolName string
	Arguments map[string]any
	SessionID string
	TurnID string
	Reason string
}

// ApprovalResponse 表示审批请求的响应。
type ApprovalResponse struct {
	RequestID string
	Decision ApprovalDecision
	Reason string
}

// ─── 审批处理接口 ────────────────────────────────────────────────

// ApprovalHandler 处理审批请求。
type ApprovalHandler interface {
	// RequestApproval 请求审批，阻塞直到决策做出或上下文取消。
	RequestApproval(ctx context.Context, req *ApprovalRequest) (ApprovalDecision, error)
}

// ApprovalHandlerFunc 是 ApprovalHandler 的函数适配器。
type ApprovalHandlerFunc func(ctx context.Context, req *ApprovalRequest) (ApprovalDecision, error)

// RequestApproval 实现 ApprovalHandler 接口。
func (f ApprovalHandlerFunc) RequestApproval(ctx context.Context, req *ApprovalRequest) (ApprovalDecision, error) {
	return f(ctx, req)
}

// ─── HITLManager ─────────────────────────────────────────────────

// HITLManager 管理 Human-in-the-Loop 审批工作流。
//
// 职责：
// - 委托 ApprovalHandler 执行审批（带超时）
// - 缓存已审批工具的决策（会话级别）
// - 支持缓存查找（快速跳过已审批的工具）
type HITLManager struct {
	mu sync.RWMutex
	handler ApprovalHandler
	timeout time.Duration
	// Decision cache: toolName -> ApprovalDecision (session-level)
	approved map[string]ApprovalDecision
}

// NewHITLManager 创建一个 HITLManager。
//
// handler: 审批处理实现（必填）
// timeout: 审批超时时间（0 表示不超时）
func NewHITLManager(handler ApprovalHandler, timeout time.Duration) *HITLManager {
	return &HITLManager{
		handler: handler,
		timeout: timeout,
		approved: make(map[string]ApprovalDecision),
	}
}

// RequestApproval 委托给 handler 执行审批（带超时）。
//
// 如果设置了超时时间，则在超时后返回 ApprovalTimeout。
func (m *HITLManager) RequestApproval(ctx context.Context, req *ApprovalRequest) (ApprovalDecision, error) {
	if m.handler == nil {
		return ApprovalDeny, fmt.Errorf("no approval handler configured")
	}

	if m.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.timeout)
		defer cancel()
	}

	decision, err := m.handler.RequestApproval(ctx, req)
	if err != nil {
		if ctx.Err() != nil {
			return ApprovalTimeout, ctx.Err()
		}
		return ApprovalDeny, err
	}

	return decision, nil
}

// IsApproved 从缓存查找工具的审批决策。
//
// 返回决策值和是否命中缓存。
func (m *HITLManager) IsApproved(toolName string) (ApprovalDecision, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	decision, ok := m.approved[toolName]
	return decision, ok
}

// CacheDecision 缓存工具的审批决策。
func (m *HITLManager) CacheDecision(toolName string, decision ApprovalDecision) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.approved[toolName] = decision
}

// ClearCache 清空审批缓存。
func (m *HITLManager) ClearCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.approved = make(map[string]ApprovalDecision)
}

// ─── ApprovalHook ────────────────────────────────────────────────

// Compile-time check that ApprovalHook implements ToolHook.
var _ toolhook.ToolHook = (*ApprovalHook)(nil)

// ApprovalHook 是一个 ToolHook，在工具执行前请求审批。
//
// 决策链：
// 1. 缓存查找（IsApproved）→ 命中则直接返回
// 2. 发射 EventApprovalRequest 事件
// 3. 调用 HITLManager.RequestApproval
// 4. 缓存决策（CacheDecision）
// 5. 根据 Decision 返回 Block 或放行
type ApprovalHook struct {
	hitl *HITLManager
	eventCh chan<- event.AgentEvent
	submissionID string
	sessionID string
	turnID string

	// OnSuspend is called before blocking for approval (Running → WaitingApproval).
	OnSuspend func()
	// OnResume is called after the approval decision is made (WaitingApproval → Running).
	OnResume func()
}

// NewApprovalHook 创建一个 ApprovalHook。
//
// hitl: HITL 管理器（必填）
// eventCh: 事件通道，用于发射 EventApprovalRequest
// submissionID, sessionID, turnID: 事件标识
func NewApprovalHook(hitl *HITLManager, eventCh chan<- event.AgentEvent, submissionID, sessionID, turnID string) *ApprovalHook {
	return &ApprovalHook{
		hitl: hitl,
		eventCh: eventCh,
		submissionID: submissionID,
		sessionID: sessionID,
		turnID: turnID,
	}
}

// Before 实现 ToolHook.Before — 在工具执行前请求审批。
func (h *ApprovalHook) Before(ctx context.Context, call *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	// Step 1: 缓存查找
	if decision, ok := h.hitl.IsApproved(call.Name); ok {
		if decision == ApprovalApprove {
			return &toolhook.BeforeResult{Block: false}, nil
		}
		return &toolhook.BeforeResult{Block: true, Reason: "tool denied (cached)"}, nil
	}

	// Step 2: 发射 EventApprovalRequest 事件
	if h.eventCh != nil {
		select {
		case h.eventCh <- event.AgentEvent{
			Type: event.EventApprovalRequest,
			SubmissionID: h.submissionID,
			TurnID: h.turnID,
			SessionID: h.sessionID,
			Payload: &ApprovalRequest{
				ToolCallID: call.ID,
				ToolName: call.Name,
				Arguments: call.Arguments,
				SessionID: call.SessionID,
				TurnID: call.TurnID,
			},
			Timestamp: time.Now().UnixMilli(),
		}:
		default:
			// 通道满，丢弃事件
		}
	}

	// Step 3: 请求审批
	req := &ApprovalRequest{
		ToolCallID: call.ID,
		ToolName: call.Name,
		Arguments: call.Arguments,
		SessionID: call.SessionID,
		TurnID: call.TurnID,
	}

	// 通知 Agent 转入 WaitingApproval 状态
	if h.OnSuspend != nil {
		h.OnSuspend()
	}

	decision, err := h.hitl.RequestApproval(ctx, req)

	// 通知 Agent 恢复到 Running 状态
	if h.OnResume != nil {
		h.OnResume()
	}

	if err != nil {
		return &toolhook.BeforeResult{Block: true, Reason: fmt.Sprintf("approval error: %v", err)}, nil
	}

	// Step 4: 缓存决策
	h.hitl.CacheDecision(call.Name, decision)

	// Step 5: 根据 Decision 返回
	switch decision {
	case ApprovalApprove:
		return &toolhook.BeforeResult{Block: false}, nil
	case ApprovalTimeout:
		return &toolhook.BeforeResult{Block: true, Reason: "approval timed out"}, nil
	default: // ApprovalDeny and any unknown
		return &toolhook.BeforeResult{Block: true, Reason: "tool denied by human"}, nil
	}
}

// After 实现 ToolHook.After — 无操作。
func (h *ApprovalHook) After(_ context.Context, _ *toolhook.ToolCall, _ *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	return &toolhook.AfterResult{}, nil
}

// PrepareArguments 实现 ArgumentsPreparer — 审批钩子不修改参数。
func (h *ApprovalHook) PrepareArguments(_ context.Context, call *toolhook.ToolCall) (*toolhook.ToolCall, error) {
	return call, nil
}
