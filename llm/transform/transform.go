// Package transform 定义跨 provider 的消息转换抽象。
//
// MessageTransformer 在调用 LLM 前应用转换，解决跨模型兼容性问题：
// ToolCallId 归一化（Anthropic 64 字符限制）、图片降级、思维块适配。
// 默认实现 DefaultTransformer transformMessages。
package transform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

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
//
// 当 Pipeline 字段非 nil 时，Transform 委托给 Pipeline 执行，
// 上述开关不再生效；Pipeline 为 nil 时沿用内联逻辑，保持向后兼容。
type DefaultTransformer struct {
	ToolCallIdClamp int
	ImageFallback bool
	ThinkingAdapter bool
	Pipeline *TransformPipeline
}

// NewDefaultTransformer 构造带合理默认值的转换器。
func NewDefaultTransformer() *DefaultTransformer {
	return &DefaultTransformer{
		ToolCallIdClamp: 64,
		ImageFallback: true,
		ThinkingAdapter: true,
	}
}

// validIDRegexp 匹配 Anthropic 要求的 ToolCall ID 字符集。
var validIDRegexp = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// Transform 实现消息转换。
// 当 Pipeline 非 nil 时委托给 Pipeline 执行；
// 否则依次应用深拷贝、ToolCallIdClamp、ImageFallback、ThinkingAdapter。
func (t *DefaultTransformer) Transform(ctx context.Context, messages []message.Message, targetProvider string) ([]message.Message, error) {
	if t.Pipeline != nil {
		return t.Pipeline.Execute(ctx, messages, targetProvider)
	}

	if len(messages) == 0 {
		return []message.Message{}, nil
	}

	out := deepCopyMessages(messages)

	if t.ToolCallIdClamp > 0 {
		applyToolCallIdClamp(out, t.ToolCallIdClamp)
	}

	if t.ImageFallback {
		applyImageFallback(out, targetProvider)
	}

	if t.ThinkingAdapter {
		applyThinkingAdapter(out, targetProvider)
	}

	return out, nil
}

// deepCopyMessages 对消息切片做深拷贝，确保修改输出不影响输入。
func deepCopyMessages(msgs []message.Message) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, m := range msgs {
		out[i] = deepCopyMessage(m)
	}
	return out
}

// deepCopyMessage 对单条消息做深拷贝。
func deepCopyMessage(m message.Message) message.Message {
	cp := message.Message{
		Role: m.Role,
		ToolCallID: m.ToolCallID,
		Name: m.Name,
	}

	if m.Content != nil {
		cp.Content = make([]message.Content, len(m.Content))
		for j, c := range m.Content {
			cp.Content[j] = deepCopyContent(c)
		}
	}

	if m.ToolCalls != nil {
		cp.ToolCalls = make([]message.ToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			cp.ToolCalls[j] = deepCopyToolCall(tc)
		}
	}

	return cp
}

// deepCopyContent 对 Content 做深拷贝。
func deepCopyContent(c message.Content) message.Content {
	cp := message.Content{
		Type: c.Type,
		Text: c.Text,
		Thinking: c.Thinking,
	}
	if c.Image != nil {
		cp.Image = &message.Image{
			Data: c.Image.Data,
			MediaType: c.Image.MediaType,
		}
	}
	return cp
}

// deepCopyToolCall 对 ToolCall 做深拷贝。
func deepCopyToolCall(tc message.ToolCall) message.ToolCall {
	cp := message.ToolCall{
		ID: tc.ID,
		Name: tc.Name,
	}
	if tc.Arguments != nil {
		cp.Arguments = make(map[string]any, len(tc.Arguments))
		for k, v := range tc.Arguments {
			cp.Arguments[k] = v
		}
	}
	return cp
}

// applyToolCallIdClamp 对所有消息中的 ToolCall.ID 和 ToolCallID 做归一化：
// 1. 清理非法字符，只保留 [a-zA-Z0-9_-]
// 2. 超过 clamp 长度时，截取前 (clamp-8) 字符 + "-" + SHA256 后 7 字符
func applyToolCallIdClamp(msgs []message.Message, clamp int) {
	if clamp <= 0 {
		return
	}
	for i := range msgs {
		// 处理 ToolCalls
		for j := range msgs[i].ToolCalls {
			msgs[i].ToolCalls[j].ID = clampID(msgs[i].ToolCalls[j].ID, clamp)
		}
		// 处理 Tool 角色消息的 ToolCallID
		if msgs[i].Role == message.RoleTool && msgs[i].ToolCallID != "" {
			msgs[i].ToolCallID = clampID(msgs[i].ToolCallID, clamp)
		}
	}
}

// clampID 对单个 ID 做清理和截断。
func clampID(id string, clamp int) string {
	// 清理非法字符
	id = validIDRegexp.ReplaceAllString(id, "_")

	// 长度合规直接返回
	if len(id) <= clamp {
		return id
	}

	// 截断: 前 (clamp-8) 字符 + "-" + SHA256 后 7 字符
	prefixLen := clamp - 8 // 1 for "-" + 7 for hash suffix
	if prefixLen < 1 {
		prefixLen = 1
	}
	prefix := id[:prefixLen]
	hash := sha256.Sum256([]byte(id))
	hashSuffix := hex.EncodeToString(hash[:])[:7]
	return prefix + "-" + hashSuffix
}

// supportsVision 判断目标 provider 是否支持图片。
// 已知支持视觉的 provider/model 组合返回 true，其余返回 false。
func supportsVision(targetProvider string) bool {
	p := strings.ToLower(targetProvider)
	switch {
	case strings.HasPrefix(p, "anthropic"):
		return true
	case strings.HasPrefix(p, "openai"):
		return true
	default:
		return false
	}
}

// applyImageFallback 将图片块替换为文本占位符。
// 仅在 provider 不支持视觉时生效。
func applyImageFallback(msgs []message.Message, targetProvider string) {
	if supportsVision(targetProvider) {
		return
	}
	for i := range msgs {
		for j := range msgs[i].Content {
			if msgs[i].Content[j].Type == message.ContentImage && msgs[i].Content[j].Image != nil {
				img := msgs[i].Content[j].Image
				size := len(img.Data) // base64 字符串长度近似字节数
				placeholder := fmt.Sprintf("[Image: %s, %d bytes]", img.MediaType, size)
				msgs[i].Content[j] = message.Content{
					Type: message.ContentText,
					Text: placeholder,
				}
			}
		}
	}
}

// applyThinkingAdapter 适配思维块：
// - OpenAI: ContentThinking → ContentText，加 "[Thinking] " 前缀
// - 其余 provider: 保持原样
func applyThinkingAdapter(msgs []message.Message, targetProvider string) {
	if !isOpenAIProvider(targetProvider) {
		return
	}
	for i := range msgs {
		for j := range msgs[i].Content {
			if msgs[i].Content[j].Type == message.ContentThinking {
				msgs[i].Content[j] = message.Content{
					Type: message.ContentText,
					Text: "[Thinking] " + msgs[i].Content[j].Thinking,
				}
			}
		}
	}
}

// isOpenAIProvider 判断是否为 OpenAI 系 provider。
func isOpenAIProvider(targetProvider string) bool {
	return strings.ToLower(targetProvider) == "openai"
}
