// Package transform 定义跨 provider 的消息转换抽象。
//
// MessageTransformer 在调用 LLM 前应用转换，解决跨模型兼容性问题：
// ToolCallId 归一化（Anthropic 64 字符限制）、图片降级、思维块适配。
// 默认实现 DefaultTransformer transformMessages。
package transform

import (
	"context"

	"github.com/pengjunchen/go-agent-core/llm/message"
)

// MessageTransformer 跨 provider 消息转换器。
type MessageTransformer interface {
	// Transform 将 messages 转换为目标 provider 兼容的格式。
	Transform(ctx context.Context, messages []message.Message, targetProvider string) ([]message.Message, error)
}

// DefaultTransformer 是默认实现。
//
// 三个开关分别控制：
// - ToolCallIdClamp：Anthropic 要求 ID 匹配 ^[a-zA-Z0-9_-]+$ 且 ≤64 字符
// - ImageFallback：不支持图片的 provider 替换为文本占位符
// - ThinkingAdapter：不同模型对思维块的处理方式不同
type DefaultTransformer struct {
	ToolCallIdClamp int
	ImageFallback bool
	ThinkingAdapter bool
}

// NewDefaultTransformer 构造带合理默认值的转换器。
func NewDefaultTransformer() *DefaultTransformer {
	return &DefaultTransformer{
		ToolCallIdClamp: 64,
		ImageFallback: true,
		ThinkingAdapter: true,
	}
}

// Transform 实现消息转换。当前为直通骨架，具体归一化逻辑在 M1 适配器实现。
func (t *DefaultTransformer) Transform(ctx context.Context, messages []message.Message, targetProvider string) ([]message.Message, error) {
	out := make([]message.Message, len(messages))
	copy(out, messages)
	return out, nil
}
