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
	Provider string `json:"provider"`
	ModelName string `json:"model"`
	MaxTokens int `json:"max_tokens,omitempty"`
	MaxInputTokens int `json:"max_input_tokens,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
	ContextWindow int `json:"context_window,omitempty"`
	SupportsStreaming bool `json:"supports_streaming"`
	SupportsThinking bool `json:"supports_thinking,omitempty"`
	SupportsVision bool `json:"supports_vision,omitempty"`

	// Cost information (per million tokens)
	CostInputPerMillion float64 `json:"cost_input_per_million,omitempty"`
	CostOutputPerMillion float64 `json:"cost_output_per_million,omitempty"`
	CacheReadPerMillion float64 `json:"cache_read_per_million,omitempty"`
	CacheWritePerMillion float64 `json:"cache_write_per_million,omitempty"`

	// Compatibility flags (e.g., {"tool_use": true, "json_mode": true})
	Compat map[string]bool `json:"compat,omitempty"`

	// ThinkingLevelMap maps thinking level names to token budgets for models
	// that support configurable thinking (e.g., {"low": 1024, "medium": 4096, "high": 16384}).
	ThinkingLevelMap map[string]int `json:"thinking_level_map,omitempty"`

	// Capabilities is an extensible capability list (e.g., "vision", "thinking",
	// "streaming", "tool_use"). Retained for backward compatibility; prefer Compat
	// for structured capability flags.
	Capabilities []string `json:"capabilities,omitempty"`
}

// HasCapability checks whether the model supports the given capability.
// It first checks the Capabilities list, then falls back to boolean fields
// for backward compatibility.
func (m *ModelInfo) HasCapability(cap string) bool {
	for _, c := range m.Capabilities {
		if c == cap {
			return true
		}
	}
	// Fall back to boolean fields for backward compatibility
	switch cap {
	case "vision":
		return m.SupportsVision
	case "thinking":
		return m.SupportsThinking
	case "streaming":
		return m.SupportsStreaming
	}
	return false
}

// ChatOptions 是一次聊天的可选参数。
type ChatOptions struct {
	Temperature *float64
	MaxTokens *int
	StopSequences []string
	ThinkingMode *ThinkingConfig
	ToolChoice *ToolChoiceConfig
	Tools []ToolSpec
	ResponseFormat *ResponseFormat // AC-2: Constrained sampling (json_schema / grammar)
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
