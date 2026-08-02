// Package compactor — SummaryCompactor: LLM 摘要压缩。
//
// SummaryCompactor 通过 ModelProvider 接口调用 LLM 生成对话历史的
// 结构化摘要，在保留关键上下文（文件操作、决策、最近轮次）的同时
// 大幅减少 token 数量。
package compactor

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// Compile-time check that SummaryCompactor implements memory/context.Compactor.
var _ memctx.Compactor = (*SummaryCompactor)(nil)

// defaultSummaryPrompt 是 Designer §4 定义的默认摘要提示词（fallback）。
const defaultSummaryPrompt = `You are a conversation summarizer. Your task is to summarize the following conversation history concisely while preserving key information.

Guidelines:
1. Retain all system instructions, user goals, and constraints
2. Keep a record of all tool calls made and their results
3. Preserve any decisions, conclusions, or outputs produced
4. Note any files created, modified, or read
5. Include error messages and failures if any occurred
6. Be concise but complete — the summary should be sufficient for an AI assistant to continue the conversation without losing context
7. Output only the summary text, no preamble or explanation

Conversation to summarize:

{conversation_text}

Summary:`

// defaultTemperature 是 LLM 调用的默认温度。
const defaultTemperature = 0.3

// defaultMaxTokens 是摘要生成的最大 token 数。
const defaultMaxTokens = 1024

// defaultMaxItemLength 是单个 TurnItem 内容的最大字符数，超过此长度的
// tool result 会被 splitTurn 拆分为多个小片段。
const defaultMaxItemLength = 2000

// fileOpTools 是操作文件路径的工具集，用于从对话中提取文件操作记录。
var fileOpTools = map[string]bool{
	"read_file": true,
	"write_file": true,
	"edit_file": true,
	"create_file": true,
	"delete_file": true,
	"overwrite_file": true,
	"search_in_file": true,
	"search_in_files": true,
	"list_directory": true,
	"find_files_by_name": true,
	"find_files_by_content": true,
}

// SummaryCompactor 通过 ModelProvider 调用 LLM 生成摘要压缩上下文。
//
// 构造时必须提供 provider.ModelProvider 和 TokenEstimator。
// 可通过 WithTemperature / WithSummaryPrompt / WithMaxTokens 链式覆写默认值。
//
// 压缩策略（
// 1. 分离系统项与对话项
// 2. 保留最近的 ~70% maxTokens 作为尾部（tail），保留原始细节
// 3. 对头部（head）调用 LLM 生成结构化摘要
// 4. 提取文件操作记录注入摘要提示
// 5. 组装结果：系统项 + 摘要项（Role=system, Metadata.compacted=true, before_tokens） + 尾部
type SummaryCompactor struct {
	model provider.ModelProvider // LLM 调用接口（必填）
	est memctx.TokenEstimator // token 估算器（必填）
	temperature float64 // LLM 温度，默认 0.3
	summaryPrompt string // 自定义提示词，空则用 defaultSummaryPrompt
	maxTokens int // 摘要生成的最大 token 数，默认 1024
	maxItemLength int // 单个 tool result 最大字符数，超过则拆分
}

// NewSummaryCompactor 构造 SummaryCompactor。
//
// model 是 LLM 调用接口，必须非 nil。
// est 是 token 估算器，nil 时使用默认 HeuristicTokenEstimator。
func NewSummaryCompactor(model provider.ModelProvider, est memctx.TokenEstimator) *SummaryCompactor {
	if est == nil {
		est = &HeuristicTokenEstimator{}
	}
	return &SummaryCompactor{
		model: model,
		est: est,
		temperature: defaultTemperature,
		maxTokens: defaultMaxTokens,
		maxItemLength: defaultMaxItemLength,
	}
}

// WithTemperature 设置 LLM 调用的温度值（默认 0.3）。
func (s *SummaryCompactor) WithTemperature(temp float64) *SummaryCompactor {
	s.temperature = temp
	return s
}

// WithSummaryPrompt 设置自定义摘要提示词。
// 提示词应包含 {conversation_text} 占位符。
func (s *SummaryCompactor) WithSummaryPrompt(prompt string) *SummaryCompactor {
	s.summaryPrompt = prompt
	return s
}

// WithMaxTokens 设置摘要生成的最大 token 数（默认 1024）。
func (s *SummaryCompactor) WithMaxTokens(maxTokens int) *SummaryCompactor {
	s.maxTokens = maxTokens
	return s
}

// WithMaxItemLength 设置单个 tool result 内容的最大字符数（默认 2000）。
// 超过此长度的 tool result 会被 splitTurn 拆分为多个小片段。
func (s *SummaryCompactor) WithMaxItemLength(maxItemLength int) *SummaryCompactor {
	s.maxItemLength = maxItemLength
	return s
}

// Compact 实现 memory/context.Compactor 接口。
//
// 用 LLM 对历史对话生成摘要，保留最近内容不压缩。
// 返回 CompactResult，其中 Summary 字段存放摘要原文，
// RetainedItems 存放系统项 + 摘要项 + 尾部保留项。
//
// 流程（
// 1. 输入校验
// 2. 分离 system items
// 3. 计算预算（tail ~70%）
// 4. 选取 tail
// 5. 处理 split turns（VC-002 规则）
// 6. 构建 prompt
// 7. 调用 provider.Generate()
// 8. 提取摘要文本
// 9. 组装摘要 item（Role=system, Metadata.compacted=true, before_tokens）
// 10. 计算并返回 CompactResult
func (sc *SummaryCompactor) Compact(ctx context.Context, items []memctx.TurnItem, maxTokens int) (*memctx.CompactResult, error) {
	if maxTokens <= 0 || len(items) == 0 {
		return &memctx.CompactResult{
			Strategy: memctx.CompactSummary,
			BeforeTokens: 0,
			AfterTokens: 0,
			ItemsRemoved: 0,
			RetainedItems: copyItems(items),
		}, nil
	}

	// 检查上下文取消。
	if ctx.Err() != nil {
		return nil, fmt.Errorf("summary compact: %w", ctx.Err())
	}

	beforeTokens := sc.est.EstimateFromItems(items)

	// 1. 分离系统项与对话项。
	var systemItems, convItems []memctx.TurnItem
	for _, item := range items {
		if item.Role == "system" {
			systemItems = append(systemItems, item)
		} else {
			convItems = append(convItems, item)
		}
	}

	if len(convItems) == 0 {
		result := copyItems(items)
		return &memctx.CompactResult{
			Strategy: memctx.CompactSummary,
			BeforeTokens: beforeTokens,
			AfterTokens: beforeTokens,
			ItemsRemoved: 0,
			RetainedItems: result,
		}, nil
	}

	// 2. 找到尾部：在 maxTokens 的 ~70% 内能容纳的最近轮次。
	tailBudget := maxTokens * 7 / 10
	tailStart := len(convItems)
	tailTokens := 0
	for i := len(convItems) - 1; i >= 0; i-- {
		t := sc.est.EstimateFromItems(convItems[i : i+1])
		if tailTokens+t > tailBudget {
			break
		}
		tailTokens += t
		tailStart = i
	}

	// 3. 处理被截断的工具调用/结果对（VC-002）：如果尾部以工具结果开头，
	// 向后扩展包含对应的助手工具调用消息。
	if tailStart > 0 && tailStart < len(convItems) && convItems[tailStart].Role == "tool" {
		toolCallID := convItems[tailStart].ToolCallID
		for i := tailStart - 1; i >= 0; i-- {
			if convItems[i].Role == "assistant" && len(convItems[i].ToolCalls) > 0 {
				for _, tc := range convItems[i].ToolCalls {
					if tc.ID == toolCallID {
						tailStart = i
						goto tailFixed
					}
				}
			}
		}
	}
tailFixed:

	// 如果全部在尾部，无需摘要。
	if tailStart == 0 {
		result := make([]memctx.TurnItem, 0, len(systemItems)+len(convItems))
		result = append(result, systemItems...)
		result = append(result, convItems...)
		return &memctx.CompactResult{
			Strategy: memctx.CompactSummary,
			BeforeTokens: beforeTokens,
			AfterTokens: beforeTokens,
			ItemsRemoved: 0,
			RetainedItems: result,
		}, nil
	}

	head := convItems[:tailStart]
	tail := convItems[tailStart:]

	// 4. 提取文件操作。
	// 在提取前，先对 head 中的过长 tool result 进行拆分（AC-1: splitTurn），
	// 防止单个巨型工具结果主导上下文窗口。
	head = splitTurn(head, sc.maxItemLength)
	fileOps := extractFileOps(head)

	// 5. 检查是否存在已有摘要（来自上一次压缩的 checkpoint）。
	// 若存在，优先使用 UpdateSummary 增量更新，避免从头重新生成，节省 LLM token。
	// 若 UpdateSummary 失败，回退到完整重新生成。
	var existingSummary string
	for _, item := range systemItems {
		if item.Metadata != nil {
			if compacted, ok := item.Metadata["compacted"]; ok && compacted == true {
				existingSummary = item.Content
				break
			}
		}
	}

	// 从 systemItems 中移除旧摘要项，避免结果中出现重复摘要。
	if existingSummary != "" {
		filtered := make([]memctx.TurnItem, 0, len(systemItems))
		for _, item := range systemItems {
			if item.Metadata != nil {
				if compacted, ok := item.Metadata["compacted"]; ok && compacted == true {
					continue
				}
			}
			filtered = append(filtered, item)
		}
		systemItems = filtered
	}

	var summaryText string
	var err error
	if existingSummary != "" {
		summaryText, err = sc.UpdateSummary(ctx, existingSummary, head)
		if err != nil {
			slog.Warn("update summary failed, falling back to full regeneration", "error", err)
			// 回退到完整重新生成。
			conversationText := formatConversation(head)
			prompt := sc.buildPrompt(conversationText, fileOps)
			summaryText, err = sc.callLLM(ctx, prompt)
			if err != nil {
				return nil, fmt.Errorf("summary compact: generate summary: %w", err)
			}
		}
	} else {
		conversationText := formatConversation(head)
		prompt := sc.buildPrompt(conversationText, fileOps)
		summaryText, err = sc.callLLM(ctx, prompt)
		if err != nil {
			return nil, fmt.Errorf("summary compact: generate summary: %w", err)
		}
	}

	// 7. 构造摘要 TurnItem。
	beforeHeadTokens := sc.est.EstimateFromItems(head)
	fileOpsRaw := make([]map[string]string, len(fileOps))
	for i, op := range fileOps {
		fileOpsRaw[i] = map[string]string{"tool": op.Tool, "path": op.Path}
	}

	summaryItem := memctx.TurnItem{
		Role: "system",
		Content: summaryText,
		Metadata: map[string]any{
			"compacted": true,
			"before_tokens": beforeHeadTokens,
			"file_ops": fileOpsRaw,
		},
	}

	// 8. 组装结果。
	result := make([]memctx.TurnItem, 0, len(systemItems)+1+len(tail))
	result = append(result, systemItems...)
	result = append(result, summaryItem)
	result = append(result, tail...)

	afterTokens := sc.est.EstimateFromItems(result)

	// 更新摘要项的 after_tokens 元数据。
	for i := range result {
		if result[i].Role == "system" && result[i].Content == summaryText {
			if result[i].Metadata == nil {
				result[i].Metadata = make(map[string]any)
			}
			result[i].Metadata["after_tokens"] = afterTokens
			break
		}
	}

	slog.Info("summary compact completed",
		"op", "compact",
		"trigger", "summary",
		"before_tokens", beforeTokens,
		"after_tokens", afterTokens,
		"items_summarized", len(head),
	)

	// RetainedTail: the last few items kept alongside the summary (for session tree
	// context reconstruction). These are the conversation items that were not summarized.
	retainedTail := make([]memctx.TurnItem, len(tail))
	copy(retainedTail, tail)

	return &memctx.CompactResult{
		Strategy: memctx.CompactSummary,
		BeforeTokens: beforeTokens,
		AfterTokens: afterTokens,
		ItemsRemoved: len(head),
		Summary: summaryText,
		RetainedItems: result,
		RetainedTail: retainedTail,
	}, nil
}

// fileOp 表示从对话中提取的文件操作记录。
type fileOp struct {
	Tool string
	Path string
}

// extractFileOps 扫描对话项，提取文件操作记录。
// 先从助手消息的 ToolCalls 构建 CallID→Args 查找表，
// 再遍历工具结果消息，通过元数据或查找表提取文件路径。
func extractFileOps(items []memctx.TurnItem) []fileOp {
	// 构建 CallID → Args 查找表（从助手消息的 ToolCalls）。
	argsByCallID := make(map[string]string)
	for _, item := range items {
		if item.Role != "assistant" {
			continue
		}
		for _, tc := range item.ToolCalls {
			if fileOpTools[tc.Name] && tc.ID != "" {
				argsByCallID[tc.ID] = stringifyArgs(tc.Arguments)
			}
		}
	}

	var ops []fileOp
	for _, item := range items {
		if item.Role != "tool" || !fileOpTools[item.ToolName] {
			continue
		}
		path := extractPathFromItem(item, argsByCallID)
		if path != "" {
			slog.Debug("file operation tracked", "op", "file_op_track", "tool_name", item.ToolName, "path", path)
			ops = append(ops, fileOp{Tool: item.ToolName, Path: path})
		}
	}
	return ops
}

// extractPathFromItem 尝试从工具结果项提取文件路径。
// 优先从 Metadata["args"] 中解析，其次从助手 ToolCall 参数查找。
func extractPathFromItem(item memctx.TurnItem, argsByCallID map[string]string) string {
	// 尝试 Metadata["args"]。
	if item.Metadata != nil {
		if argsRaw, ok := item.Metadata["args"]; ok {
			if s, ok := argsRaw.(string); ok && s != "" {
				if p := parsePathFromMap(s); p != "" {
					return p
				}
			}
		}
	}
	// 回退：通过 CallID 查找助手消息的 ToolCall 参数。
	if item.ToolCallID != "" {
		if args, ok := argsByCallID[item.ToolCallID]; ok && args != "" {
			if p := parsePathFromMap(args); p != "" {
				return p
			}
		}
	}
	return ""
}

// parsePathFromMap 尝试从 JSON 字符串解析 "path" 字段。
// 使用简单的字符串扫描，不依赖 encoding/json 以减少依赖。
func parsePathFromMap(raw string) string {
	idx := strings.Index(raw, `"path"`)
	if idx < 0 {
		return ""
	}
	rest := raw[idx+6:] // 跳过 "path"
	rest = strings.TrimLeft(rest, " :")
	if len(rest) == 0 {
		return ""
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		for _, q := range []byte{'"', '\''} {
			if j := strings.IndexByte(rest, q); j >= 0 {
				quote = q
				rest = rest[j:]
				break
			}
		}
		if quote != '"' && quote != '\'' {
			return ""
		}
	}
	rest = rest[1:] // 跳过开引号
	end := strings.IndexByte(rest, quote)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// stringifyArgs 将 map[string]any 转为字符串表示。
func stringifyArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	var parts []string
	for k, v := range args {
		parts = append(parts, fmt.Sprintf(`"%s":%v`, k, v))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// buildPrompt 构造发送给 LLM 的摘要提示。
// 使用自定义 prompt（若设置）或 defaultSummaryPrompt，将 {conversation_text} 替换为格式化对话。
func (sc *SummaryCompactor) buildPrompt(conversationText string, fileOps []fileOp) string {
	template := sc.summaryPrompt
	if template == "" {
		template = defaultSummaryPrompt
	}

	// 如果有文件操作，追加到 conversation_text 后。
	if len(fileOps) > 0 {
		var sb strings.Builder
		sb.WriteString(conversationText)
		sb.WriteString("\n\n--- File Operations ---\n")
		for _, op := range fileOps {
			fmt.Fprintf(&sb, "- %s: %s\n", op.Tool, op.Path)
		}
		conversationText = sb.String()
	}

	return strings.Replace(template, "{conversation_text}", conversationText, 1)
}

// formatConversation 将对话项格式化为 LLM 可读的文本。
func formatConversation(items []memctx.TurnItem) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString(formatTurnItem(item))
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatTurnItem 将 TurnItem 格式化为 LLM 可读的字符串。
func formatTurnItem(item memctx.TurnItem) string {
	role := item.Role
	content := item.Content
	if len(content) > 500 {
		content = content[:500] + "..."
	}
	if item.ThinkingContent != "" {
		return fmt.Sprintf("[%s] (thinking) %s\n%s", role, item.ThinkingContent, content)
	}
	return fmt.Sprintf("[%s] %s", role, content)
}

// callLLM 通过 ModelProvider 调用 LLM 生成摘要。
// 使用低 temperature（0.3），不传 Tools。
func (sc *SummaryCompactor) callLLM(ctx context.Context, prompt string) (string, error) {
	msgs := []message.Message{
		message.NewTextMessage(message.RoleUser, prompt),
	}

	// 使用低 temperature，不传 Tools / ThinkingMode。
	temp := sc.temperature
	opts := &provider.ChatOptions{
		Temperature: &temp,
		MaxTokens: &sc.maxTokens,
	}

	resp, err := sc.model.Generate(ctx, msgs, opts)
	if err != nil {
		return "", fmt.Errorf("llm generate: %w", err)
	}

	// 从响应消息中提取文本内容。
	var summaryParts []string
	for _, c := range resp.Content {
		if c.Type == message.ContentText {
			summaryParts = append(summaryParts, c.Text)
		}
	}

	return strings.Join(summaryParts, "\n"), nil
}

// copyItems 深拷贝 TurnItem 切片。
func copyItems(items []memctx.TurnItem) []memctx.TurnItem {
	result := make([]memctx.TurnItem, len(items))
	copy(result, items)
	return result
}
