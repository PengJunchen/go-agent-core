package compactor

import (
	"context"
	"testing"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// Compactor 接口测试：TruncatingCompactor 实现 Compactor 接口。
func TestTruncatingCompactor_Interface(t *testing.T) {
	var _ Compactor = TruncatingCompactor{}
}

// VT-001: 截断压缩后 token 数应低于 maxTokens。
func TestTruncatingCompactor_TruncatesBelowMax(t *testing.T) {
	c := TruncatingCompactor{Estimator: HeuristicEstimator{}}
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

// VT-002: 不需要截断时保留全部。
func TestTruncatingCompactor_NoTruncationNeeded(t *testing.T) {
	c := TruncatingCompactor{Estimator: HeuristicEstimator{}}
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

// VT-003: HeuristicEstimator 估算合理（char/4）。
func TestHeuristicEstimator_Estimate(t *testing.T) {
	e := HeuristicEstimator{}
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
