package compactor

import (
	"context"
	"testing"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// Compactor 接口测试：TruncatingCompactor 实现 memory/context.Compactor 接口。
func TestTruncatingCompactor_Interface(t *testing.T) {
	var _ memctx.Compactor = TruncatingCompactor{}
}

// VQ-001: 截断压缩后 token 数应低于 maxTokens。
func TestVQ001_TruncatesBelowMax(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	c := TruncatingCompactor{Estimator: e}
	items := []memctx.TurnItem{
		{Role: "user", Content: "这是一段很长的文本用于测试截断压缩，需要足够长才能触发截断逻辑"},
		{Role: "assistant", Content: "另一段中等长度的回复文本"},
		{Role: "user", Content: "短"},
	}
	maxTokens := 5 // 强制截断到很小的值
	result, err := c.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Strategy != memctx.CompactTruncate {
		t.Errorf("expected strategy %s, got %s", memctx.CompactTruncate, result.Strategy)
	}
	if result.AfterTokens > maxTokens {
		t.Errorf("after tokens %d > max %d", result.AfterTokens, maxTokens)
	}
	if result.ItemsRemoved <= 0 {
		t.Error("expected items to be removed")
	}
	if len(result.RetainedItems) == 0 {
		t.Error("retained items should not be empty")
	}
}

// VQ-002: 不需要截断时保留全部。
func TestVQ002_NoTruncationNeeded(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	c := TruncatingCompactor{Estimator: e}
	items := []memctx.TurnItem{
		{Role: "user", Content: "短"},
	}
	result, err := c.Compact(context.Background(), items, 1000)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.ItemsRemoved != 0 {
		t.Errorf("expected 0 removed, got %d", result.ItemsRemoved)
	}
	if len(result.RetainedItems) != 1 {
		t.Errorf("expected 1 retained, got %d", len(result.RetainedItems))
	}
}

// VQ-003: HeuristicTokenEstimator 估算合理（char/4 for English）。
func TestVQ003_HeuristicTokenEstimatorEnglish(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	if got := e.Estimate("hello world!"); got != 3 { // 12/4=3
		t.Errorf("Estimate(\"hello world!\") = %d, want 3", got)
	}
	items := []memctx.TurnItem{
		{Role: "user", Content: "hello"}, // 5/4=1
		{Role: "assistant", Content: "world"}, // 5/4=1
		{Role: "user", Content: "thinking about"}, // 13/4=3
	}
	// 1+1+3=5
	if got := e.EstimateFromItems(items); got != 5 {
		t.Errorf("EstimateFromItems = %d, want 5", got)
	}
}

// VQ-004: HeuristicTokenEstimator 对空输入估算为 0。
func TestVQ004_EstimatorEmptyInput(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	if got := e.Estimate(""); got != 0 {
		t.Errorf("Estimate(\"\") = %d, want 0", got)
	}
	items := []memctx.TurnItem{
		{Role: "user", Content: ""},
	}
	if got := e.EstimateFromItems(items); got != 0 {
		t.Errorf("EstimateFromItems empty = %d, want 0", got)
	}
}

// VQ-005: HeuristicTokenEstimator 计算包含 ThinkingContent。
func TestVQ005_EstimatorWithThinking(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	items := []memctx.TurnItem{
		{Role: "assistant", Content: "hello", ThinkingContent: "long thinking process here"}, // 5/4 + 27/4 = 1+6 = 7
	}
	if got := e.EstimateFromItems(items); got != 7 {
		t.Errorf("EstimateFromItems with thinking = %d, want 7", got)
	}
}

// VQ-006: TruncatingCompactor 使用默认 Estimate（当 Estimator 为 nil 时）。
func TestVQ006_DefaultEstimator(t *testing.T) {
	c := TruncatingCompactor{} // nil Estimator -> fallback to HeuristicTokenEstimator
	items := []memctx.TurnItem{
		{Role: "user", Content: "hello world"}, // 11/4=2
		{Role: "assistant", Content: "this is a longer response text"}, // 33/4=8
		{Role: "user", Content: "short"}, // 5/4=1
	}
	result, err := c.Compact(context.Background(), items, 5)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.BeforeTokens != 10 {
		t.Errorf("BeforeTokens = %d, want 10", result.BeforeTokens)
	}
	if result.ItemsRemoved <= 0 {
		t.Error("expected items to be removed")
	}
}

// VQ-007: TruncatingCompactor 单个条目应保留。
func TestVQ007_SingleItem(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	c := TruncatingCompactor{Estimator: e}
	items := []memctx.TurnItem{
		{Role: "user", Content: "single"},
	}
	result, err := c.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.ItemsRemoved != 0 {
		t.Errorf("expected 0 removed, got %d", result.ItemsRemoved)
	}
	if len(result.RetainedItems) != 1 {
		t.Errorf("expected 1 retained, got %d", len(result.RetainedItems))
	}
	if result.BeforeTokens != 1 {
		t.Errorf("BeforeTokens = %d, want 1", result.BeforeTokens)
	}
}

// VS-001: Context 取消应尽早返回。
func TestVS001_ContextCancel(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	c := TruncatingCompactor{Estimator: e}
	items := []memctx.TurnItem{
		{Role: "user", Content: "hello"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	result, err := c.Compact(ctx, items, 5)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result == nil {
		t.Fatal("result should not be nil")
	}
}

// VS-002: 空 items 返回空结果。
func TestVS002_EmptyItems(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	c := TruncatingCompactor{Estimator: e}
	result, err := c.Compact(context.Background(), []memctx.TurnItem{}, 100)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.BeforeTokens != 0 {
		t.Errorf("BeforeTokens = %d, want 0", result.BeforeTokens)
	}
	if len(result.RetainedItems) != 0 {
		t.Errorf("RetainedItems should be empty, got %d", len(result.RetainedItems))
	}
}

// VS-003: HeuristicTokenEstimator 接口编译检查。
func TestVS003_EstimatorInterface(t *testing.T) {
	var _ memctx.TokenEstimator = (*HeuristicTokenEstimator)(nil)
}

// AC-14.2: TruncatingCompactor populates RetainedTail after compaction.
// After truncation, RetainedTail should contain the kept (tail) items,
// matching RetainedItems in content and length.
func TestTruncatingCompactor_RetainedTailPopulated(t *testing.T) {
	e := &HeuristicTokenEstimator{}
	c := TruncatingCompactor{Estimator: e}
	items := []memctx.TurnItem{
		{Role: "user", Content: "这是一段很长的文本用于测试截断压缩，需要足够长才能触发截断逻辑"},
		{Role: "assistant", Content: "另一段中等长度的回复文本"},
		{Role: "user", Content: "短"},
	}
	maxTokens := 5 // 强制截断到很小的值
	result, err := c.Compact(context.Background(), items, maxTokens)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// RetainedTail should be non-empty after compaction.
	if len(result.RetainedTail) == 0 {
		t.Fatal("expected non-empty RetainedTail after truncation")
	}

	// RetainedTail length should match RetainedItems length.
	if len(result.RetainedTail) != len(result.RetainedItems) {
		t.Errorf("RetainedTail length %d != RetainedItems length %d",
			len(result.RetainedTail), len(result.RetainedItems))
	}

	// RetainedTail content should match RetainedItems.
	for i := range result.RetainedTail {
		if result.RetainedTail[i].Content != result.RetainedItems[i].Content {
			t.Errorf("RetainedTail[%d].Content = %q, want %q",
				i, result.RetainedTail[i].Content, result.RetainedItems[i].Content)
		}
	}

	// Verify RetainedTail is an independent copy (not aliasing RetainedItems).
	result.RetainedTail[0].Content = "modified"
	if result.RetainedItems[0].Content == "modified" {
		t.Error("RetainedTail should be an independent copy, not alias RetainedItems")
	}
}
