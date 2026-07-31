// Package context 定义上下文管理抽象。
//
// HeuristicContextManager 是 ContextManager 的启发式默认实现。
// 它使用 TokenEstimator 估算 token 用量，在 RecordItem 时记录条目，
// 在 GetMessages 时返回维护的历史，在 TokenUsage 时返回估算值，
// 并在 Compact 时按策略委派给对应的 Compactor（方案 C：双 Compactor）。
//
// 设计 ContextManager，默认触发 M0 级启发式逻辑。
package context

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// HeuristicContextManager 是 ContextManager 的启发式默认实现。
//
// 按方案 C（Designer §2）持有 summaryCompactor + truncatingCompactor 双字段。
// Compact 策略按如下方式分发：
// - CompactSummary → summaryCompactor
// - CompactTruncate → truncatingCompactor
// - CompactAuto → 优先 summaryCompactor（若可用），超阈值回退 truncatingCompactor
// - CompactManual → 先 summaryCompactor 再 truncatingCompactor（双重压缩）
//
// 字段：
// - items：有序的所有 TurnItem 列表（初始上下文在前，后续条目追加）
// - estimator：Token 估算器（默认 DefaultHeuristicEstimator，可替换）
// - summaryCompactor：摘要压缩器（nil 表示不可用）
// - truncatingCompactor：截断压缩器（nil 表示不可用）
// - maxTokens：触发压缩的 token 上限（0 表示不限制）
// - mu：读写锁保证并发安全
type HeuristicContextManager struct {
	mu sync.RWMutex
	items []TurnItem
	estimator TokenEstimator
	summaryCompactor Compactor // 摘要压缩器（CompactSummary/CompactAuto 用）
	truncatingCompactor Compactor // 截断压缩器（CompactTruncate 用，自动回退）
	maxTokens int
}

// HeuristicContextManagerOption 是 HeuristicContextManager 的可选构造参数。
type HeuristicContextManagerOption func(*HeuristicContextManager)

// WithEstimator 设置自定义 TokenEstimator。
func WithEstimator(e TokenEstimator) HeuristicContextManagerOption {
	return func(m *HeuristicContextManager) {
		m.estimator = e
	}
}

// WithCompactor 设置 Compactor（作为 summaryCompactor 和 truncatingCompactor 的快捷设置）。
// 同时设置两个字段。为精细控制，请使用 WithSummaryCompactor 和 WithTruncatingCompactor。
func WithCompactor(c Compactor) HeuristicContextManagerOption {
	return func(m *HeuristicContextManager) {
		m.summaryCompactor = c
		m.truncatingCompactor = c
	}
}

// WithSummaryCompactor 设置摘要压缩器（用于 CompactSummary / CompactAuto 策略）。
func WithSummaryCompactor(c Compactor) HeuristicContextManagerOption {
	return func(m *HeuristicContextManager) {
		m.summaryCompactor = c
	}
}

// WithTruncatingCompactor 设置截断压缩器（用于 CompactTruncate 策略）。
func WithTruncatingCompactor(c Compactor) HeuristicContextManagerOption {
	return func(m *HeuristicContextManager) {
		m.truncatingCompactor = c
	}
}

// WithMaxTokens 设置触发压缩的 token 上限。
// 0 表示不限制（永远不会自动触发，但手动 Compact 仍可工作）。
func WithMaxTokens(max int) HeuristicContextManagerOption {
	return func(m *HeuristicContextManager) {
		m.maxTokens = max
	}
}

// ErrNoCompactor 是未设置 Compactor 时调用 Compact 返回的错误。
var ErrNoCompactor = fmt.Errorf("compact: no compactor configured")

// Deprecated: DefaultHeuristicEstimator is the legacy heuristic estimator (char/4).
// It is kept for backward compatibility but has been superseded by
// compactor.HeuristicTokenEstimator which offers CJK-aware estimation
// and ToolCall accounting. Use memory/compactor.NewHeuristicTokenEstimator() instead.
//
// DefaultHeuristicEstimator implements TokenEstimator with a simple char/4 heuristic.
type DefaultHeuristicEstimator struct{}

// Estimate estimates token count from text (char/4 heuristic).
func (DefaultHeuristicEstimator) Estimate(text string) int {
	return len(text) / 4
}

// EstimateFromItems estimates total token count from a batch of TurnItems.
func (e DefaultHeuristicEstimator) EstimateFromItems(items []TurnItem) int {
	total := 0
	for _, item := range items {
		total += e.Estimate(item.Content)
		total += e.Estimate(item.ThinkingContent)
	}
	return total
}

// NewHeuristicContextManager 创建一个 HeuristicContextManager。
//
// 默认行为：
// - estimator：DefaultHeuristicEstimator（char/4 启发式估算）
// - summaryCompactor：nil（摘要压缩需通过 WithSummaryCompactor 或 WithCompactor 设置）
// - truncatingCompactor：nil（截断压缩需通过 WithTruncatingCompactor 或 WithCompactor 设置）
// - maxTokens：4096（默认触发压缩的阈值）
//
// 注：不默认注入 TruncatingCompactor，以避免 memory/compactor → memory/context 循环依赖。
// 调用方负责通过 WithCompactor / WithSummaryCompactor / WithTruncatingCompactor 注入。
func NewHeuristicContextManager(opts ...HeuristicContextManagerOption) *HeuristicContextManager {
	m := &HeuristicContextManager{
		items: make([]TurnItem, 0),
		estimator: DefaultHeuristicEstimator{},
		maxTokens: 4096,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// compile-time interface check
var _ ContextManager = (*HeuristicContextManager)(nil)

// RecordItem 记录一个 TurnItem 到历史。
//
// 追加到 items 末尾。如果设置了 maxTokens > 0 且估算值超过阈值，
// 日志记录警告但**不做自动压缩**（留给 Compact 显式触发）。
func (m *HeuristicContextManager) RecordItem(ctx context.Context, item TurnItem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.items = append(m.items, item)

	currentTokens := m.estimator.EstimateFromItems(m.items)
	if m.maxTokens > 0 && currentTokens > m.maxTokens {
		slog.Warn("token usage exceeds maxTokens",
			"op", "context_record",
			"current", currentTokens,
			"max", m.maxTokens,
			"total_items", len(m.items),
		)
	}

	slog.Debug("item recorded",
		"op", "context_record",
		"role", item.Role,
		"total_items", len(m.items),
	)

	return nil
}

// GetMessages 获取用于推理的标准化历史。
//
// 返回当前完整的 items 列表。若 opts.MaxItems > 0，则返回最近的
// MaxItems 个条目（保留最前面的初始上下文）。
// 不会修改内存状态。
func (m *HeuristicContextManager) GetMessages(ctx context.Context, opts *ContextOptions) ([]TurnItem, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.items) == 0 {
		return []TurnItem{}, nil
	}

	if opts == nil || opts.MaxItems <= 0 {
		result := make([]TurnItem, len(m.items))
		copy(result, m.items)
		return result, nil
	}

	return m.truncateMessages(m.items, opts.MaxItems), nil
}

// truncateMessages 保留 items 前部的初始上下文（首个 user/assistant 消息之前的条目）
// 并截取最近的 maxItems 个条目。
func (m *HeuristicContextManager) truncateMessages(items []TurnItem, maxItems int) []TurnItem {
	if len(items) <= maxItems {
		result := make([]TurnItem, len(items))
		copy(result, items)
		return result
	}

	// 找到初始上下文的边界：第一个非 user/assistant 角色通常是 system 或 function
	// 我们保留 items 开头所有 system/function 角色作为初始上下文
	prefixEnd := 0
	for i, item := range items {
		if item.Role == "system" || item.Role == "function" || item.Role == "developer" {
			prefixEnd = i + 1
		} else {
			break
		}
	}

	// 如果初始上下文已经占了大部分，直接截断尾部
	if prefixEnd >= maxItems {
		result := make([]TurnItem, maxItems)
		copy(result, items[:maxItems])
		return result
	}

	// 保留前缀，再取最近的 (maxItems - prefixEnd) 个条目
	n := maxItems - prefixEnd
	if n > len(items)-prefixEnd {
		n = len(items) - prefixEnd
	}
	start := len(items) - n

	result := make([]TurnItem, 0, maxItems)
	result = append(result, items[:prefixEnd]...)
	result = append(result, items[start:]...)
	return result
}

// TokenUsage 返回当前 token 的启发式估算值。
func (m *HeuristicContextManager) TokenUsage(ctx context.Context) int {
	select {
	case <-ctx.Done():
		return 0
	default:
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.estimator.EstimateFromItems(m.items)
}

// compactOrErr 辅助方法：调用 Compactor.Compact 并处理结果。
func (m *HeuristicContextManager) compactOrErr(ctx context.Context, c Compactor, strategy CompactStrategy, limit int) (*CompactResult, error) {
	if c == nil {
		return nil, ErrNoCompactor
	}
	result, err := c.Compact(ctx, m.items, limit)
	if err != nil {
		return nil, fmt.Errorf("compact: %w", err)
	}
	m.items = result.RetainedItems
	return result, nil
}

// Compact 按策略压缩上下文。
//
// 策略行为（按 Designer §2 方案 C）：
// - CompactTruncate → 委托 truncatingCompactor
// - CompactSummary → 委托 summaryCompactor
// - CompactAuto → 若 currentTokens <= maxTokens 则 noop；
// 否则先尝试 summaryCompactor，若结果仍超阈值则回退 truncatingCompactor
// - CompactManual → 先 summaryCompactor（若可用），后 truncatingCompactor（双重压缩）
//
// 若无对应 Compactor 则返回 ErrNoCompactor。
func (m *HeuristicContextManager) Compact(ctx context.Context, strategy CompactStrategy) (*CompactResult, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	currentTokens := m.estimator.EstimateFromItems(m.items)
	limit := m.maxTokens
	if limit <= 0 {
		limit = currentTokens
	}

	switch strategy {
	case CompactTruncate:
		return m.compactOrErr(ctx, m.truncatingCompactor, strategy, limit)

	case CompactSummary:
		return m.compactOrErr(ctx, m.summaryCompactor, strategy, limit)

	case CompactAuto:
		if m.maxTokens > 0 && currentTokens <= m.maxTokens {
			return &CompactResult{
				Strategy: CompactAuto,
				BeforeTokens: currentTokens,
				AfterTokens: currentTokens,
				ItemsRemoved: 0,
				RetainedItems: m.items,
			}, nil
		}
		// 优先尝试 summaryCompactor
		if m.summaryCompactor != nil {
			result, err := m.summaryCompactor.Compact(ctx, m.items, limit)
			if err == nil && result.AfterTokens <= limit {
				m.items = result.RetainedItems
				result.Strategy = CompactAuto
				logCompact(ctx, "auto", result)
				return result, nil
			}
		}
		// 回退 truncatingCompactor
		return m.compactOrErr(ctx, m.truncatingCompactor, CompactAuto, limit)

	case CompactManual:
		// 先 summaryCompactor（若可用）
		if m.summaryCompactor != nil {
			result, err := m.summaryCompactor.Compact(ctx, m.items, limit)
			if err != nil {
				return nil, fmt.Errorf("compact manual (summary): %w", err)
			}
			m.items = result.RetainedItems
			// 再 truncatingCompactor
			if m.truncatingCompactor != nil && result.AfterTokens > limit {
				result2, err := m.truncatingCompactor.Compact(ctx, m.items, limit)
				if err != nil {
					return nil, fmt.Errorf("compact manual (truncate): %w", err)
				}
				m.items = result2.RetainedItems
				result2.Strategy = CompactManual
				logCompact(ctx, "manual", result2)
				return result2, nil
			}
			result.Strategy = CompactManual
			logCompact(ctx, "manual", result)
			return result, nil
		}
		// 无 summaryCompactor 时退到 truncatingCompactor
		if m.truncatingCompactor != nil {
			return m.compactOrErr(ctx, m.truncatingCompactor, CompactManual, limit)
		}
		return nil, ErrNoCompactor

	default:
		return nil, fmt.Errorf("unknown compact strategy: %s", strategy)
	}
}

// logCompact 记录压缩日志。
func logCompact(ctx context.Context, strategy string, result *CompactResult) {
	slog.Info("context compacted",
		"op", "context_compact",
		"strategy", strategy,
		"before", result.BeforeTokens,
		"after", result.AfterTokens,
		"removed", result.ItemsRemoved,
		"total_items", len(result.RetainedItems),
	)
}

// initialCtxMarker is a sentinel value stored in TurnItem.Metadata to mark
// items that were injected via SetInitialContext, enabling idempotent dedup.
const initialCtxMarker = "__initial_ctx__"

// SetInitialContext 设置初始上下文（如系统提示）。
//
// 这些条目被放置在 items 列表的最前面。如果 items 当前为空则直接设置；
// 否则在现有 items 前面插入。通常用于注入 system prompt。
//
// 幂等：多次调用会在 items 前面追加（不覆盖）。如果需覆盖，可先清空再调用。
// 连续调用时，已有的 initial context 不会被重复插入——新传入的 items
// 中与已有 initial context 内容相同的条目会被跳过。
func (m *HeuristicContextManager) SetInitialContext(ctx context.Context, items []TurnItem) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if len(items) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Count existing initial context items (marked with initialCtxMarker)
	existingInitialCount := 0
	for _, item := range m.items {
		if item.Metadata != nil {
			if _, ok := item.Metadata[initialCtxMarker]; ok {
				existingInitialCount++
			}
		}
	}

	// If there are existing initial items, prepend only new (non-duplicate) items
	if existingInitialCount > 0 {
		// Build a set of existing initial item content for dedup
		existingSet := make(map[string]struct{})
		for i := 0; i < existingInitialCount && i < len(m.items); i++ {
			key := m.items[i].Role + ":" + m.items[i].Content
			existingSet[key] = struct{}{}
		}

		// Filter out duplicates from new items
		var newItems []TurnItem
		for _, item := range items {
			key := item.Role + ":" + item.Content
			if _, exists := existingSet[key]; !exists {
				newItems = append(newItems, item)
			}
		}

		if len(newItems) == 0 {
			return nil
		}

		// Mark new items as initial context
		for i := range newItems {
			if newItems[i].Metadata == nil {
				newItems[i].Metadata = make(map[string]any)
			}
			newItems[i].Metadata[initialCtxMarker] = true
		}

		// Prepend new items before existing initial context (preserve prepend semantics)
		merged := make([]TurnItem, 0, len(newItems)+len(m.items))
		merged = append(merged, newItems...)
		merged = append(merged, m.items...)
		m.items = merged
	} else {
		// No existing initial context: mark and prepend all items
		for i := range items {
			if items[i].Metadata == nil {
				items[i].Metadata = make(map[string]any)
			}
			items[i].Metadata[initialCtxMarker] = true
		}
		m.items = append(items, m.items...)
	}

	slog.Info("initial context set",
		"op", "context_set_initial",
		"items_added", len(items),
		"total_items", len(m.items),
	)

	return nil
}
