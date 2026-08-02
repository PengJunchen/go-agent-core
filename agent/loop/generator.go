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
	"math/rand"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/extension"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	"github.com/pengjunchen/go-agent-core/llm/transform"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
	"github.com/pengjunchen/go-agent-core/production"
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
	ProductionBundle *production.ProductionBundle // 可选，nil 表示不启用生产化组件
	ToolExecutor *registry.ParallelToolExecutor // 可选，nil 表示串行执行
	ConvertToLlm ConvertToLlmCallback // 可选，nil 表示使用默认 turnItemsToMessages
	MessageTransformer transform.MessageTransformer // 可选，nil 表示使用 DefaultTransformer 进行跨 provider 归一化
	TransformContext TransformContextCallback // 可选，nil 表示不转换消息
	ExtensionRunner *extension.ExtensionRunner // 可选，nil 表示不发射扩展事件（向后兼容）
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
			g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, err)
			return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
		}

		// 转换 TurnItems → []message.Message（可配置回调）
		var msgs []message.Message
		if params.ConvertToLlm != nil {
			msgs, err = params.ConvertToLlm(items)
			if err != nil {
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("convert to llm: %w", err))
				g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, err)
				return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
			}
		} else {
			msgs = turnItemsToMessages(items)
		}

		// MessageTransformer：跨 provider 消息归一化（ToolCallID 截断、图片降级、思维块适配等）
		// 在 TransformContext 回调之前应用，确保用户回调可以进一步自定义
		providerName := ""
		if activeProvider != nil {
			if mi := activeProvider.ModelInfo(); mi != nil {
				providerName = mi.Provider
			}
		}
		if params.MessageTransformer != nil {
			msgs, err = params.MessageTransformer.Transform(ctx, msgs, providerName)
		} else {
			msgs, err = transform.NewDefaultTransformer().Transform(ctx, msgs, providerName)
		}
		if err != nil {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTurnEnd,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Timestamp: time.Now().UnixMilli(),
			})
			emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("message transform: %w", err))
			g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, err)
			return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
		}

		// TransformContext 回调：在发送给 LLM 前重写消息
		if params.TransformContext != nil {
			msgs, err = params.TransformContext(ctx, msgs)
			if err != nil {
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("transform context: %w", err))
				g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, err)
				return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
			}
		}

		// 构建工具列表
		chatOpts := buildChatOptions(ctx, params.ToolRegistry)

		// 扩展事件：BeforeProviderRequest
		if earlyRet, replacePayload := g.checkExtensionEvent(eventCh, params.ExtensionRunner, extension.EventBeforeProviderRequest, params, turnCount, msgs); earlyRet != nil {
			return earlyRet
		} else if replacePayload != nil {
			if replacedMsgs, ok := replacePayload.([]message.Message); ok {
				msgs = replacedMsgs
			}
		}

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

		// 调用 LLM（带重试逻辑，可选熔断器保护）
		var streamCh <-chan stream.StreamEvent
		if params.ProductionBundle != nil && params.ProductionBundle.CircuitBreaker != nil {
			cbErr := params.ProductionBundle.CircuitBreaker.Execute(ctx, func(ctx context.Context) error {
				var chatErr error
				streamCh, chatErr = streamChatWithRetry(ctx, activeProvider, params.RetryConfig, msgs, chatOpts)
				return chatErr
			})
			if cbErr != nil {
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				if errors.Is(cbErr, production.ErrCircuitOpen) {
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("circuit breaker open: %w", cbErr))
				} else {
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("stream chat: %w", cbErr))
				}
				g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, cbErr)
				return &TurnResult{Status: event.StatusError, Error: cbErr, TurnCount: turnCount}
			}
		} else {
			var chatErr error
			streamCh, chatErr = streamChatWithRetry(ctx, activeProvider, params.RetryConfig, msgs, chatOpts)
			if chatErr != nil {
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("stream chat: %w", chatErr))
			g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, chatErr)
			return &TurnResult{Status: event.StatusError, Error: chatErr, TurnCount: turnCount}
			}
		}

		// 处理流式事件
		var toolCalls []stream.ToolCall
		var textContent string
		var thinkingContent string
		var finishReason string
		streamDone := false

		// 发射消息开始事件（LLM 响应开始）
		emitEvent(eventCh, event.AgentEvent{
			Type: event.EventMessageStart,
			SubmissionID: params.SubmissionID,
			TurnID: params.TurnID,
			SessionID: params.SessionID,
			Timestamp: time.Now().UnixMilli(),
		})

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
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventMessageUpdate,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: event.MessageUpdatePayload{
						Type: event.MessageUpdateText,
						Content: streamEvt.Content,
					},
					Timestamp: time.Now().UnixMilli(),
				})
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
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventMessageUpdate,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: event.MessageUpdatePayload{
						Type: event.MessageUpdateThinking,
						Content: streamEvt.Thinking,
					},
					Timestamp: time.Now().UnixMilli(),
				})
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
				if streamEvt.FinishReason != "" {
					finishReason = streamEvt.FinishReason
				}
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
				g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, streamEvt.Error)
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

		// 扩展事件：AfterProviderResponse
		extResponsePayload := map[string]any{
			"text": textContent,
			"thinking": thinkingContent,
			"tool_calls": toolCalls,
			"finish_reason": finishReason,
		}
		if earlyRet, replacePayload := g.checkExtensionEvent(eventCh, params.ExtensionRunner, extension.EventAfterProviderResponse, params, turnCount, extResponsePayload); earlyRet != nil {
			return earlyRet
		} else if replacePayload != nil {
			if replaced, ok := replacePayload.(map[string]any); ok {
				if t, ok := replaced["text"].(string); ok {
					textContent = t
				}
				if t, ok := replaced["thinking"].(string); ok {
					thinkingContent = t
				}
				if tc, ok := replaced["tool_calls"].([]stream.ToolCall); ok {
					toolCalls = tc
				}
				if fr, ok := replaced["finish_reason"].(string); ok {
					finishReason = fr
				}
			}
		}

		// 发射消息结束事件（LLM 响应完成，工具调用前）
		emitEvent(eventCh, event.AgentEvent{
			Type: event.EventMessageEnd,
			SubmissionID: params.SubmissionID,
			TurnID: params.TurnID,
			SessionID: params.SessionID,
			Timestamp: time.Now().UnixMilli(),
		})

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
		g.synthesizeFailureMessage(ctx, params, eventCh, turnCount, err)
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

		// StopLength 截断保护：如果响应因 token 上限被截断，工具调用参数可能不完整，
		// 跳过工具执行并为每个工具调用记录错误结果，让 LLM 在下一轮重试。
		if finishReason == stream.FinishReasonLength && len(toolCalls) > 0 {
			for _, tc := range toolCalls {
				errResult := &registry.ToolResult{
					Content: "tool call skipped: response was truncated due to length limit",
					IsError: true,
				}
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventToolCallResult,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: errResult,
					Timestamp: time.Now().UnixMilli(),
				})
				if err := params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{
					Role: string(message.RoleTool),
					Content: errResult.Content,
					ToolCallID: tc.ID,
					ToolName: tc.Name,
					Metadata: map[string]any{"is_error": true},
				}); err != nil {
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("record truncated tool result: %w", err))
				}
			}
			turnCount++
			continue
		}

		// 执行工具调用
		shouldTerminate := false
		if params.ToolExecutor != nil && len(toolCalls) > 1 {
			// ─── 并行执行路径 ───
			var parallelResult *TurnResult
			shouldTerminate, parallelResult = g.executeToolsParallel(ctx, params, eventCh, toolCalls, turnCount)
			if parallelResult != nil {
				return parallelResult
			}
		} else {
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

				// 安全守卫校验
				if params.ProductionBundle != nil && params.ProductionBundle.SecurityGuard != nil {
					secDecision, secErr := params.ProductionBundle.SecurityGuard.ValidateToolCall(ctx, production.SecurityCallInfo{
						ToolName: hookCall.Name,
						Arguments: hookCall.Arguments,
						SessionID: params.SessionID,
					})
					if secErr != nil {
						emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("security guard error: %w", secErr))
						emitEvent(eventCh, event.AgentEvent{
							Type: event.EventToolCallResult,
							SubmissionID: params.SubmissionID,
							TurnID: params.TurnID,
							SessionID: params.SessionID,
							Payload: &registry.ToolResult{
								Content: fmt.Sprintf("security validation error: %v", secErr),
								IsError: true,
							},
							Timestamp: time.Now().UnixMilli(),
						})
						continue
					}
					if !secDecision.Allowed {
						reason := secDecision.Reason
						if reason == "" {
							reason = "blocked by security policy"
						}
						emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("tool call blocked by security: %s", reason))
						emitEvent(eventCh, event.AgentEvent{
							Type: event.EventToolCallResult,
							SubmissionID: params.SubmissionID,
							TurnID: params.TurnID,
							SessionID: params.SessionID,
							Payload: &registry.ToolResult{
								Content: fmt.Sprintf("tool call blocked by security: %s", reason),
								IsError: true,
							},
							Timestamp: time.Now().UnixMilli(),
						})
						continue
					}
				}

				// 幂等键检查
				if params.ProductionBundle != nil && params.ProductionBundle.IdempotencyKey != nil {
					idemKey := fmt.Sprintf("%s:%v", hookCall.Name, hookCall.Arguments)
					rec, found, idemErr := params.ProductionBundle.IdempotencyKey.Check(ctx, idemKey)
					if idemErr != nil {
						slog.Warn("idempotency check failed, proceeding with execution", "error", idemErr)
					} else if found && rec != nil {
						// Return cached result
						cachedResult := &registry.ToolResult{
							Content: fmt.Sprintf("%v", rec.Result),
						}
						emitEvent(eventCh, event.AgentEvent{
							Type: event.EventToolCallResult,
							SubmissionID: params.SubmissionID,
							TurnID: params.TurnID,
							SessionID: params.SessionID,
							Payload: cachedResult,
							Timestamp: time.Now().UnixMilli(),
						})
						if err := params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{
							Role: string(message.RoleTool),
							Content: cachedResult.Content,
							ToolCallID: hookCall.ID,
							ToolName: hookCall.Name,
							Metadata: map[string]any{"is_error": false, "idempotency_cached": true},
						}); err != nil {
							emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("record idempotency result: %w", err))
						}
						continue
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

				// 循环检测
				if params.ProductionBundle != nil && params.ProductionBundle.LoopDetector != nil {
					_ = params.ProductionBundle.LoopDetector.Record(ctx, production.ToolCallRecord{ // 循环检测记录失败不影响工具执行
						ToolName: hookCall.Name,
						Arguments: hookCall.Arguments,
						Timestamp: time.Now(),
					})
					if params.ProductionBundle.LoopDetector.IsLoop(ctx) {
						emitEvent(eventCh, event.AgentEvent{
							Type: event.EventToolLoopDetected,
							SubmissionID: params.SubmissionID,
							TurnID: params.TurnID,
							SessionID: params.SessionID,
							Payload: hookCall.Name,
							Timestamp: time.Now().UnixMilli(),
						})
						// 调用 forceTextReply 生成文本摘要，而非直接报错退出
						g.forceTextReply(ctx, params, eventCh, hookCall.Name)
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

				// 审计日志
				if params.ProductionBundle != nil && params.ProductionBundle.AuditLogger != nil {
					_ = params.ProductionBundle.AuditLogger.LogToolCall(ctx, production.AuditToolCallEvent{ // 审计日志记录失败不影响工具执行
						Timestamp: time.Now(),
						SessionID: params.SessionID,
						ToolName: hookCall.Name,
						Arguments: hookCall.Arguments,
						Result: hookResult.Content,
						Approved: true, // If we got here, the call was approved (not blocked)
						DecisionBy: "auto",
					})
				}

				// 幂等键记录
				if params.ProductionBundle != nil && params.ProductionBundle.IdempotencyKey != nil {
					idemKey := fmt.Sprintf("%s:%v", hookCall.Name, hookCall.Arguments)
					_ = params.ProductionBundle.IdempotencyKey.Record(ctx, idemKey, hookResult.Content) // 幂等记录失败不影响工具执行
				}
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

	return &TurnResult{Status: event.StatusCompleted, TurnCount: turnCount}
}

// synthesizeFailureMessage 合成一条助手消息来解释失败，记录到上下文管理器并发射到事件流。
// 用于可恢复错误的返回路径，使用户看到解释而非原始错误。
func (g *DefaultLoopGenerator) synthesizeFailureMessage(ctx context.Context, params *TurnParams, eventCh chan<- event.AgentEvent, turnCount int, runErr error) {
	// 合成一条解释失败的助手消息
	msg := fmt.Sprintf("I encountered an error while processing your request: %v. Please try rephrasing your request or try again.", runErr)

	// 记录合成消息到上下文
	_ = params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{ // 合成消息记录失败不影响错误返回路径
		Role: string(message.RoleAssistant),
		Content: msg,
		Metadata: map[string]any{
			"synthesized": true,
			"error": runErr.Error(),
		},
	})

	// 发射文本到事件流，使用户看到解释
	emitEvent(eventCh, event.AgentEvent{
		Type: event.EventTextDelta,
		SubmissionID: params.SubmissionID,
		TurnID: params.TurnID,
		SessionID: params.SessionID,
		Payload: msg,
		Timestamp: time.Now().UnixMilli(),
	})
	emitEvent(eventCh, event.AgentEvent{
		Type: event.EventMessageUpdate,
		SubmissionID: params.SubmissionID,
		TurnID: params.TurnID,
		SessionID: params.SessionID,
		Payload: event.MessageUpdatePayload{
			Type: event.MessageUpdateText,
			Content: msg,
		},
		Timestamp: time.Now().UnixMilli(),
	})
}

// checkExtensionEvent 发射扩展事件并处理结果。
// 返回 earlyReturn 非 nil 时表示应立即返回 RunTurn（Block/Cancel）。
// 返回 replacement 非 nil 时表示事件监听器提供了替换数据。
func (g *DefaultLoopGenerator) checkExtensionEvent(
	eventCh chan<- event.AgentEvent,
	runner *extension.ExtensionRunner,
	evtType extension.EventType,
	params *TurnParams,
	turnCount int,
	payload any,
) (earlyReturn *TurnResult, replacement any) {
	if runner == nil {
		return nil, nil
	}
	result := runner.EmitEvent(extension.Event{
		Type: evtType,
		SessionID: params.SessionID,
		TurnID: params.TurnID,
		Payload: payload,
	})
	if result == nil {
		return nil, nil
	}
	switch result.Action {
	case extension.EventActionBlock:
		emitEvent(eventCh, event.AgentEvent{
			Type: event.EventTurnEnd,
			SubmissionID: params.SubmissionID,
			TurnID: params.TurnID,
			SessionID: params.SessionID,
			Timestamp: time.Now().UnixMilli(),
		})
		err := fmt.Errorf("extension blocked: %s", result.Reason)
		emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, err)
		return &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}, nil
	case extension.EventActionCancel:
		emitEvent(eventCh, event.AgentEvent{
			Type: event.EventTurnEnd,
			SubmissionID: params.SubmissionID,
			TurnID: params.TurnID,
			SessionID: params.SessionID,
			Timestamp: time.Now().UnixMilli(),
		})
		return &TurnResult{Status: event.StatusCanceled, Error: context.Canceled, TurnCount: turnCount}, nil
	case extension.EventActionReplace:
		return nil, result.Replace
	}
	return nil, nil
}

// ─── 并行执行路径 ─────────────────────────────────────────────────

// executeToolsParallel 在配置了 ToolExecutor 时并行执行工具调用。
// 采用三阶段方法：
// - Phase 1：串行预检查（Before hooks + SecurityGuard + 幂等检查）
// - Phase 2：并行执行通过预检查的调用
// - Phase 3：串行后处理（After hooks + 记录 + 检测）
//
// 返回 (shouldTerminate, earlyReturn)。当 earlyReturn 非 nil 时，调用方应立即返回该结果。
func (g *DefaultLoopGenerator) executeToolsParallel(
	ctx context.Context,
	params *TurnParams,
	eventCh chan<- event.AgentEvent,
	toolCalls []stream.ToolCall,
	turnCount int,
) (bool, *TurnResult) {
	// Phase 1: 串行预检查
	var passableCalls []registry.ToolCall
	var passableHookCalls []*toolhook.ToolCall
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
			return false, &TurnResult{Status: event.StatusCanceled, Error: ctx.Err(), TurnCount: turnCount}
		default:
		}

		hookCall := &toolhook.ToolCall{
			ID: tc.ID,
			Name: tc.Name,
			Arguments: tc.Arguments,
			SessionID: params.SessionID,
			TurnID: params.TurnID,
		}

		// Before 钩子
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
				return false, &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
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

		// 安全守卫校验
		if params.ProductionBundle != nil && params.ProductionBundle.SecurityGuard != nil {
			secDecision, secErr := params.ProductionBundle.SecurityGuard.ValidateToolCall(ctx, production.SecurityCallInfo{
				ToolName: hookCall.Name,
				Arguments: hookCall.Arguments,
				SessionID: params.SessionID,
			})
			if secErr != nil {
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("security guard error: %w", secErr))
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventToolCallResult,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: &registry.ToolResult{
						Content: fmt.Sprintf("security validation error: %v", secErr),
						IsError: true,
					},
					Timestamp: time.Now().UnixMilli(),
				})
				continue
			}
			if !secDecision.Allowed {
				reason := secDecision.Reason
				if reason == "" {
					reason = "blocked by security policy"
				}
				emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("tool call blocked by security: %s", reason))
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventToolCallResult,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: &registry.ToolResult{
						Content: fmt.Sprintf("tool call blocked by security: %s", reason),
						IsError: true,
					},
					Timestamp: time.Now().UnixMilli(),
				})
				continue
			}
		}

		// 幂等键检查
		if params.ProductionBundle != nil && params.ProductionBundle.IdempotencyKey != nil {
			idemKey := fmt.Sprintf("%s:%v", hookCall.Name, hookCall.Arguments)
			rec, found, idemErr := params.ProductionBundle.IdempotencyKey.Check(ctx, idemKey)
			if idemErr != nil {
				slog.Warn("idempotency check failed, proceeding with execution", "error", idemErr)
			} else if found && rec != nil {
				cachedResult := &registry.ToolResult{
					Content: fmt.Sprintf("%v", rec.Result),
				}
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventToolCallResult,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: cachedResult,
					Timestamp: time.Now().UnixMilli(),
				})
				if err := params.ContextManager.RecordItem(ctx, ctxpkg.TurnItem{
					Role: string(message.RoleTool),
					Content: cachedResult.Content,
					ToolCallID: hookCall.ID,
					ToolName: hookCall.Name,
					Metadata: map[string]any{"is_error": false, "idempotency_cached": true},
				}); err != nil {
					emitError(eventCh, params.SubmissionID, params.TurnID, params.SessionID, fmt.Errorf("record idempotency result: %w", err))
				}
				continue
			}
		}

		// 通过所有预检查，加入待执行列表
		passableCalls = append(passableCalls, registry.ToolCall{
			ID: hookCall.ID,
			Name: hookCall.Name,
			Arguments: hookCall.Arguments,
			SessionID: hookCall.SessionID,
			TurnID: hookCall.TurnID,
		})
		passableHookCalls = append(passableHookCalls, hookCall)
	}

	// Phase 2: 并行执行
	if len(passableCalls) == 0 {
		return shouldTerminate, nil
	}

	execResults := params.ToolExecutor.ExecuteTools(ctx, passableCalls, params.ToolRegistry)

	// Phase 3: 串行后处理
	for i, execResult := range execResults {
		hookCall := passableHookCalls[i]

		// 构建工具结果
		var toolResult *registry.ToolResult
		if execResult.Error != nil {
			toolResult = &registry.ToolResult{
				Content: execResult.Error.Error(),
				IsError: true,
			}
		} else if execResult.Result != nil {
			toolResult = execResult.Result
		} else {
			toolResult = &registry.ToolResult{Content: ""}
		}

		// After 钩子
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
				return false, &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
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
			return false, &TurnResult{Status: event.StatusError, Error: err, TurnCount: turnCount}
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

		// 循环检测
		if params.ProductionBundle != nil && params.ProductionBundle.LoopDetector != nil {
			_ = params.ProductionBundle.LoopDetector.Record(ctx, production.ToolCallRecord{ // 循环检测记录失败不影响工具执行
				ToolName: hookCall.Name,
				Arguments: hookCall.Arguments,
				Timestamp: time.Now(),
			})
			if params.ProductionBundle.LoopDetector.IsLoop(ctx) {
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventToolLoopDetected,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Payload: hookCall.Name,
					Timestamp: time.Now().UnixMilli(),
				})
				g.forceTextReply(ctx, params, eventCh, hookCall.Name)
				emitEvent(eventCh, event.AgentEvent{
					Type: event.EventTurnEnd,
					SubmissionID: params.SubmissionID,
					TurnID: params.TurnID,
					SessionID: params.SessionID,
					Timestamp: time.Now().UnixMilli(),
				})
				return false, &TurnResult{Status: event.StatusCompleted, TurnCount: turnCount}
			}
		}

		// 审计日志
		if params.ProductionBundle != nil && params.ProductionBundle.AuditLogger != nil {
			_ = params.ProductionBundle.AuditLogger.LogToolCall(ctx, production.AuditToolCallEvent{ // 审计日志记录失败不影响工具执行
				Timestamp: time.Now(),
				SessionID: params.SessionID,
				ToolName: hookCall.Name,
				Arguments: hookCall.Arguments,
				Result: hookResult.Content,
				Approved: true, // If we got here, the call was approved (not blocked)
				DecisionBy: "auto",
			})
		}

		// 幂等键记录
		if params.ProductionBundle != nil && params.ProductionBundle.IdempotencyKey != nil {
			idemKey := fmt.Sprintf("%s:%v", hookCall.Name, hookCall.Arguments)
			_ = params.ProductionBundle.IdempotencyKey.Record(ctx, idemKey, hookResult.Content) // 幂等记录失败不影响工具执行
		}
	}

	return shouldTerminate, nil
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

// streamChatWithRetry 调用 StreamChat 并在遇到可重试错误时进行指数退避重试。
// 重试判断结合 provider.IsRetryableAssistantError（跨 provider 统一分类）
// 和 rc.RetryOnHTTP（向后兼容的 HTTP 状态码白名单）。
// 支持 Retry-After 响应头和抖动（jitter）。
func streamChatWithRetry(ctx context.Context, p provider.ModelProvider, rc *RetryConfig, msgs []message.Message, chatOpts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	streamCh, err := p.StreamChat(ctx, msgs, chatOpts)
	if err == nil {
		return streamCh, nil
	}

	// 没有重试配置或 MaxRetries 为 0，直接返回错误
	if rc == nil || rc.MaxRetries <= 0 {
		return nil, err
	}

	// 检查是否为可重试错误：结合 provider 统一分类和 HTTPError 白名单
	if !isRetryableWithProvider(err, rc.RetryOnHTTP) {
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
		// 指数退避 + 抖动
		backoff := float64(baseDelay) * math.Pow(2, float64(retryCount))
		if backoff > float64(maxDelay) {
			backoff = float64(maxDelay)
		}
		// 添加抖动：取 backoff 的 50%-150%
		jitter := rand.Float64() * backoff
		backoff = backoff*0.5 + jitter
		if backoff > float64(maxDelay) {
			backoff = float64(maxDelay)
		}
		delay := time.Duration(backoff)

		// 如果错误包含 RetryAfter，取较大值
		if pe := provider.ClassifyError(err); pe != nil && pe.RetryAfter > 0 {
			if pe.RetryAfter > delay {
				delay = pe.RetryAfter
			}
		}

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
		if !isRetryableWithProvider(err, rc.RetryOnHTTP) {
			return nil, err
		}
	}

	return nil, err
}

// isRetryableWithProvider 结合 provider 统一分类和 HTTPError 白名单判断错误是否可重试。
// 优先使用 provider.IsRetryableAssistantError 进行跨 provider 统一分类，
// 同时保留 RetryOnHTTP 白名单以向后兼容。
func isRetryableWithProvider(err error, retryOnHTTP []int) bool {
	// 1. 使用 provider 统一分类（覆盖 ProviderError、网络错误、消息正则匹配）
	if provider.IsRetryableAssistantError(err) {
		return true
	}
	// 2. 向后兼容：检查 RetryOnHTTP 白名单中的 HTTPError
	if len(retryOnHTTP) > 0 {
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			for _, code := range retryOnHTTP {
				if httpErr.StatusCode == code {
					return true
				}
			}
		}
	}
	return false
}

// forceTextReply 在循环检测触发后，使用 ToolChoiceNone 再调用一次 LLM，
// 生成文本摘要而非继续执行工具循环。
//
// 使用 context.WithoutCancel 派生不受父上下文取消影响的新上下文，
// 确保 forceTextReply 能在父上下文被取消后仍完成 LLM 调用。
func (g *DefaultLoopGenerator) forceTextReply(ctx context.Context, params *TurnParams, eventCh chan<- event.AgentEvent, loopToolName string) {
	textCtx := context.WithoutCancel(ctx)

	items, _ := params.ContextManager.GetMessages(textCtx, nil)
	msgs := turnItemsToMessages(items)

	msgs = append(msgs, message.Message{
		Role: message.RoleUser,
		Content: []message.Content{{
			Type: message.ContentText,
			Text: fmt.Sprintf("A tool loop was detected with tool %q. Please provide a text summary of what you were trying to do and why you couldn't complete it.", loopToolName),
		}},
	})

	opts := &provider.ChatOptions{
		ToolChoice: &provider.ToolChoiceConfig{Mode: provider.ToolChoiceNone},
	}

	streamCh, err := params.Provider.StreamChat(textCtx, msgs, opts)
	if err != nil {
		return
	}

	for evt := range streamCh {
		if evt.Type == stream.StreamTextDelta {
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventTextDelta,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Payload: evt.Content,
				Timestamp: time.Now().UnixMilli(),
			})
			emitEvent(eventCh, event.AgentEvent{
				Type: event.EventMessageUpdate,
				SubmissionID: params.SubmissionID,
				TurnID: params.TurnID,
				SessionID: params.SessionID,
				Payload: event.MessageUpdatePayload{
					Type: event.MessageUpdateText,
					Content: evt.Content,
				},
				Timestamp: time.Now().UnixMilli(),
			})
		}
	}
}
