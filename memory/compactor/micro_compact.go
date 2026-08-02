package compactor

import (
	"context"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// MicroCompactor 微压缩器：用占位符替换旧工具结果内容。
//
// 保留消息链结构（tool call → tool result 配对）的同时减少 token 数量。
// 不调用 LLM，零开销。适用于 Auto 管线的第一级压缩。
//
// 压缩逻辑：
// - 统计 role == "tool" 的条目
// - 跳过最近 KeepRecent 个工具结果（保持最新上下文完整）
// - 将其余工具结果的 Content 替换为 Placeholder，并在 Metadata 中标记 compacted=true
type MicroCompactor struct {
	Estimator memctx.TokenEstimator
	KeepRecent int // 保留最近 N 个工具结果不变（默认 3）
	Placeholder string // 占位文本（默认 "[compacted tool result]"）
}

// defaultKeepRecent 是 KeepRecent 的默认值。
const defaultKeepRecent = 3

// defaultPlaceholder 是 Placeholder 的默认值。
const defaultPlaceholder = "[compacted tool result]"

// Compact 实现微压缩。
func (m MicroCompactor) Compact(_ context.Context, items []memctx.TurnItem, _ int) (*memctx.CompactResult, error) {
	est := m.Estimator
	if est == nil {
		est = &HeuristicTokenEstimator{}
	}
	before := est.EstimateFromItems(items)

	keepRecent := m.KeepRecent
	if keepRecent <= 0 {
		keepRecent = defaultKeepRecent
	}
	placeholder := m.Placeholder
	if placeholder == "" {
		placeholder = defaultPlaceholder
	}

	// 收集工具结果条目的索引
	var toolIndices []int
	for i, item := range items {
		if item.Role == "tool" {
			toolIndices = append(toolIndices, i)
		}
	}

	// 无工具结果，直接返回
	if len(toolIndices) == 0 {
		retainedTail := make([]memctx.TurnItem, len(items))
		copy(retainedTail, items)
		return &memctx.CompactResult{
			Strategy: memctx.CompactMicro,
			BeforeTokens: before,
			AfterTokens: before,
			ItemsRemoved: 0,
			RetainedItems: items,
			RetainedTail: retainedTail,
		}, nil
	}

	// 确定需要压缩的工具结果索引（跳过最近 keepRecent 个）
	compactCount := len(toolIndices) - keepRecent
	if compactCount < 0 {
		compactCount = 0
	}
	compactIndices := toolIndices[:compactCount]

	// 复制 items 并替换旧工具结果内容
	result := make([]memctx.TurnItem, len(items))
	copy(result, items)

	for _, idx := range compactIndices {
		result[idx].Content = placeholder
		if result[idx].Metadata == nil {
			result[idx].Metadata = make(map[string]any)
		}
		result[idx].Metadata["compacted"] = true
	}

	after := est.EstimateFromItems(result)
	itemsRemoved := 0
	if after < before {
		// 报告被压缩（非移除）的条目数
		itemsRemoved = len(compactIndices)
	}

	// RetainedTail: full list of items after compaction (message structure preserved,
	// only old tool result contents replaced with placeholders).
	retainedTail := make([]memctx.TurnItem, len(result))
	copy(retainedTail, result)
	return &memctx.CompactResult{
		Strategy: memctx.CompactMicro,
		BeforeTokens: before,
		AfterTokens: after,
		ItemsRemoved: itemsRemoved,
		RetainedItems: result,
		RetainedTail: retainedTail,
	}, nil
}
