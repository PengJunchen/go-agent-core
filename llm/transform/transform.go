// Package transform 定义跨 provider 的消息转换抽象。
//
// MessageTransformer 在调用 LLM 前应用转换，解决跨模型兼容性问题：
// ToolCallId 归一化（Anthropic 64 字符限制）、图片降级、思维块适配。
// 默认实现 DefaultTransformer。
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
	case strings.HasPrefix(p, ProviderNameAnthropic):
		return true
	case strings.HasPrefix(p, ProviderNameOpenAI):
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
	return strings.ToLower(targetProvider) == ProviderNameOpenAI
}

// ========== 新增跨 Provider 转换函数 ==========

// ToolCallIDNormalizer 归一化 tool_call ID：
// - 将纯数字 ID（如 "0"、"1"、"42"）转为 "tc_{n}" 格式
// - 对应的 Tool 角色消息 ToolCallID 也同步映射
// - 与 applyToolCallIdClamp 互补：本函数解决类型差异，后者解决长度/字符集差异
func ToolCallIDNormalizer(_ context.Context, msgs []message.Message, _ string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)

	// 构建旧 ID → 新 ID 映射
	idMap := make(map[string]string)
	for i := range out {
		for j := range out[i].ToolCalls {
			orig := out[i].ToolCalls[j].ID
			if orig == "" {
				continue
			}
			if normalized, ok := idMap[orig]; ok {
				out[i].ToolCalls[j].ID = normalized
				continue
			}
			if isNumericID(orig) {
				norm := "tc_" + orig
				idMap[orig] = norm
				out[i].ToolCalls[j].ID = norm
			} else {
				idMap[orig] = orig
			}
		}
	}

	// 映射 Tool 角色消息的 ToolCallID
	for i := range out {
		if out[i].Role == message.RoleTool && out[i].ToolCallID != "" {
			if mapped, ok := idMap[out[i].ToolCallID]; ok {
				out[i].ToolCallID = mapped
			} else if isNumericID(out[i].ToolCallID) {
				out[i].ToolCallID = "tc_" + out[i].ToolCallID
			}
		}
	}

	return out, nil
}

// isNumericID 判断字符串是否为非负纯数字（某些 provider 使用整数作为 tool_call ID）。
func isNumericID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ─── Provider 名称常量 ─────────────────────────────────────────────
// 这些常量用于 transform 层的 provider 能力检测，不是 Provider 路由。
// Provider 路由必须通过 ProviderRegistry（SCAN-010）。

const (
	ProviderNameOpenAI = "openai"
	ProviderNameAnthropic = "anthropic"
	ProviderNameGemini = "gemini"
	ProviderNameOllama = "ollama"
	ProviderNameGroq = "groq"
)

// supportedImageTypes 列出各 provider 支持的图片 MIME 类型。
// 未列出的 provider 视为不支持任何图片。
var supportedImageTypes = map[string][]string{
	ProviderNameOpenAI: {"image/png", "image/jpeg", "image/gif", "image/webp"},
	ProviderNameAnthropic: {"image/png", "image/jpeg", "image/gif", "image/webp"},
}

// imageProviders 支持内联 base64 图片的 provider 前缀。
var imageProviders = []string{ProviderNameOpenAI, ProviderNameAnthropic}

// ImageFormatAdapter 处理图片格式差异：
// - 不支持图片的 provider：将图片替换为文本占位符
// - 支持图片但 MIME 类型不在白名单：替换为文本占位符
// - 支持图片且 MIME 匹配：保留原样
func ImageFormatAdapter(_ context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)
	applyImageFormatAdapter(out, targetProvider)
	return out, nil
}

// applyImageFormatAdapter 根据 provider 能力适配图片格式。
func applyImageFormatAdapter(msgs []message.Message, targetProvider string) {
	provider := strings.ToLower(targetProvider)

	// 检查是否为支持图片的 provider
	supported := false
	for _, prefix := range imageProviders {
		if strings.HasPrefix(provider, prefix) {
			supported = true
			break
		}
	}

	// 不支持图片：全部替换
	if !supported {
		for i := range msgs {
			for j := range msgs[i].Content {
				if msgs[i].Content[j].Type == message.ContentImage && msgs[i].Content[j].Image != nil {
					img := msgs[i].Content[j].Image
					placeholder := fmt.Sprintf("[Image: %s, %d bytes]", img.MediaType, len(img.Data))
					msgs[i].Content[j] = message.Content{
						Type: message.ContentText,
						Text: placeholder,
					}
				}
			}
		}
		return
	}

	// 支持图片：检查 MIME 类型白名单
	allowedTypes := getAllowedImageTypes(provider)
	for i := range msgs {
		for j := range msgs[i].Content {
			if msgs[i].Content[j].Type == message.ContentImage && msgs[i].Content[j].Image != nil {
				img := msgs[i].Content[j].Image
				if !isAllowedImageType(img.MediaType, allowedTypes) {
					placeholder := fmt.Sprintf("[Unsupported image: %s]", img.MediaType)
					msgs[i].Content[j] = message.Content{
						Type: message.ContentText,
						Text: placeholder,
					}
				}
			}
		}
	}
}

// getAllowedImageTypes 获取 provider 允许的图片 MIME 类型列表。
func getAllowedImageTypes(provider string) []string {
	for prefix, types := range supportedImageTypes {
		if strings.HasPrefix(provider, prefix) {
			return types
		}
	}
	return nil
}

// isAllowedImageType 检查 MIME 类型是否在白名单中。
func isAllowedImageType(mediaType string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	for _, t := range allowed {
		if strings.EqualFold(mediaType, t) {
			return true
		}
	}
	return false
}

// thinkingBlockProviders 各 provider 对思维块的处理方式。
type thinkingProviderConvention int

const (
	// thinkingOpenAI 使用 reasoning_content 字段（转为文本前缀）
	thinkingOpenAI thinkingProviderConvention = iota
	// thinkingAnthropic 使用 thinking 内容类型
	thinkingAnthropic
	// thinkingPassthrough 其他 provider 保持原样
	thinkingPassthrough
)

// classifyThinkingProvider 判断 provider 对思维块的约定。
func classifyThinkingProvider(targetProvider string) thinkingProviderConvention {
	p := strings.ToLower(targetProvider)
	if p == ProviderNameOpenAI {
		return thinkingOpenAI
	}
	if strings.HasPrefix(p, ProviderNameAnthropic) {
		return thinkingAnthropic
	}
	return thinkingPassthrough
}

// ThinkingBlockAdapterEnhanced 增强版思维块适配：
// - OpenAI: ContentThinking → ContentText，加 "[Thinking] " 前缀
// - Anthropic: ContentText 以 "[Thinking] " 开头 → ContentThinking
// - 其他 provider: 保持原样
func ThinkingBlockAdapterEnhanced(_ context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)
	applyThinkingBlockAdapterEnhanced(out, targetProvider)
	return out, nil
}

// applyThinkingBlockAdapterEnhanced 增强版思维块适配逻辑。
func applyThinkingBlockAdapterEnhanced(msgs []message.Message, targetProvider string) {
	convention := classifyThinkingProvider(targetProvider)

	for i := range msgs {
		for j := range msgs[i].Content {
			switch convention {
			case thinkingOpenAI:
				// OpenAI: 思维块 → 文本前缀
				if msgs[i].Content[j].Type == message.ContentThinking {
					msgs[i].Content[j] = message.Content{
						Type: message.ContentText,
						Text: "[Thinking] " + msgs[i].Content[j].Thinking,
					}
				}
			case thinkingAnthropic:
				// Anthropic: 文本前缀 → 思维块
				if msgs[i].Content[j].Type == message.ContentText &&
					strings.HasPrefix(msgs[i].Content[j].Text, "[Thinking] ") {
					msgs[i].Content[j] = message.Content{
						Type: message.ContentThinking,
						Thinking: strings.TrimPrefix(msgs[i].Content[j].Text, "[Thinking] "),
					}
				}
			case thinkingPassthrough:
				// 其他 provider: 不做转换
			}
		}
	}
}

// systemMessageProviders 列出需要将系统消息合并到用户消息的 provider 前缀。
var systemMessageProviders = []string{ProviderNameOllama, ProviderNameGroq}

// SystemMessageAdapter 归一化系统消息处理：
// - 支持 system role 的 provider：保留原样；空系统消息被移除
// - 不支持 system role 的 provider：将系统消息内容前置到第一条用户消息
// - 若无用户消息，系统消息角色变为 user
func SystemMessageAdapter(_ context.Context, msgs []message.Message, targetProvider string) ([]message.Message, error) {
	out := deepCopyMessages(msgs)
	return applySystemMessageAdapter(out, targetProvider), nil
}

// applySystemMessageAdapter 根据目标 provider 处理系统消息，返回新切片。
func applySystemMessageAdapter(msgs []message.Message, targetProvider string) []message.Message {
	provider := strings.ToLower(targetProvider)

	// 先移除空的系统消息（所有 provider 共用）
	msgs = removeEmptySystemMessages(msgs)

	// 判断是否需要合并系统消息
	needsMerge := false
	for _, prefix := range systemMessageProviders {
		if strings.HasPrefix(provider, prefix) {
			needsMerge = true
			break
		}
	}
	if !needsMerge {
		return msgs
	}

	// 收集所有系统消息文本
	var systemTexts []string
	var remaining []message.Message
	for i := range msgs {
		if msgs[i].Role == message.RoleSystem {
			for _, c := range msgs[i].Content {
				if c.Type == message.ContentText && c.Text != "" {
					systemTexts = append(systemTexts, c.Text)
				}
			}
		} else {
			remaining = append(remaining, msgs[i])
		}
	}

	if len(systemTexts) == 0 {
		return remaining
	}

	combinedSystem := strings.Join(systemTexts, "\n\n")

	// 将系统内容前置到第一条用户消息
	merged := false
	for i := range remaining {
		if remaining[i].Role == message.RoleUser && !merged {
			prefix := message.Content{
				Type: message.ContentText,
				Text: combinedSystem,
			}
			remaining[i].Content = append([]message.Content{prefix}, remaining[i].Content...)
			merged = true
			break
		}
	}

	// 若无用户消息，将系统消息变为用户消息
	if !merged {
		remaining = append([]message.Message{{
			Role: message.RoleUser,
			Content: []message.Content{{Type: message.ContentText, Text: combinedSystem}},
		}}, remaining...)
	}

	return remaining
}

// removeEmptySystemMessages 移除空的系统消息，返回新切片。
func removeEmptySystemMessages(msgs []message.Message) []message.Message {
	filtered := msgs[:0]
	for i := range msgs {
		if msgs[i].Role == message.RoleSystem {
			if isEmptyMessage(msgs[i]) {
				continue
			}
		}
		filtered = append(filtered, msgs[i])
	}
	return filtered
}

// isEmptyMessage 判断消息是否为空（无内容或内容全为空文本）。
func isEmptyMessage(m message.Message) bool {
	if len(m.Content) == 0 {
		return true
	}
	for _, c := range m.Content {
		if c.Type == message.ContentText && strings.TrimSpace(c.Text) != "" {
			return false
		}
		if c.Type == message.ContentThinking && c.Thinking != "" {
			return false
		}
		if c.Type == message.ContentImage && c.Image != nil {
			return false
		}
	}
	return true
}
