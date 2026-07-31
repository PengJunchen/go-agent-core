// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// generator.go 提供无状态生成器 LoopGenerator，将 DefaultLoopAgent 的
// runLoop 逻辑提取为可独立调用、可并发复用的纯函数式组件。
// LoopGenerator 不持有任何可变状态，所有依赖通过 TurnParams 传入。
package loop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ─── LoopGenerator 接口 ──────────────────────────────────────────

// LoopGenerator 无状态生成器接口。
// 执行单个 Turn 的 LLM 循环（ReAct: model → tool → model → ...），
// 不持有任何可变状态，可并发调用。
type LoopGenerator interface {
	// RunTurn 执行一个完整的 Turn：LLM 推理 → 工具调用 → 循环。
	// 参数通过 TurnParams 传入，事件写入 eventCh。
	// 返回 TurnResult 描述 Turn 结束状态。
	RunTurn(ctx context.Context, params *TurnParams, eventCh chan<- event.AgentEvent) *TurnResult
}

// ─── 参数与结果结构体 ─────────────────────────────────────────────

// TurnParams 封装 RunTurn 所需的全部依赖。
// 所有字段均为只读，RunTurn 不修改 params 本身。
type TurnParams struct {
	Provider provider.ModelProvider
	ContextManager ctxpkg.ContextManager
	ToolRegistry registry.ToolRegistry
	HookPipeline *toolhook.HookPipeline
	MiddlewareChain *middleware.Chain
	Logger log.ExecLogger
	MaxTurns int
	RetryConfig *RetryConfig
	CompactThreshold int
	SessionID string
	TurnID string
	SubmissionID string
	SteerCh <-chan string
	Prompt string
	PrepareNextTurn PrepareNextTurnFunc // 可选，每次 Turn 前回调以动态替换 ModelProvider
}

// TurnResult 描述一次 Turn 的结束状态。
type TurnResult struct {
	Status event.AgentStatus // Completed, Error, Canceled, MaxTurnsReached
	Error error
	TurnCount int
}

// ─── DefaultLoopGenerator ─────────────────────────────────────────

// Compile-time check that DefaultLoopGenerator implements LoopGenerator.
var _ LoopGenerator = (*DefaultLoopGenerator)(nil)

// DefaultLoopGenerator 是 LoopGenerator 的默认实现。
//
// 它是无状态的，所有依赖通过 TurnParams 传入。
// 核心逻辑与 DefaultLoopAgent.runLoop 一致，但去除了状态机管理，
// 只负责纯粹的 Turn 执行循环。
type DefaultLoopGenerator struct{}

// NewDefaultLoopGenerator 构造一个 DefaultLoopGenerator。
func NewDefaultLoopGenerator() *DefaultLoopGenerator {
	return &DefaultLoopGenerator{}
}

// RunTurn 执行一个完整的 Turn 循环。
//
// 核心流程：
// 1. 执行 BeforeTurn 中间件
// 2. 记录用户消息到上下文
// 3. 进入 LLM 循环：LLM 推理 → 流式事件处理 → 工具调用 → 循环
// 4. 执行 AfterTurn 中间件
// 5. 返回 TurnResult
//
// 调用者负责在 eventCh 上发送 EventTurnStart/EventTurnEnd/EventCompleted，
// RunTurn 只发送 Turn 内部事件（EventTextDelta、EventToolCallStart 等）
// 和 EventTurnEnd（在异常退出路径上）。
func (g *DefaultLoopGenerator) RunTurn(ctx context.Context, params *TurnParams, eventCh chan<- event.AgentEvent) *TurnResult {
	// 执行 BeforeTurn 中间件
	if params.MiddlewareChain != nil {
		if err := params.MiddlewareChain.BeforeTurn(ctx, params.TurnID); err != nil {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("before turn middleware: %w", err))
			return &TurnResult{Status: event.StatusError, Error: err}
		}
	}

	// 记录用户消息
	if err := params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{
		Role: string(message.RoleUser),
		Content: params.Prompt,
	}); err != nil {
		emitEvent(eventCh, event.AgentEvent{
			Type: event.EventTurnEnd,
			SubmissionID: params.SubmissionID,
			TurnID: params.TurnID,
			SessionID: params.SessionID,
			Timestamp: time.Now().UnixMilli(),
		})
		emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("record user message: %w", err))
		return &TurnResult{Status: event.StatusError, Error: err}
	}

	// 记录 Turn 开始日志
	if params.Logger != nil {
		params.Logger.LogTurn(ctx, log.NewTurnRecord("turn_start", params.SessionID, params.TurnID, "running"))
	}

	// Turn 循环
	turnCount := 0
	for turnCount < params.MaxTurns {
		select {
		case <-ctx.Done():
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, ctx.Err())
			return &TurnResult{Status: event.StatusCanceled, Error: ctx.Err(), TurnCount: turnCount}
		default:
		}

		// 调用 PrepareNextTurn 回调（如已设置），允许动态替换 Provider
		activeProvider := params.Provider
		if params.PrepareNextTurn != nil {
			if nextProvider := params.PrepareNextTurn(ctx, activeProvider, turnCount); nextProvider != nil {
				activeProvider = nextProvider
			}
		}

		// 获取消息历史
		items, err := params.ContextManager.GetMessages(ctx, nil)
		if err != nil {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("get messages: %w", err))
			return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
		}

		// 转换 TurnItems → []message.Message
		msgs := turnItemsToMessages(items)

		// 构建工具列表
		chatOpts := buildChatOptions(ctx, params.ToolRegistry)

		// 记录 LLM 调用日志（调用前）
		if params.Logger != nil {
			llmRec := log.NewItemRecord("llm_call", params.SessionID, params.TurnID)
			if mi := activeProvider.ModelInfo(); mi != nil {
				llmRec.Provider = mi.Provider
				llmRec.Model = mi.ModelName
			}
			llmRec.Input = params.Prompt
			params.Logger.LogItem(ctx, llmRec)
		}

		// 调用 LLM（带重试逻辑）
		streamCh, err := streamChatWithRetry(ctx, activeProvider, params.RetryConfig, msgs, chatOpts)
		if err != nil {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("stream chat: %w", err))
			return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
		}

		// 处理流式事件
		var toolCalls []stream.ToolCall
		var textContent string
		var thinkingContent string
		streamDone := false

		for streamEvt := range streamCh {
			switch streamEvt.Type {
			case stream.StreamTextDelta:
				textContent += streamEvt.Content
				evt := event.AgentEvent{
					Type: event.EventTextDelta,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: streamEvt.Content,
					Timestamp: time.Now().UnixMilli(),
				}
				emitEvent(eventCh, evt)
				if params.Logger != nil {
					params.Logger.LogEvent(ctx, log.NewEventRecord("text_delta", params.SessionID, params.TurnID))
				}

			case stream.StreamThinkingDelta:
				thinkingContent += streamEvt.Thinking
				evt := event.AgentEvent{
					Type: event.EventThinkingDelta,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: streamEvt.Thinking,
					Timestamp: time.Now().UnixMilli(),
				}
				emitEvent(eventCh, evt)
				if params.Logger != nil {
					params.Logger.LogEvent(ctx, log.NewEventRecord("thinking_delta", params.SessionID, params.TurnID))
				}

			case stream.StreamToolCallStart:
				if streamEvt.ToolCall != nil {
					toolCalls = append(toolCalls, *streamEvt.ToolCall)
					evt := event.AgentEvent{
						Type: event.EventToolCallStart,
						SubmissionID: params.SubmissionID,
						TurnID: params.TurnID,
						SessionID: params.SessionID,
						Payload: streamEvt.ToolCall,
						Timestamp: time.Now().UnixMilli(),
					}
					emitEvent(eventCh, evt)
					if params.Logger != nil {
						rec := log.NewEventRecord("tool_call_start", params.SessionID, params.TurnID)
						rec.ToolCallID = streamEvt.ToolCall.ID
						rec.ToolName = streamEvt.ToolCall.Name
						params.Logger.LogEvent(ctx, rec)
					}
				}

			case stream.StreamToolCallResult:
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventToolCallResult,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: streamEvt.ToolCall,
					Timestamp: time.Now().UnixMilli(),
				})

			case stream.StreamDone:
				streamDone = true

			case stream.StreamError:
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, streamEvt.Error)
				if errors.Is(streamEvt.Error, context.Canceled) || errors.Is(streamEvt.Error, context.DeadlineExceeded) {
					return &TurnResult{Status: event.StatusCanceled, Error: streamEvt.Error, TurnCount: turnCount}
				}
				return &TurnResult{Status: event.StatusError, Error: streamEvt.Error, TurnCount: turnCount}
			}

			if streamDone {
				break
			}

			// 检查 steer 消息
			select {
			case steerMsg := <-params.SteerCh:
				slog.Debug("steer message received", "message", steerMsg)
				_ = params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{
					Role: string(message.RoleUser),
					Content: steerMsg,
					Metadata: map[string]any{
						"type": "steer",
					},
				}) // steer 消息记录失败不阻断 Turn 循环
				if params.Logger != nil {
					params.Logger.LogItem(ctx, &log.ItemRecord{
						ItemType: "steer",
						Input: steerMsg,
					})
				}
			default:
			}
		}

		// 流结束后检查 context 是否已取消
		select {
		case <-ctx.Done():
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, ctx.Err())
			return &TurnResult{Status: event.StatusCanceled, Error: ctx.Err(), TurnCount: turnCount}
		default:
		}

		// 记录助手消息到上下文
		assistantItem := ctxpkg.TurnItem{
			Role: string(message.RoleAssistant),
			Content: textContent,
			ThinkingContent: thinkingContent,
		}
		if len(toolCalls) > 0 {
			refs := make([]ctxpkg.ToolCallRef, len(toolCalls))
			for i, tc := range toolCalls {
				refs[i] = ctxpkg.ToolCallRef{
					ID: tc.ID,
					Name: tc.Name,
					Arguments: tc.Arguments,
				}
			}
			assistantItem.ToolCalls = refs
		}
		if err := params.ContextManager.RecordItem(ctx, assistantItem); err != nil {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("record assistant message: %w", err))
			return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
		}

		// 没有工具调用 → 退出循环
		if len(toolCalls) == 0 {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			break
		}

		// 执行工具调用
		shouldTerminate := false
		for _, tc := range toolCalls {
			select {
			case <-ctx.Done():
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, ctx.Err())
				return &TurnResult{Status: event.StatusCanceled, Error: ctx.Err(), TurnCount: turnCount}
			default:
			}

			// 转换为 toolhook.ToolCall
			hookCall := &toolhook.ToolCall{
				ID: tc.ID,
				Name: tc.Name,
				Arguments: tc.Arguments,
				SessionID: params.SessionID,
				TurnID: params.TurnID,
			}

			// 执行 Before 钩子
			if params.HookPipeline != nil {
				beforeResult, err := params.HookPipeline.Before(ctx, hookCall)
				if err != nil {
					emitEvent(eventCh, event.AgentEvent{
						Type: event.EventTurnEnd,
						SubmissionID: params.SubmissionID,
						TurnID: params.TurnID,
						SessionID: params.SessionID,
						Timestamp: time.Now().UnixMilli(),
					})
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("hook before: %w", err))
					return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
				}
				if beforeResult.Block {
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("tool call blocked: %s", beforeResult.Reason))
					emitEvent(eventCh, event.AgentEvent{
						Type: event.EventToolCallResult,
						SubmissionID: params.SubmissionID,
						TurnID: params.TurnID,
						SessionID: params.SessionID,
						Payload: &registry.ToolResult{
							Content: fmt.Sprintf("tool call blocked: %s", beforeResult.Reason),
							IsError: true,
						},
						Timestamp: time.Now().UnixMilli(),
					})
					continue
				}
				if beforeResult.Terminate {
					shouldTerminate = true
					break
				}
				if beforeResult.ModifiedCall != nil {
					hookCall = beforeResult.ModifiedCall
				}
			}

			// 执行工具
			var toolResult *registry.ToolResult
			toolDef, err := params.ToolRegistry.GetTool(ctx, hookCall.Name)
			if err != nil {
				toolResult = &registry.ToolResult{
					Content: fmt.Sprintf("tool not found: %s", hookCall.Name),
					IsError: true,
				}
			} else {
				toolResult, err = toolDef.Handler(ctx, hookCall.Arguments)
				if err != nil {
					toolResult = &registry.ToolResult{
						Content: err.Error(),
						IsError: true,
					}
				}
			}

			// 执行 After 钩子
			hookResult := &toolhook.ToolResult{
				Content: toolResult.Content,
				IsError: toolResult.IsError,
				Details: toolResult.Details,
				Metadata: make(map[string]any),
			}
			if params.HookPipeline != nil {
				afterResult, err := params.HookPipeline.After(ctx, hookCall, hookResult)
				if err != nil {
					emitEvent(eventCh, event.AgentEvent{
						Type: event.EventTurnEnd,
						SubmissionID: params.SubmissionID,
						TurnID: params.TurnID,
						SessionID: params.SessionID,
						Timestamp: time.Now().UnixMilli(),
					})
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("hook after: %w", err))
					return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
				}
				if afterResult.Terminate {
					shouldTerminate = true
				}
				if afterResult.ModifiedResult != nil {
					hookResult = afterResult.ModifiedResult
				}
			}

			// 发射 EventToolCallResult
			emitResult := &registry.ToolResult{
				Content: hookResult.Content,
				IsError: hookResult.IsError,
				Details: hookResult.Details,
			}
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventToolCallResult,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Payload: emitResult,
				Timestamp: time.Now().UnixMilli(),
			})

			// 记录工具结果到上下文
			if err := params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{
				Role: string(message.RoleTool),
				Content: hookResult.Content,
				ToolCallID: hookCall.ID,
				ToolName: hookCall.Name,
				Metadata: map[string]any{
					"is_error": hookResult.IsError,
				},
			}); err != nil {
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("record tool result: %w", err))
				return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
			}

			// 自动压缩检查
			if params.CompactThreshold > 0 {
				usage := params.ContextManager.TokenUsage(ctx)
				if usage > params.CompactThreshold {
					emitEvent(eventCh, event.AgentEvent{
						Type: event.EventCompactStart,
						SubmissionID: params.SubmissionID,
						TurnID: params.TurnID,
						SessionID: params.SessionID,
						Timestamp: time.Now().UnixMilli(),
					})
					_, compactErr := params.ContextManager.Compact(ctx, ctxpkg.CompactAuto)
					emitEvent(eventCh, event.AgentEvent{
						Type: event.EventCompactEnd,
						SubmissionID: params.SubmissionID,
						TurnID: params.TurnID,
						SessionID: params.SessionID,
						Timestamp: time.Now().UnixMilli(),
					})
					if compactErr != nil {
						slog.Warn("auto-compact failed", "error", compactErr)
					}
				}
			}

			// 记录 Item 日志
			if params.Logger != nil {
				rec := log.NewItemRecord("tool_call", params.SessionID, params.TurnID)
				rec.ToolName = hookCall.Name
				rec.Input = hookCall.Arguments
				rec.Output = hookResult.Content
				if hookResult.IsError {
					rec.Error = hookResult.Content
				}
				params.Logger.LogItem(ctx, rec)
			}
		}

		if shouldTerminate {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			break
		}

		turnCount++

		// 检查是否达到最大轮次
		if turnCount >= params.MaxTurns {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventMaxTurnsReached,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Payload: turnCount,
				Timestamp: time.Now().UnixMilli(),
			})
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			return &TurnResult{Status: event.StatusCompleted, TurnCount: turnCount}
		}
	}

	// 执行 AfterTurn 中间件
	if params.MiddlewareChain != nil {
		if err := params.MiddlewareChain.AfterTurn(ctx, params.TurnID); err != nil {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("after turn middleware: %w", err))
			return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
		}
	}

	// 记录 Turn 结束日志
	if params.Logger != nil {
		params.Logger.LogTurn(ctx, log.NewTurnRecord("turn_end", params.SessionID, params.TurnID, "completed"))
	}

	// 记录事件日志
	if params.Logger != nil {
		params.Logger.LogEvent(ctx, log.NewEventRecord("completed", params.SessionID, params.TurnID))
	}

	return &TurnResult{Status: event.StatusCompleted, TurnCount: turnCount}
}

// ─── 包级辅助函数 ──────────────────────────────────────────────────

// emitEvent 向事件通道发送事件（非阻塞）。
func emitEvent(ch chan<- event.AgentEvent, evt event.AgentEvent) {
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
func emitError(ch chan<- event.AgentEvent, submissionID, turnID, sessionID string, err error) {
	if err == nil {
		return
	}
	emitEvent(ch, event.AgentEvent{
		Type: event.EventError,
		SubmissionID: submissionID,
		TurnID: turnID,
		SessionID: sessionID,
		Error: err,
		Timestamp: time.Now().UnixMilli(),
	})
}

// buildChatOptions 构建聊天选项，包含工具列表。
func buildChatOptions(ctx context.Context, toolRegistry registry.ToolRegistry) *provider.ChatOptions {
	opts := &provider.ChatOptions{}

	if toolRegistry != nil {
		tools, err := toolRegistry.ListTools(ctx)
		if err == nil && len(tools) > 0 {
			specs := make([]provider.ToolSpec, len(tools))
			for i, t := range tools {
				specs[i] = provider.ToolSpec{
					Name: t.Name,
					Description: t.Description,
					Parameters: t.Parameters,
				}
			}
			opts.Tools = specs
			opts.ToolChoice = &provider.ToolChoiceConfig{
				Mode: provider.ToolChoiceAuto,
			}
		}
	}

	return opts
}

// streamChatWithRetry 调用 StreamChat 并在遇到可重试的 HTTP 错误时进行指数退避重试。
func streamChatWithRetry(ctx context.Context, p provider.ModelProvider, rc *RetryConfig, msgs []message.Message, chatOpts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	streamCh, err := p.StreamChat(ctx, msgs, chatOpts)
	if err == nil {
		return streamCh, nil
	}

	// 没有重试配置或 MaxRetries 为 0，直接返回错误
	if rc == nil || rc.MaxRetries <= 0 {
		return nil, err
	}

	// 检查是否为可重试的 HTTP 错误
	if !isRetryableError(err, rc.RetryOnHTTP) {
		return nil, err
	}

	baseDelay := rc.BaseDelay
	if baseDelay <= 0 {
		baseDelay = DefaultRetryBaseDelay
	}
	maxDelay := rc.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultRetryMaxDelay
	}

	for retryCount := 0; retryCount < rc.MaxRetries; retryCount++ {
		delay := time.Duration(math.Min(float64(baseDelay*time.Duration(1<<retryCount)), float64(maxDelay)))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		slog.Debug("retrying StreamChat",
			"attempt", retryCount+1,
			"delay", delay,
		)

		streamCh, err = p.StreamChat(ctx, msgs, chatOpts)
		if err == nil {
			return streamCh, nil
		}
		if !isRetryableError(err, rc.RetryOnHTTP) {
			return nil, err
		}
	}

	return nil, err
}
