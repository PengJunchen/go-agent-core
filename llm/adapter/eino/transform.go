// Package eino 提供基于 Eino 框架的 ModelProvider 适配器。
package eino

import (
	"encoding/json"

	"github.com/cloudwego/eino/schema"

	"github.com/pengjunchen/go-agent-core/llm/message"
)

// ---------------------------------------------------------------------------
// ToEinoMessages: message.Message -> []*schema.Message
// ---------------------------------------------------------------------------

// ToEinoMessages 将 go-agent-core 的 []message.Message 转换为 Eino 的 []*schema.Message。
func ToEinoMessages(msgs []message.Message) []*schema.Message {
	if msgs == nil {
		return nil
	}
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		converted := ToEinoMessage(m)
		if converted != nil {
			out = append(out, converted)
		}
	}
	return out
}

// ToEinoMessage 将单个 message.Message 转换为 *schema.Message。
func ToEinoMessage(m message.Message) *schema.Message {
	out := &schema.Message{
		Role: toEinoRole(m.Role),
		Name: m.Name,
		ToolCallID: m.ToolCallID,
		Extra: make(map[string]any),
	}

	// 处理 Content 块
	var textParts []string
	for _, c := range m.Content {
		switch c.Type {
		case message.ContentText:
			if c.Text != "" {
				textParts = append(textParts, c.Text)
			}
		case message.ContentThinking:
			// 将思维块写入 ReasoningContent
			out.ReasoningContent = c.Thinking
		case message.ContentImage:
			if c.Image != nil {
				out.UserInputMultiContent = append(out.UserInputMultiContent, toEinoImageInput(c.Image))
			}
		}
	}

	// 拼接文本
	if len(textParts) > 0 {
		out.Content = concatText(textParts)
	}

	// 处理 ToolCalls
	if len(m.ToolCalls) > 0 {
		out.ToolCalls = toEinoToolCalls(m.ToolCalls)
	}

	return out
}

// toEinoRole 将 message.Role 映射为 schema.RoleType。
func toEinoRole(r message.Role) schema.RoleType {
	switch r {
	case message.RoleSystem:
		return schema.System
	case message.RoleUser:
		return schema.User
	case message.RoleAssistant:
		return schema.Assistant
	case message.RoleTool:
		return schema.Tool
	default:
		return schema.User
	}
}

// toEinoImageInput 将 *message.Image 转换为 *schema.MessageInputPart（图片类型）。
func toEinoImageInput(img *message.Image) schema.MessageInputPart {
	data := img.Data
	mimeType := img.MediaType
	if mimeType == "" {
		mimeType = "image/png"
	}
	return schema.MessageInputPart{
		Type: schema.ChatMessagePartTypeImageURL,
		Image: &schema.MessageInputImage{
			MessagePartCommon: schema.MessagePartCommon{
				Base64Data: &data,
				MIMEType: mimeType,
			},
		},
	}
}

// toEinoToolCalls 将 []message.ToolCall 转换为 []schema.ToolCall。
func toEinoToolCalls(tcs []message.ToolCall) []schema.ToolCall {
	out := make([]schema.ToolCall, 0, len(tcs))
	for _, tc := range tcs {
		args := ""
		if len(tc.Arguments) > 0 {
			b, _ := json.Marshal(tc.Arguments)
			args = string(b)
		}
		out = append(out, schema.ToolCall{
			ID: tc.ID,
			Type: "function",
			Function: schema.FunctionCall{
				Name: tc.Name,
				Arguments: args,
			},
		})
	}
	return out
}

// concatText 拼接多个文本块。
func concatText(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		total := 0
		for _, p := range parts {
			total += len(p)
		}
		buf := make([]byte, 0, total)
		for _, p := range parts {
			buf = append(buf, p...)
		}
		return string(buf)
	}
}

// ---------------------------------------------------------------------------
// FromEinoMessage: *schema.Message -> message.Message
// ---------------------------------------------------------------------------

// FromEinoMessage 将 Eino 的 *schema.Message 转换为 go-agent-core 的 message.Message。
func FromEinoMessage(einoMsg *schema.Message) *message.Message {
	if einoMsg == nil {
		return nil
	}

	m := &message.Message{
		Role: fromEinoRole(einoMsg.Role),
		Name: einoMsg.Name,
		ToolCallID: einoMsg.ToolCallID,
	}

	// 1. 处理 Content 字段（非流式返回的完整文本）
	if einoMsg.Content != "" {
		m.Content = append(m.Content, message.Content{
			Type: message.ContentText,
			Text: einoMsg.Content,
		})
	}

	// 2. 处理 ReasoningContent 字段
	if einoMsg.ReasoningContent != "" {
		m.Content = append(m.Content, message.Content{
			Type: message.ContentThinking,
			Thinking: einoMsg.ReasoningContent,
		})
	}

	// 3. 处理 AssistantGenMultiContent（多内容输出）
	for _, part := range einoMsg.AssistantGenMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			if part.Text != "" {
				m.Content = append(m.Content, message.Content{
					Type: message.ContentText,
					Text: part.Text,
				})
			}
		case schema.ChatMessagePartTypeReasoning:
			if part.Reasoning != nil && part.Reasoning.Text != "" {
				m.Content = append(m.Content, message.Content{
					Type: message.ContentThinking,
					Thinking: part.Reasoning.Text,
				})
			}
		}
	}

	// 4. 处理工具调用
	if len(einoMsg.ToolCalls) > 0 {
		m.ToolCalls = fromEinoToolCalls(einoMsg.ToolCalls)
	}

	return m
}

// fromEinoRole 将 schema.RoleType 映射为 message.Role。
func fromEinoRole(r schema.RoleType) message.Role {
	switch r {
	case schema.System:
		return message.RoleSystem
	case schema.User:
		return message.RoleUser
	case schema.Assistant:
		return message.RoleAssistant
	case schema.Tool:
		return message.RoleTool
	default:
		return message.RoleUser
	}
}

// fromEinoToolCalls 将 []schema.ToolCall 转换为 []message.ToolCall。
func fromEinoToolCalls(tcs []schema.ToolCall) []message.ToolCall {
	out := make([]message.ToolCall, 0, len(tcs))
	for _, tc := range tcs {
		args := make(map[string]any)
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		out = append(out, message.ToolCall{
			ID: tc.ID,
			Name: tc.Function.Name,
			Arguments: args,
		})
	}
	return out
}

// compile-time check
var _ = FromEinoMessage
var _ = ToEinoMessages
