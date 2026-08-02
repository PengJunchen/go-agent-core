// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// callbacks.go 定义可配置回调类型，允许调用方自定义消息转换和上下文变换：
// - ConvertToLlmCallback: 替换默认的 turnItemsToMessages 转换逻辑
// - TransformContextCallback: 在发送给 LLM 前重写消息列表
//
// 两个回调均为可选（nil 时使用默认行为），保证向后兼容。
package loop

import (
	"context"

	"github.com/pengjunchen/go-agent-core/llm/message"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ConvertToLlmCallback converts turn items to LLM messages.
// If nil, the default turnItemsToMessages is used.
type ConvertToLlmCallback func(items []ctxpkg.TurnItem) ([]message.Message, error)

// TransformContextCallback rewrites messages before sending to LLM.
// If nil, messages are sent as-is.
type TransformContextCallback func(ctx context.Context, messages []message.Message) ([]message.Message, error)
