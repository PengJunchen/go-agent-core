// Package context 定义上下文管理抽象。
//
// ContextManager 管理 Agent 的对话历史与上下文压缩。它记录每个 TurnItem，
// 在推理前提供标准化历史，并在 token 超限时触发压缩。
//
// 此包包含核心接口（ContextManager、Compactor、TokenEstimator）和共享类型
// （TurnItem、CompactResult）。默认实现位于 memory/context/heuristic.go
// （HeuristicContextManager）和 memory/compactor/（TruncatingCompactor 等）。
package context

import "context"

// ContextManager 是上下文管理接口。
type ContextManager interface {
	// RecordItem 记录一个 TurnItem 到历史。
	RecordItem(ctx context.Context, item TurnItem) error
	// GetMessages 获取用于推理的标准化历史。
	GetMessages(ctx context.Context, opts *ContextOptions) ([]TurnItem, error)
	// TokenUsage 返回当前 token 用量。
	TokenUsage(ctx context.Context) int
	// Compact 按策略压缩上下文。
	Compact(ctx context.Context, strategy CompactStrategy) (*CompactResult, error)
	// SetInitialContext 设置初始上下文（如系统提示）。
	SetInitialContext(ctx context.Context, items []TurnItem) error
}

// ContextOptions 是获取消息的可选参数。
type ContextOptions struct {
	MaxItems int
}

// CompactStrategy 枚举压缩策略。
type CompactStrategy string

const (
	// CompactAuto 自动（启发式判断）。
	CompactAuto CompactStrategy = "auto"
	// CompactManual 手动触发。
	CompactManual CompactStrategy = "manual"
	// CompactTruncate 截断压缩。
	CompactTruncate CompactStrategy = "truncate"
	// CompactSummary LLM 摘要压缩。
	CompactSummary CompactStrategy = "summary"
	// CompactMicro 微压缩：用占位符替换旧工具结果内容（零 LLM）。
	CompactMicro CompactStrategy = "micro"
)

// CompactResult 是一次压缩的结果。
type CompactResult struct {
	Strategy CompactStrategy
	BeforeTokens int
	AfterTokens int
	ItemsRemoved int
	Summary string // optional summary of removed items
	RetainedItems []TurnItem
	RetainedTail []TurnItem // items preserved during compaction (for session tree)
}

// TurnItem 是上下文中的一个条目（消息/工具调用/工具结果等）。
//
// TurnItem 是上下文管理的通用货币。
type TurnItem struct {
	Role string
	Content string
	ThinkingContent string
	ToolCalls []ToolCallRef
	ToolCallID string
	ToolName string
	Metadata map[string]any
}

// ToolCallRef 引用一次工具调用。
type ToolCallRef struct {
	ID string
	Name string
	Arguments map[string]any
}

// Compactor 是上下文压缩器接口。
//
// 默认实现包括 TruncatingCompactor（memory/compactor 包）、
// SummaryCompactor（memory/compactor 包，待实现）。
type Compactor interface {
	// Compact 压缩 items，返回压缩结果。
	Compact(c context.Context, items []TurnItem, maxTokens int) (*CompactResult, error)
}

// TokenEstimator 估算 token 数量。
//
// 默认实现为 HeuristicEstimator（memory/compactor 包）或
// DefaultHeuristicEstimator（memory/context 包）。
// 第三方可提供基于实际 tokenizer 的实现。
type TokenEstimator interface {
	Estimate(text string) int
	EstimateFromItems(items []TurnItem) int
}
