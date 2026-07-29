// Package message 定义跨 provider 的消息与内容块类型。
//
// 这些类型是 L4 协议层的通用货币：ModelProvider 接收 []Message、
// 返回 Message，转换层负责在通用类型与 provider 私有格式间互转。
// 类型设计 AgentMessage 联合类型。
package message

// Role 标识消息的发言角色。
type Role string

const (
	// RoleSystem 系统指令。
	RoleSystem Role = "system"
	// RoleUser 用户消息。
	RoleUser Role = "user"
	// RoleAssistant 助手消息。
	RoleAssistant Role = "assistant"
	// RoleTool 工具结果。
	RoleTool Role = "tool"
)

// Message 是一条对话消息。
type Message struct {
	Role Role
	Content []Content
	ToolCalls []ToolCall
	ToolCallID string // Role == RoleTool 时关联的调用 ID
	Name string // 可选的发言者名称
}

// Content 是消息内容的联合类型，按 Type 区分。
type Content struct {
	Type ContentType
	Text string // Type == ContentText
	Thinking string // Type == ContentThinking
	Image *Image // Type == ContentImage
}

// ContentType 枚举内容块类型。
type ContentType int

const (
	// ContentText 文本块。
	ContentText ContentType = iota
	// ContentThinking 思维块。
	ContentThinking
	// ContentImage 图片块。
	ContentImage
)

// Image 是图片内容块。
type Image struct {
	Data string // base64 编码
	MediaType string // 如 "image/png"
}

// ToolCall 是助手消息中发起的工具调用。
type ToolCall struct {
	ID string
	Name string
	Arguments map[string]any
}

// Usage 描述一次 LLM 调用的 token 用量与成本。
type Usage struct {
	Input int
	Output int
	CacheRead int
	CacheWrite int
	TotalTokens int
}

// NewTextMessage 快速构造纯文本消息。
func NewTextMessage(role Role, text string) Message {
	return Message{Role: role, Content: []Content{{Type: ContentText, Text: text}}}
}
