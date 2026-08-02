// Package eino 提供基于 Eino 框架的 ModelProvider 适配器。
//
// EinoProvider 封装 model.BaseChatModel 实现 provider.ModelProvider 接口。
// 使用时构造 EinoProvider 后注册到 registry.DefaultRegistry 即可。
package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// EinoProvider 适配 Eino 的 BaseChatModel 到 ModelProvider 接口。
type EinoProvider struct {
	chatModel model.BaseChatModel
	modelInfo *provider.ModelInfo
}

// NewEinoProvider 创建 EinoProvider 实例。
//
// chatModel 是一个 Eino 的 BaseChatModel 实现（如 openai.NewChatModel 的结果）。
// providerName 是提供者名称（如 "openai"、"gemini"）。
// modelName 是模型名称（如 "gpt-4o"、"gemini-2.5-pro"）。
// maxTokens 是该模型的最大输出 token 数，0 表示未知。
func NewEinoProvider(chatModel model.BaseChatModel, providerName, modelName string, maxTokens int) *EinoProvider {
	return &EinoProvider{
		chatModel: chatModel,
		modelInfo: &provider.ModelInfo{
			Provider: providerName,
			ModelName: modelName,
			MaxTokens: maxTokens,
			SupportsStreaming: true,
		},
	}
}

// StreamChat 流式聊天，将 []message.Message 转为 Eino 消息后调用 Stream，
// 返回 <-chan stream.StreamEvent。
func (p *EinoProvider) StreamChat(ctx context.Context, msgs []message.Message, opts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	einoMsgs := ToEinoMessages(msgs)
	einoOpts := toEinoOptions(opts)

	reader, err := p.chatModel.Stream(ctx, einoMsgs, einoOpts...)
	if err != nil {
		return nil, fmt.Errorf("eino stream: %w", err)
	}

	return pumpStream(reader), nil
}

// pumpStream 从 schema.StreamReader[*schema.Message] 持续拉取消息并产出 StreamEvent。
//
// 流式模式下，OpenAI 等协议的 tool call arguments 是增量的（每次 chunk 只包含
// 部分 arguments）。如果直接对每个 chunk 发射 StreamToolCallStart，会导致：
// - 不完整的 arguments 被解析为空 map
// - 同一个 tool call 被多次发射
// - 重建消息时 arguments 格式错误（如 {"raw":"..."}）
//
// 因此 pumpStream 对 tool call 做累积合并：只在流结束时（StreamDone）
// 发射完整的 StreamToolCallStart 事件。文本和思维增量则实时发射。
func pumpStream(reader *schema.StreamReader[*schema.Message]) <-chan stream.StreamEvent {
	eventCh := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(eventCh)
		defer reader.Close()

		// 累积 tool calls（按 ID 合并增量 arguments）
		var pendingToolCalls []schema.ToolCall

		for {
			chunk, err := reader.Recv()
			if err != nil {
				if err == io.EOF {
					// 流结束：发射累积的 tool call 事件
					emitPendingToolCalls(eventCh, pendingToolCalls)
					eventCh <- stream.StreamEvent{Type: stream.StreamDone}
					return
				}
				eventCh <- stream.StreamEvent{Type: stream.StreamError, Error: err}
				return
			}
			if chunk == nil {
				continue
			}

			// 处理文本和思维增量（实时发射）
			emitTextAndThinking(eventCh, chunk)

			// 累积 tool calls（不发射，等流结束）
			if len(chunk.ToolCalls) > 0 {
				pendingToolCalls = mergeToolCalls(pendingToolCalls, chunk.ToolCalls)
			}
		}
	}()
	return eventCh
}

// emitPendingToolCalls 在流结束时发射完整的 tool call 事件。
func emitPendingToolCalls(ch chan<- stream.StreamEvent, toolCalls []schema.ToolCall) {
	for _, tc := range toolCalls {
		args := make(map[string]any)
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args) // tool call args 容错解析
		}
		ch <- stream.StreamEvent{
			Type: stream.StreamToolCallStart,
			ToolCall: &stream.ToolCall{
				ID: tc.ID,
				Name: tc.Function.Name,
				Arguments: args,
			},
		}
	}
}

// mergeToolCalls 合并增量 tool calls（按 ID 累积 arguments）。
//
// OpenAI 流式协议中，tool call 的 arguments 是分段传输的：
//
//	chunk 1: ToolCalls[0] = {ID:"call_1", Function:{Name:"read_file", Arguments:"{\"pa"}}
//	chunk 2: ToolCalls[0] = {ID:"call_1", Function:{Name:"", Arguments:"th\":"}}
//	chunk 3: ToolCalls[0] = {ID:"call_1", Function:{Name:"", Arguments:"\"/tmp\"}"}}
//
// 合并策略：按 ID 匹配，Arguments 字符串追加，Name 取首次非空值。
func mergeToolCalls(existing []schema.ToolCall, incoming []schema.ToolCall) []schema.ToolCall {
	for _, inc := range incoming {
		if inc.ID == "" {
			continue
		}
		found := false
		for i := range existing {
			if existing[i].ID == inc.ID {
				// 合并：追加 arguments
				existing[i].Function.Arguments += inc.Function.Arguments
				// Name：取首次非空值
				if existing[i].Function.Name == "" && inc.Function.Name != "" {
					existing[i].Function.Name = inc.Function.Name
				}
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, inc)
		}
	}
	return existing
}

// emitTextAndThinking 仅发射文本和思维增量（不发射 tool call）。
func emitTextAndThinking(ch chan<- stream.StreamEvent, msg *schema.Message) {
	if msg == nil {
		return
	}

	// 1. 文本增量
	if msg.Content != "" {
		ch <- stream.StreamEvent{
			Type: stream.StreamTextDelta,
			Content: msg.Content,
		}
	}

	// 2. 思维增量
	if msg.ReasoningContent != "" {
		ch <- stream.StreamEvent{
			Type: stream.StreamThinkingDelta,
			Thinking: msg.ReasoningContent,
		}
	}

	// 3. 多内容输出块
	for _, part := range msg.AssistantGenMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			if part.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamTextDelta,
					Content: part.Text,
				}
			}
		case schema.ChatMessagePartTypeReasoning:
			if part.Reasoning != nil && part.Reasoning.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamThinkingDelta,
					Thinking: part.Reasoning.Text,
				}
			}
		}
	}
}

// Generate 同步生成，将 []message.Message 转为 Eino 消息后调用 Generate。
func (p *EinoProvider) Generate(ctx context.Context, msgs []message.Message, opts *provider.ChatOptions) (*message.Message, error) {
	einoMsgs := ToEinoMessages(msgs)
	einoOpts := toEinoOptions(opts)

	result, err := p.chatModel.Generate(ctx, einoMsgs, einoOpts...)
	if err != nil {
		return nil, fmt.Errorf("eino generate: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("eino generate returned nil message")
	}

	return FromEinoMessage(result), nil
}

// ModelInfo 返回模型的元信息。
func (p *EinoProvider) ModelInfo() *provider.ModelInfo {
	return p.modelInfo
}

// compile-time interface check
var _ provider.ModelProvider = (*EinoProvider)(nil)

// toEinoOptions 将 provider.ChatOptions 转换为 Eino model.Option 列表。
func toEinoOptions(opts *provider.ChatOptions) []model.Option {
	if opts == nil {
		return nil
	}

	var result []model.Option

	if opts.Temperature != nil {
		t := float32(*opts.Temperature)
		result = append(result, model.WithTemperature(t))
	}

	if opts.MaxTokens != nil {
		result = append(result, model.WithMaxTokens(*opts.MaxTokens))
	}

	if len(opts.StopSequences) > 0 {
		result = append(result, model.WithStop(opts.StopSequences))
	}

	if len(opts.Tools) > 0 {
		result = append(result, model.WithTools(toEinoToolInfos(opts.Tools)))
	}

	if opts.ToolChoice != nil {
		result = append(result, model.WithToolChoice(toEinoToolChoice(opts.ToolChoice)))
	}

	// GAP-1: ResponseFormat → OpenAI response_format via extra fields.
	if opts.ResponseFormat != nil && opts.ResponseFormat.Type == provider.ConstrainedJSONSchema {
		result = append(result, toResponseFormatOption(opts.ResponseFormat))
	}

	// GAP-2: ThinkingMode → OpenAI reasoning_effort.
	if opts.ThinkingMode != nil && opts.ThinkingMode.Enabled {
		result = append(result, toThinkingModeOption(opts.ThinkingMode))
	}

	return result
}

// toResponseFormatOption 将 provider.ResponseFormat 转换为 Eino model.Option。
//
// 仅支持 json_schema 模式，通过 OpenAI WithExtraFields 将 response_format
// 注入请求体。grammar 模式需要 provider-specific 后端（如 llama.cpp GBNF），
// 暂未实现。
func toResponseFormatOption(rf *provider.ResponseFormat) model.Option {
	rfMap := map[string]any{
		"type": string(rf.Type),
	}
	if rf.JSONSchema != nil {
		name := "response"
		if n, ok := rf.JSONSchema["title"].(string); ok && n != "" {
			name = n
		}
		rfMap["json_schema"] = map[string]any{
			"name": name,
			"schema": rf.JSONSchema,
			"strict": true,
		}
	}
	return openai.WithExtraFields(map[string]any{"response_format": rfMap})
}

// toThinkingModeOption 将 provider.ThinkingConfig 转换为 Eino model.Option。
//
// 将 Budget 映射到 OpenAI reasoning_effort 级别：
// - Budget ≤ 4096 → low
// - Budget ≤ 16384 → medium
// - Budget > 16384 → high
// - Budget == 0 → high（不限预算视为最大思考力度）
//
// TODO: Anthropic agentic model 的 extended thinking（budget_tokens）
// 需在 agenticclaude 适配器集成后通过 impl-specific option 传递。
func toThinkingModeOption(tc *provider.ThinkingConfig) model.Option {
	effort := openai.ReasoningEffortLevelMedium
	if tc.Budget > 0 {
		if tc.Budget <= 4096 {
			effort = openai.ReasoningEffortLevelLow
		} else if tc.Budget > 16384 {
			effort = openai.ReasoningEffortLevelHigh
		}
	} else {
		effort = openai.ReasoningEffortLevelHigh
	}
	return openai.WithReasoningEffort(effort)
}

// toEinoToolInfos 将 []provider.ToolSpec 转换为 []*schema.ToolInfo。
func toEinoToolInfos(tools []provider.ToolSpec) []*schema.ToolInfo {
	infos := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		ti := &schema.ToolInfo{
			Name: t.Name,
			Desc: t.Description,
		}
		// 如果提供了参数，构造简化的 ParameterInfo
		if len(t.Parameters) > 0 {
			params := make(map[string]*schema.ParameterInfo)
			for k, v := range t.Parameters {
				paramType := inferDataType(v)
				params[k] = &schema.ParameterInfo{
					Type: paramType,
					Desc: "",
				}
			}
			ti.ParamsOneOf = schema.NewParamsOneOfByParams(params)
		}
		infos = append(infos, ti)
	}
	return infos
}

// inferDataType 从 Go 值推断 schema.DataType。
func inferDataType(v any) schema.DataType {
	if v == nil {
		return schema.String
	}
	switch v.(type) {
	case string:
		return schema.String
	case float64, float32:
		return schema.Number
	case int, int32, int64:
		return schema.Integer
	case bool:
		return schema.Boolean
	case []any:
		return schema.Array
	case map[string]any:
		return schema.Object
	default:
		return schema.String
	}
}

// toEinoToolChoice 将 *provider.ToolChoiceConfig 转换为 schema.ToolChoice。
func toEinoToolChoice(cfg *provider.ToolChoiceConfig) schema.ToolChoice {
	if cfg == nil {
		return schema.ToolChoiceAllowed
	}
	switch cfg.Mode {
	case provider.ToolChoiceNone:
		return schema.ToolChoiceForbidden
	case provider.ToolChoiceSpecific:
		return schema.ToolChoiceForced
	default:
		return schema.ToolChoiceAllowed
	}
}

// emitEvents 将单个 Eino schema.Message 块转换为零到多个 StreamEvent 并发送到 ch。
func emitEvents(ch chan<- stream.StreamEvent, msg *schema.Message) {
	if msg == nil {
		return
	}

	// 1. 文本增量 (Content 字段)
	if msg.Content != "" {
		ch <- stream.StreamEvent{
			Type: stream.StreamTextDelta,
			Content: msg.Content,
		}
	}

	// 2. 思维增量
	if msg.ReasoningContent != "" {
		ch <- stream.StreamEvent{
			Type: stream.StreamThinkingDelta,
			Thinking: msg.ReasoningContent,
		}
	}

	// 3. 多内容输出块（AssistantGenMultiContent）
	for _, part := range msg.AssistantGenMultiContent {
		switch part.Type {
		case schema.ChatMessagePartTypeText:
			if part.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamTextDelta,
					Content: part.Text,
				}
			}
		case schema.ChatMessagePartTypeReasoning:
			if part.Reasoning != nil && part.Reasoning.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamThinkingDelta,
					Thinking: part.Reasoning.Text,
				}
			}
		}
	}

	// 4. 工具调用
	for _, tc := range msg.ToolCalls {
		args := make(map[string]any)
		if tc.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &args) // tool call args 容错解析
		}
		ch <- stream.StreamEvent{
			Type: stream.StreamToolCallStart,
			ToolCall: &stream.ToolCall{
				ID: tc.ID,
				Name: tc.Function.Name,
				Arguments: args,
			},
		}
	}
}

// ---------------------------------------------------------------------------
// EinoAgenticProvider — 适配 Eino AgenticModel（如 Anthropic）
// ---------------------------------------------------------------------------

// EinoAgenticProvider 适配 Eino 的 AgenticModel（如 Anthropic Claude）到 ModelProvider 接口。
type EinoAgenticProvider struct {
	agenticModel model.AgenticModel
	modelInfo *provider.ModelInfo
}

// NewEinoAgenticProvider 创建 EinoAgenticProvider 实例。
func NewEinoAgenticProvider(agenticModel model.AgenticModel, providerName, modelName string, maxTokens int) *EinoAgenticProvider {
	return &EinoAgenticProvider{
		agenticModel: agenticModel,
		modelInfo: &provider.ModelInfo{
			Provider: providerName,
			ModelName: modelName,
			MaxTokens: maxTokens,
			SupportsStreaming: true,
		},
	}
}

// StreamChat 实现 provider.ModelProvider 流式接口。
func (p *EinoAgenticProvider) StreamChat(ctx context.Context, msgs []message.Message, opts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	agenticMsgs := toAgenticMessages(msgs)
	einoOpts := toEinoOptions(opts)

	reader, err := p.agenticModel.Stream(ctx, agenticMsgs, einoOpts...)
	if err != nil {
		return nil, fmt.Errorf("agentic stream: %w", err)
	}

	return pumpAgenticStream(reader), nil
}

// Generate 实现 provider.ModelProvider 同步接口。
func (p *EinoAgenticProvider) Generate(ctx context.Context, msgs []message.Message, opts *provider.ChatOptions) (*message.Message, error) {
	agenticMsgs := toAgenticMessages(msgs)
	einoOpts := toEinoOptions(opts)

	result, err := p.agenticModel.Generate(ctx, agenticMsgs, einoOpts...)
	if err != nil {
		return nil, fmt.Errorf("agentic generate: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("agentic generate returned nil message")
	}

	return fromAgenticMessage(result), nil
}

// ModelInfo 返回模型的元信息。
func (p *EinoAgenticProvider) ModelInfo() *provider.ModelInfo {
	return p.modelInfo
}

// ---------------------------------------------------------------------------
// AgenticMessage 转换辅助
// ---------------------------------------------------------------------------

// toAgenticMessages 将 []message.Message 转换为 []*schema.AgenticMessage。
func toAgenticMessages(msgs []message.Message) []*schema.AgenticMessage {
	if msgs == nil {
		return nil
	}
	out := make([]*schema.AgenticMessage, 0, len(msgs))
	for _, m := range msgs {
		converted := toAgenticMessage(m)
		if converted != nil {
			out = append(out, converted)
		}
	}
	return out
}

func toAgenticMessage(m message.Message) *schema.AgenticMessage {
	role := toAgenticRole(m.Role)
	var blocks []*schema.ContentBlock

	for _, c := range m.Content {
		switch c.Type {
		case message.ContentText:
			blocks = append(blocks, &schema.ContentBlock{
				Type: schema.ContentBlockTypeUserInputText,
				UserInputText: &schema.UserInputText{
					Text: c.Text,
				},
			})
		case message.ContentThinking:
			blocks = append(blocks, &schema.ContentBlock{
				Type: schema.ContentBlockTypeReasoning,
				Reasoning: &schema.Reasoning{
					Text: c.Thinking,
				},
			})
		}
	}

	for _, tc := range m.ToolCalls {
		blocks = append(blocks, &schema.ContentBlock{
			Type: schema.ContentBlockTypeFunctionToolCall,
			FunctionToolCall: &schema.FunctionToolCall{
				CallID: tc.ID,
				Name: tc.Name,
				Arguments: marshalJSONArgs(tc.Arguments),
			},
		})
	}

	return &schema.AgenticMessage{
		Role: role,
		ContentBlocks: blocks,
	}
}

// fromAgenticMessage 将 *schema.AgenticMessage 转换为 *message.Message。
func fromAgenticMessage(m *schema.AgenticMessage) *message.Message {
	if m == nil {
		return nil
	}

	out := &message.Message{
		Role: fromAgenticRole(m.Role),
	}

	for _, block := range m.ContentBlocks {
		if block == nil {
			continue
		}
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText != nil && block.AssistantGenText.Text != "" {
				out.Content = append(out.Content, message.Content{
					Type: message.ContentText,
					Text: block.AssistantGenText.Text,
				})
			}
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning != nil && block.Reasoning.Text != "" {
				out.Content = append(out.Content, message.Content{
					Type: message.ContentThinking,
					Thinking: block.Reasoning.Text,
				})
			}
		case schema.ContentBlockTypeFunctionToolCall:
			if block.FunctionToolCall != nil {
				fc := block.FunctionToolCall
				args := make(map[string]any)
				if fc.Arguments != "" {
					_ = json.Unmarshal([]byte(fc.Arguments), &args) // tool call args 容错解析 // tool call args 容错解析
				}
				out.ToolCalls = append(out.ToolCalls, message.ToolCall{
					ID: fc.CallID,
					Name: fc.Name,
					Arguments: args,
				})
			}
		}
	}

	return out
}

// pumpAgenticStream 从 *schema.StreamReader[*schema.AgenticMessage] 持续拉取消息并产出 StreamEvent。
//
// 与 pumpStream 类似，对 tool call 做累积合并，避免增量 arguments 导致格式错误。
func pumpAgenticStream(reader *schema.StreamReader[*schema.AgenticMessage]) <-chan stream.StreamEvent {
	eventCh := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(eventCh)
		defer reader.Close()

		// 累积 tool calls（按 CallID 合并增量 arguments）
		var pendingToolCalls []agenticToolCallAccum

		for {
			chunk, err := reader.Recv()
			if err != nil {
				if err == io.EOF {
					// 流结束：发射累积的 tool call 事件
					emitPendingAgenticToolCalls(eventCh, pendingToolCalls)
					eventCh <- stream.StreamEvent{Type: stream.StreamDone}
					return
				}
				eventCh <- stream.StreamEvent{Type: stream.StreamError, Error: err}
				return
			}
			if chunk == nil {
				continue
			}

			// 处理文本和思维增量（实时发射）
			emitAgenticTextAndThinking(eventCh, chunk)

			// 累积 tool calls
			for _, block := range chunk.ContentBlocks {
				if block != nil && block.Type == schema.ContentBlockTypeFunctionToolCall && block.FunctionToolCall != nil {
					pendingToolCalls = mergeAgenticToolCall(pendingToolCalls, block.FunctionToolCall)
				}
			}
		}
	}()
	return eventCh
}

// agenticToolCallAccum 累积 Agentic 模式的 tool call。
type agenticToolCallAccum struct {
	CallID string
	Name string
	Arguments string // 累积的原始 JSON 字符串
}

// mergeAgenticToolCall 合并增量 agentic tool call。
func mergeAgenticToolCall(existing []agenticToolCallAccum, fc *schema.FunctionToolCall) []agenticToolCallAccum {
	if fc == nil || fc.CallID == "" {
		return existing
	}
	for i := range existing {
		if existing[i].CallID == fc.CallID {
			existing[i].Arguments += fc.Arguments
			if existing[i].Name == "" && fc.Name != "" {
				existing[i].Name = fc.Name
			}
			return existing
		}
	}
	return append(existing, agenticToolCallAccum{
		CallID: fc.CallID,
		Name: fc.Name,
		Arguments: fc.Arguments,
	})
}

// emitPendingAgenticToolCalls 在流结束时发射完整的 agentic tool call 事件。
func emitPendingAgenticToolCalls(ch chan<- stream.StreamEvent, toolCalls []agenticToolCallAccum) {
	for _, tc := range toolCalls {
		args := make(map[string]any)
		if tc.Arguments != "" {
			_ = json.Unmarshal([]byte(tc.Arguments), &args) // tool call args 容错解析
		}
		ch <- stream.StreamEvent{
			Type: stream.StreamToolCallStart,
			ToolCall: &stream.ToolCall{
				ID: tc.CallID,
				Name: tc.Name,
				Arguments: args,
			},
		}
	}
}

// emitAgenticTextAndThinking 仅发射 Agentic 消息的文本和思维增量。
func emitAgenticTextAndThinking(ch chan<- stream.StreamEvent, msg *schema.AgenticMessage) {
	if msg == nil {
		return
	}
	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText != nil && block.AssistantGenText.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamTextDelta,
					Content: block.AssistantGenText.Text,
				}
			}
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning != nil && block.Reasoning.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamThinkingDelta,
					Thinking: block.Reasoning.Text,
				}
			}
		}
	}
}

// emitAgenticEvents 将单个 AgenticMessage 块转换为零到多个 StreamEvent。
func emitAgenticEvents(ch chan<- stream.StreamEvent, msg *schema.AgenticMessage) {
	if msg == nil {
		return
	}

	for _, block := range msg.ContentBlocks {
		if block == nil {
			continue
		}
		switch block.Type {
		case schema.ContentBlockTypeAssistantGenText:
			if block.AssistantGenText != nil && block.AssistantGenText.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamTextDelta,
					Content: block.AssistantGenText.Text,
				}
			}
		case schema.ContentBlockTypeReasoning:
			if block.Reasoning != nil && block.Reasoning.Text != "" {
				ch <- stream.StreamEvent{
					Type: stream.StreamThinkingDelta,
					Thinking: block.Reasoning.Text,
				}
			}
		case schema.ContentBlockTypeFunctionToolCall:
			if block.FunctionToolCall != nil {
				fc := block.FunctionToolCall
				args := make(map[string]any)
				if fc.Arguments != "" {
					_ = json.Unmarshal([]byte(fc.Arguments), &args) // tool call args 容错解析
				}
				ch <- stream.StreamEvent{
					Type: stream.StreamToolCallStart,
					ToolCall: &stream.ToolCall{
						ID: fc.CallID,
						Name: fc.Name,
						Arguments: args,
					},
				}
			}
		}
	}
}

func toAgenticRole(r message.Role) schema.AgenticRoleType {
	switch r {
	case message.RoleSystem:
		return schema.AgenticRoleTypeSystem
	case message.RoleUser:
		return schema.AgenticRoleTypeUser
	case message.RoleAssistant:
		return schema.AgenticRoleTypeAssistant
	default:
		return schema.AgenticRoleTypeUser
	}
}

func fromAgenticRole(r schema.AgenticRoleType) message.Role {
	switch r {
	case schema.AgenticRoleTypeSystem:
		return message.RoleSystem
	case schema.AgenticRoleTypeUser:
		return message.RoleUser
	case schema.AgenticRoleTypeAssistant:
		return message.RoleAssistant
	default:
		return message.RoleUser
	}
}

func marshalJSONArgs(v map[string]any) string {
	if len(v) == 0 {
		return "{}" // Anthropic/OpenAI 要求 arguments 必须是合法 JSON object
	}
	b, _ := json.Marshal(v)
	return string(b)
}
