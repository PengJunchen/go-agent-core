// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// DefaultLoopAgent 是 LoopAgent 的参考实现，实现完整的
// query → dispatch → turn → event 调度循环。
// 它保持 完整能力（串行 dispatch、Turn 管理、
// Interrupt/Steer/FollowUp 干预、状态机），同时通过 L4/L3/L2 接口
// 解耦实现可替换性。
package loop

import (
	"context"
	crypto_rand "crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/production"
)

// ─── 错误定义 ────────────────────────────────────────────────────

var (
	// ErrAgentNotIdle Agent 不在可启动的状态。
	ErrAgentNotIdle = errors.New("agent is not idle or completed")
	// ErrNoActiveTurn 没有活跃的 Turn。
	ErrNoActiveTurn = errors.New("no active turn to steer")
	// ErrAgentClosed Agent 已关闭。
	ErrAgentClosed = errors.New("agent is closed")
	// ErrInvalidTransition 非法状态转换。
	ErrInvalidTransition = errors.New("invalid status transition")
)

// ─── HTTPError ────────────────────────────────────────────────────

// HTTPError 表示一个 HTTP 错误，可用于重试判断。
type HTTPError struct {
	StatusCode int
	Message string
}

// Error 实现 error 接口。
func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
}

// ─── 重试配置 ─────────────────────────────────────────────────────

// RetryConfig 配置 StreamChat 调用的重试策略。
type RetryConfig struct {
	// MaxRetries 最大重试次数（默认 0，不重试）。
	MaxRetries int
	// RetryOnHTTP 需要重试的 HTTP 状态码（例如 [429]）。
	RetryOnHTTP []int
	// BaseDelay 首次重试的等待时间（默认 1s）。
	BaseDelay time.Duration
	// MaxDelay 最大等待时间（默认 30s）。
	MaxDelay time.Duration
}

// ─── 配置 ────────────────────────────────────────────────────────

const (
	// DefaultMaxTurns 默认最大轮次。
	DefaultMaxTurns = 20
	// EventChannelSize 事件通道缓冲大小。
	EventChannelSize = 256
	// SteerChannelSize Steer 通道缓冲大小。
	SteerChannelSize = 16
	// DefaultRetryBaseDelay 默认重试基础延迟。
	DefaultRetryBaseDelay = 1 * time.Second
	// DefaultRetryMaxDelay 默认重试最大延迟。
	DefaultRetryMaxDelay = 30 * time.Second
)

// LoopAgentConfig 是 DefaultLoopAgent 的配置。
type LoopAgentConfig struct {
	Provider provider.ModelProvider
	ContextManager ctxpkg.ContextManager
	ToolRegistry registry.ToolRegistry
	HookPipeline *toolhook.HookPipeline
	MiddlewareChain *middleware.Chain
	Logger log.ExecLogger // 可选，nil 表示不记录执行日志
	MaxTurns int // 默认 20
	RetryConfig *RetryConfig // 可选，nil 表示不重试
	CompactThreshold int // 触发自动压缩的 token 阈值（0 = 禁用）
	PrepareNextTurn PrepareNextTurnFunc // 可选，nil 表示不启用运行时 Provider 切换
	ProductionBundle *production.ProductionBundle // 可选，nil 表示不启用生产化组件
}

// ─── DefaultLoopAgent ────────────────────────────────────────────

// Compile-time check that DefaultLoopAgent implements LoopAgent.
var _ LoopAgent = (*DefaultLoopAgent)(nil)

// DefaultLoopAgent 是 LoopAgent 的默认实现。
//
// 它维护一个串行 Turn 循环：query → LLM 推理 → 工具调用 → 下一轮，
// 直到模型不再调用工具或达到 MaxTurns。
// 通过 Interrupt/Steer/FollowUp 支持外部干预。
//
// 核心调度逻辑委托给 LoopGenerator（无状态生成器），
// DefaultLoopAgent 自身负责状态机、steer 通道、提交跟踪和取消管理。
type DefaultLoopAgent struct {
	mu sync.Mutex
	wg sync.WaitGroup // tracks running goroutines

	// 配置（只读）
	provider provider.ModelProvider
	contextManager ctxpkg.ContextManager
	toolRegistry registry.ToolRegistry
	hookPipeline *toolhook.HookPipeline
	middlewareChain *middleware.Chain
	logger log.ExecLogger
	maxTurns int
	retryConfig *RetryConfig
	compactThreshold int
	prepareNextTurn PrepareNextTurnFunc
	productionBundle *production.ProductionBundle

	// 生成器（可替换）
	generator LoopGenerator

	// 运行时状态
	status event.AgentStatus
	cancelFunc context.CancelFunc
	steerCh chan string
	sessionID string
	submissionID string
	closed bool
}

// ─── Query ───────────────────────────────────────────────────────

// Query 提交查询，返回事件通道。
//
// 核心流程：
// 1. 校验状态（必须为 Idle 或 Completed）
// 2. 转换状态为 Running
// 3. 生成 submissionID
// 4. 启动 goroutine 执行 Turn 循环
// 5. 返回事件通道
func (a *DefaultLoopAgent) Query(ctx context.Context, input AgentInput) (<-chan event.AgentEvent, error) {
	a.mu.Lock()

	if a.closed {
		a.mu.Unlock()
		return nil, ErrAgentClosed
	}

	// 校验状态
	if a.status != event.StatusIdle && a.status != event.StatusCompleted {
		a.mu.Unlock()
		return nil, ErrAgentNotIdle
	}

	// 转换状态：Idle/Completed → Running
	if !event.CanTransition(a.status, event.StatusRunning) {
		a.mu.Unlock()
		return nil, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, a.status, event.StatusRunning)
	}
	a.status = event.StatusRunning

	// 记录状态变更日志
	if a.logger != nil {
		a.logger.LogItem(ctx, log.NewItemRecord("status_change", input.SessionID, ""))
	}

	// 生成 ID
	submissionID := generateID("sub")
	a.submissionID = submissionID
	a.sessionID = input.SessionID

	// 创建可取消的上下文
	ctx, cancel := context.WithCancel(ctx)
	a.cancelFunc = cancel

	// 创建 steer 通道
	a.steerCh = make(chan string, SteerChannelSize)

	a.mu.Unlock()

	// 创建事件通道
	eventCh := make(chan event.AgentEvent, EventChannelSize)

	// 启动 Turn 循环
	a.wg.Add(1)
	go a.runLoop(ctx, input, submissionID, eventCh)

	return eventCh, nil
}

// ─── runLoop ──────────────────────────────────────────────────────

// runLoop 是 Turn 循环的主逻辑，在独立 goroutine 中运行。
//
// P0 Fix 1: 使用 defer 保证 EventCompleted 始终在通道关闭前发送，
// 消费者可以区分"agent 终止"和"流中断"。
// P1 Fix 3: 在错误/中断/MaxTurns 路径上补充 EventTurnEnd。
//
// 核心调度逻辑委托给 LoopGenerator.RunTurn，
// DefaultLoopAgent 负责 EventTurnStart/EventTurnEnd/EventCompleted 的生命周期
// 和状态机转换。
func (a *DefaultLoopAgent) runLoop(ctx context.Context, input AgentInput, submissionID string, eventCh chan<- event.AgentEvent) {
	completed := false
	defer func() {
		a.wg.Done()
		if !completed {
			a.emitEvent(eventCh, event.AgentEvent{
				Type: event.EventCompleted,
				SubmissionID: submissionID,
				TurnID: "", // 可能在 turnID 生成之前就返回
				SessionID: a.sessionID,
				Timestamp: time.Now().UnixMilli(),
			})
		}
		close(eventCh)
	}()

	turnID := generateID("turn")

	// 发射 EventTurnStart
	a.emitEvent(eventCh, event.AgentEvent{
		Type: event.EventTurnStart,
		SubmissionID: submissionID,
		TurnID: turnID,
		SessionID: a.sessionID,
		Payload: input.Prompt,
		Timestamp: time.Now().UnixMilli(),
	})

	// 记录事件日志
	if a.logger != nil {
		a.logger.LogEvent(ctx, log.NewEventRecord("turn_start", a.sessionID, turnID))
	}

	// 构建 TurnParams 并委托给 LoopGenerator
	params := &TurnParams{
		Provider: a.provider,
		ContextManager: a.contextManager,
		ToolRegistry: a.toolRegistry,
		HookPipeline: a.hookPipeline,
		MiddlewareChain: a.middlewareChain,
		Logger: a.logger,
		MaxTurns: a.maxTurns,
		RetryConfig: a.retryConfig,
		CompactThreshold: a.compactThreshold,
		SessionID: a.sessionID,
		TurnID: turnID,
		SubmissionID: submissionID,
		SteerCh: a.steerCh,
		Prompt: input.Prompt,
		PrepareNextTurn: a.prepareNextTurn,
		ProductionBundle: a.productionBundle,
	}

	result := a.generator.RunTurn(ctx, params, eventCh)

	// 根据 TurnResult 处理状态转换
	switch result.Status {
	case event.StatusCompleted:
		// 执行 AfterTurn 中间件（generator 不负责 AfterTurn）
		if a.middlewareChain != nil {
			if err := a.middlewareChain.AfterTurn(ctx, turnID); err != nil {
				a.emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: submissionID,
					TurnID: turnID,
					SessionID: a.sessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				a.emitError(eventCh, submissionID, turnID, fmt.Errorf("after turn middleware: %w", err))
				a.transitionStatus(event.StatusRunning, event.StatusError)
				return
			}
		}

		// 记录 Turn 结束日志
		if a.logger != nil {
			a.logger.LogTurn(ctx, log.NewTurnRecord("turn_end", a.sessionID, turnID, "completed"))
		}

		// 记录事件日志
		if a.logger != nil {
			a.logger.LogEvent(ctx, log.NewEventRecord("completed", a.sessionID, turnID))
		}

		// 发射 EventCompleted，标记 completed = true 避免重复
		completed = true
		a.emitEvent(eventCh, event.AgentEvent{
			Type: event.EventCompleted,
			SubmissionID: submissionID,
			TurnID: turnID,
			SessionID: a.sessionID,
			Timestamp: time.Now().UnixMilli(),
		})

		// 转换状态：Running → Completed
		a.transitionStatus(event.StatusRunning, event.StatusCompleted)

	case event.StatusCanceled:
		a.transitionStatus(event.StatusRunning, event.StatusCanceled)

	case event.StatusError:
		a.transitionStatus(event.StatusRunning, event.StatusError)
	}
}

// isRetryableError 判断错误是否为可重试的 HTTP 错误。
func isRetryableError(err error, retryOnHTTP []int) bool {
	if len(retryOnHTTP) == 0 {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		for _, code := range retryOnHTTP {
			if httpErr.StatusCode == code {
				return true
			}
		}
	}
	return false
}

// ─── Interrupt ────────────────────────────────────────────────────

// Interrupt 中断当前 Turn。
//
// 通过取消当前 Turn 的上下文来中断执行。
func (a *DefaultLoopAgent) Interrupt(_ context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status != event.StatusRunning {
		return fmt.Errorf("cannot interrupt: agent is %s", a.status)
	}

	if a.cancelFunc != nil {
		a.cancelFunc()
		a.cancelFunc = nil
	}

	// 状态转换将在 goroutine 检测到 ctx.Done 后执行
	return nil
}

// ─── Steer ───────────────────────────────────────────────────────

// Steer 中途调整方向（不中断 Turn）。
//
// 消息被发送到 steer 通道，由 Turn 循环在处理流式事件时消费。
func (a *DefaultLoopAgent) Steer(_ context.Context, message string) error {
	a.mu.Lock()

	if a.status != event.StatusRunning {
		a.mu.Unlock()
		return ErrNoActiveTurn
	}

	steerCh := a.steerCh
	a.mu.Unlock()

	if steerCh == nil {
		return ErrNoActiveTurn
	}

	select {
	case steerCh <- message:
		slog.Debug("steer message sent", "message", message)
		return nil
	default:
		return fmt.Errorf("steer channel full")
	}
}

// ─── FollowUp ────────────────────────────────────────────────────

// FollowUp 在 Turn 完成后追加后续消息。
//
// 如果当前状态为 Completed，则将后续消息作为新的 Query 提交。
// 如果 Agent 仍在运行，则返回错误。
func (a *DefaultLoopAgent) FollowUp(ctx context.Context, content string) error {
	a.mu.Lock()

	if a.closed {
		a.mu.Unlock()
		return ErrAgentClosed
	}

	if a.status != event.StatusCompleted && a.status != event.StatusIdle {
		a.mu.Unlock()
		return fmt.Errorf("cannot follow up: agent is %s", a.status)
	}

	a.mu.Unlock()

	// 作为新的 Query 提交
	_, err := a.Query(ctx, AgentInput{
		Prompt: content,
		SessionID: a.sessionID,
	})
	return err
}

// ─── Status ──────────────────────────────────────────────────────

// Status 返回当前状态。
func (a *DefaultLoopAgent) Status() event.AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// ─── Close ───────────────────────────────────────────────────────

// Close 释放资源。
//
// 取消任何正在运行的查询，并通过状态机转换状态。
// P0 Fix 2: 不再直接设置 a.status = StatusIdle，而是通过状态机合法路径转换。
func (a *DefaultLoopAgent) Close() error {
	a.mu.Lock()

	if a.closed {
		a.mu.Unlock()
		return nil
	}

	a.closed = true

	if a.cancelFunc != nil {
		a.cancelFunc()
		a.cancelFunc = nil
	}

	// 通过状态机合法路径转换：
	// Running → Canceled → Idle
	// 如果当前非 Running（如 Idle/Completed/Error/Canceled），直接尝试转到 Idle
	switch a.status {
	case event.StatusRunning:
		// Running → Canceled
		if event.CanTransition(a.status, event.StatusCanceled) {
			a.status = event.StatusCanceled
		}
		// Canceled → Idle
		if event.CanTransition(a.status, event.StatusIdle) {
			a.status = event.StatusIdle
		}
	case event.StatusCompleted:
		// Completed → Idle
		if event.CanTransition(a.status, event.StatusIdle) {
			a.status = event.StatusIdle
		}
	case event.StatusError:
		// Error → Idle
		if event.CanTransition(a.status, event.StatusIdle) {
			a.status = event.StatusIdle
		}
	case event.StatusCanceled:
		// Canceled → Idle
		if event.CanTransition(a.status, event.StatusIdle) {
			a.status = event.StatusIdle
		}
	case event.StatusIdle:
		// 已经是 Idle，无需转换
	default:
		// 未知状态，强制置为 Idle
		slog.Warn("close: unknown status, forcing to idle", "status", a.status)
		a.status = event.StatusIdle
	}

	logger := a.logger
	a.logger = nil
	a.mu.Unlock()

	// Wait for goroutines to finish after cancellation (outside lock to avoid deadlock)
	a.wg.Wait()

	if logger != nil {
		logger.Close()
	}

	return nil
}

// ─── 内部辅助方法 ──────────────────────────────────────────────────

// TransitionToWaitingApproval transitions from Running to WaitingApproval.
// This is called by ApprovalHook.OnSuspend callback.
// If the current status is not Running, the transition is a no-op (logged as invalid).
func (a *DefaultLoopAgent) TransitionToWaitingApproval() {
	a.transitionStatus(event.StatusRunning, event.StatusWaitingApproval)
}

// TransitionToRunning transitions from WaitingApproval back to Running.
// This is called by ApprovalHook.OnResume callback.
// If the current status is not WaitingApproval, the transition is a no-op (logged as invalid).
func (a *DefaultLoopAgent) TransitionToRunning() {
	a.transitionStatus(event.StatusWaitingApproval, event.StatusRunning)
}

// transitionStatus 执行状态转换，带校验。
func (a *DefaultLoopAgent) transitionStatus(from, to event.AgentStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status != from {
		slog.Warn("status transition mismatch",
			"expected", from,
			"actual", a.status,
			"target", to,
		)
		// 仍然尝试转换
	}

	if !event.CanTransition(a.status, to) {
		slog.Error("invalid status transition",
			"from", a.status,
			"to", to,
		)
		return
	}

	slog.Debug("status transition",
		"from", a.status,
		"to", to,
	)
	a.status = to

	// P1 Fix 6: 记录状态变更日志
	if a.logger != nil {
		a.logger.LogItem(context.Background(), log.NewItemRecord("status_change", a.sessionID, ""))
	}
}

// emitEvent 向事件通道发送事件。
//
// 使用非阻塞发送，如果通道满则丢弃并记录日志。
func (a *DefaultLoopAgent) emitEvent(ch chan<- event.AgentEvent, evt event.AgentEvent) {
	select {
	case ch <- evt:
	default:
		slog.Warn("event channel full, dropping event",
			"type", evt.Type,
			"submission_id", evt.SubmissionID,
		)
	}
}

// emitError 发射错误事件。
func (a *DefaultLoopAgent) emitError(ch chan<- event.AgentEvent, submissionID, turnID string, err error) {
	if err == nil {
		return
	}
	a.emitEvent(ch, event.AgentEvent{
		Type: event.EventError,
		SubmissionID: submissionID,
		TurnID: turnID,
		SessionID: a.sessionID,
		Error: err,
		Timestamp: time.Now().UnixMilli(),
	})
}

// ─── 转换辅助 ──────────────────────────────────────────────────────

// turnItemsToMessages 将 TurnItem 列表转换为 LLM 消息格式。
func turnItemsToMessages(items []ctxpkg.TurnItem) []message.Message {
	msgs := make([]message.Message, 0, len(items))
	for _, item := range items {
		msg := message.Message{
			Role: message.Role(item.Role),
			ToolCallID: item.ToolCallID,
			Name: item.ToolName,
		}

		// 构建内容块
		if item.Content != "" || item.ThinkingContent != "" {
			if item.ThinkingContent != "" {
				msg.Content = append(msg.Content, message.Content{
					Type: message.ContentThinking,
					Thinking: item.ThinkingContent,
				})
			}
			if item.Content != "" {
				msg.Content = append(msg.Content, message.Content{
					Type: message.ContentText,
					Text: item.Content,
				})
			}
		}

		// 转换工具调用
		if len(item.ToolCalls) > 0 {
			msg.ToolCalls = make([]message.ToolCall, len(item.ToolCalls))
			for i, tc := range item.ToolCalls {
				msg.ToolCalls[i] = message.ToolCall{
					ID: tc.ID,
					Name: tc.Name,
					Arguments: tc.Arguments,
				}
			}
		}

		msgs = append(msgs, msg)
	}
	return msgs
}

// generateID 生成唯一 ID。
func generateID(prefix string) string {
	b := make([]byte, 8)
	_, _ = crypto_rand.Read(b) // crypto/rand.Read 在 Go 1.20+ 永不返回错误
	return fmt.Sprintf("%s-%x", prefix, b)
}
