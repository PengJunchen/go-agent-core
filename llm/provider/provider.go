// Package provider 定义 LLM 调用的核心抽象。
//
// ModelProvider 是 go-agent-core 的 L4 LLM 协议层接口，所有 LLM 后端
// （Eino 适配器、自研推理网关等）均实现此接口。接口层零 Eino 依赖，
// 第三方实现即可替换默认的 EinoProvider。
package provider

import (
	"context"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ModelProvider 抽象 LLM 调用。EinoProvider 是默认实现，第三方可实现
// 此接口接入任意后端（自托管推理网关等），框架零感知。
type ModelProvider interface {
	// StreamChat 流式聊天，返回事件流。
	StreamChat(ctx context.Context, messages []message.Message, opts *ChatOptions) (<-chan stream.StreamEvent, error)
	// Generate 同步生成。
	Generate(ctx context.Context, messages []message.Message, opts *ChatOptions) (*message.Message, error)
	// ModelInfo 返回模型元信息。
	ModelInfo() *ModelInfo
}

// ModelInfo 描述模型的能力与限制。
type ModelInfo struct {
	Provider string
	ModelName string
	MaxTokens int
	SupportsThinking bool
	SupportsVision bool
	SupportsStreaming bool
}

// ChatOptions 是一次聊天的可选参数。
type ChatOptions struct {
	Temperature *float64
	MaxTokens *int
	StopSequences []string
	ThinkingMode *ThinkingConfig
	ToolChoice *ToolChoiceConfig
	Tools []ToolSpec
}

// ThinkingConfig 跨 provider 归一化的思维控制。
type ThinkingConfig struct {
	Enabled bool
	Budget int // 思维 token 预算，0 表示不限
}

// ToolChoiceConfig 工具选择策略。
type ToolChoiceConfig struct {
	Mode ToolChoiceMode
	Name string // Mode == ToolChoiceSpecific 时指定
}

// ToolChoiceMode 枚举工具选择模式。
type ToolChoiceMode int

const (
	// ToolChoiceAuto 由模型自主决定。
	ToolChoiceAuto ToolChoiceMode = iota
	// ToolChoiceNone 禁止调用工具。
	ToolChoiceNone
	// ToolChoiceSpecific 强制调用指定工具。
	ToolChoiceSpecific
)

// ToolSpec 描述一个可供模型调用的工具。
type ToolSpec struct {
	Name string
	Description string
	Parameters map[string]any // JSON Schema
}
