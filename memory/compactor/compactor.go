// Package compactor 定义上下文压缩抽象。
//
// Compactor 是可替换的压缩策略接口。默认提供两种实现：
// - SummaryCompactor：LLM 摘要压缩（核心能力，
// - TruncatingCompactor：截断压缩（fallback）
//
// SummaryCompactor 通过 ModelProvider 接口调用 LLM 生成摘要，实现接口闭环。
package compactor

import (
	"context"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// Compactor 是上下文压缩器接口。
type Compactor interface {
	// Compact 压缩 items，返回压缩结果。
	Compact(c context.Context, items []memctx.TurnItem, maxTokens int) (*memctx.CompactResult, error)
}

// TokenEstimator 估算 token 数量。
type TokenEstimator interface {
	Estimate(text string) int
	EstimateFromItems(items []memctx.TurnItem) int
}

// HeuristicEstimator 是默认的 token 估算器（char/4 启发式）。
type HeuristicEstimator struct{}

// Estimate 估算文本的 token 数。
func (HeuristicEstimator) Estimate(text string) int {
	return len(text) / 4
}

// EstimateFromItems 估算一批条目的 token 数。
func (e HeuristicEstimator) EstimateFromItems(items []memctx.TurnItem) int {
	total := 0
	for _, item := range items {
		total += e.Estimate(item.Content)
		total += e.Estimate(item.ThinkingContent)
	}
	return total
}

// TruncatingCompactor 截断式压缩（fallback 实现）。
//
// 移除最早的非系统项直到 token 数降至 maxTokens 以下。
type TruncatingCompactor struct {
	Estimator TokenEstimator
}

// Compact 实现截断压缩。
func (t TruncatingCompactor) Compact(c context.Context, items []memctx.TurnItem, maxTokens int) (*memctx.CompactResult, error) {
	est := t.Estimator
	if est == nil {
		est = HeuristicEstimator{}
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
