// Package compactor 定义上下文压缩实现。
//
// 提供三种默认实现：
// - TruncatingCompactor：截断压缩（fallback）
// - SummaryCompactor：LLM 摘要压缩（memory/compactor/summary.go）
// - HeuristicTokenEstimator：CJK-aware 启发式 token 估算（memory/compactor/estimator.go）
//
// 接口 Compactor 和 TokenEstimator 定义在 memory/context 包。
package compactor

import (
	"context"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// TruncatingCompactor 截断式压缩（fallback 实现）。
//
// 移除最早的非系统项直到 token 数降至 maxTokens 以下。
// 实现 memory/context.Compactor 接口。
type TruncatingCompactor struct {
	Estimator memctx.TokenEstimator
}

// Compact 实现截断压缩。
func (t TruncatingCompactor) Compact(c context.Context, items []memctx.TurnItem, maxTokens int) (*memctx.CompactResult, error) {
	est := t.Estimator
	if est == nil {
		est = &HeuristicTokenEstimator{}
	}
	before := est.EstimateFromItems(items)
	kept := items
	for len(kept) > 1 && est.EstimateFromItems(kept) > maxTokens {
		kept = kept[1:]
	}
	return &memctx.CompactResult{
		Strategy: memctx.CompactTruncate,
		BeforeTokens: before,
		AfterTokens: est.EstimateFromItems(kept),
		ItemsRemoved: len(items) - len(kept),
		RetainedItems: kept,
	}, nil
}
