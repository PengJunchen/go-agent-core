package compactor

import (
	"context"
	"testing"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// makeToolResult 构造一个 role=="tool" 的 TurnItem。
func makeToolResult(id, content string) memctx.TurnItem {
	return memctx.TurnItem{
		Role: "tool",
		Content: content,
		ToolCallID: id,
		ToolName: "search",
	}
}

// MC-001: MicroCompactor replaces old tool results with placeholder.
func TestMicroCompactor_ReplacesOldToolResults_MC001(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "user", Content: "What is the weather?"},
		{Role: "assistant", Content: "Let me check.", ToolCalls: []memctx.ToolCallRef{{ID: "call_1", Name: "search"}}},
		makeToolResult("call_1", "The weather in Tokyo is sunny with a high of 28 degrees Celsius and light winds from the northeast."),
		{Role: "assistant", Content: "Let me check more.", ToolCalls: []memctx.ToolCallRef{{ID: "call_2", Name: "search"}}},
		makeToolResult("call_2", "The weather in Paris is rainy with a temperature of 15 degrees Celsius."),
		{Role: "assistant", Content: "Let me check again.", ToolCalls: []memctx.ToolCallRef{{ID: "call_3", Name: "search"}}},
		makeToolResult("call_3", "The weather in London is cloudy with a temperature of 12 degrees."),
		{Role: "assistant", Content: "Let me check one more.", ToolCalls: []memctx.ToolCallRef{{ID: "call_4", Name: "search"}}},
		makeToolResult("call_4", "The weather in Berlin is snowy with a temperature of -2 degrees."),
	}

	mc := MicroCompactor{KeepRecent: 1}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Strategy != memctx.CompactMicro {
		t.Errorf("expected strategy %s, got %s", memctx.CompactMicro, result.Strategy)
	}

	// With KeepRecent=1, only the last tool result should be intact.
	// The first 3 tool results should be replaced.
	toolResults := 0
	compacted := 0
	for _, item := range result.RetainedItems {
		if item.Role == "tool" {
			toolResults++
			if item.Metadata != nil && item.Metadata["compacted"] == true {
				compacted++
			}
		}
	}
	if toolResults != 4 {
		t.Errorf("expected 4 tool results, got %d", toolResults)
	}
	if compacted != 3 {
		t.Errorf("expected 3 compacted tool results, got %d", compacted)
	}

	// Token count should decrease
	if result.AfterTokens >= result.BeforeTokens {
		t.Errorf("expected after tokens < before tokens, got after=%d before=%d", result.AfterTokens, result.BeforeTokens)
	}
}

// MC-002: MicroCompactor does not call LLM (no LLM dependency).
func TestMicroCompactor_NoLLMCall_MC002(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "user", Content: "hello"},
		makeToolResult("call_1", "a very long tool result that should be compacted away to save tokens"),
		makeToolResult("call_2", "another long tool result that should also be compacted"),
		makeToolResult("call_3", "recent result kept intact"),
	}

	// MicroCompactor struct has no LLM field — this is the compile-time guarantee.
	// We verify it works without any LLM provider set.
	mc := MicroCompactor{}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Summary != "" {
		t.Errorf("MicroCompactor should not produce a summary, got %q", result.Summary)
	}
}

// MC-003: MicroCompactor keeps recent N tool results intact.
func TestMicroCompactor_KeepsRecentIntact_MC003(t *testing.T) {
	items := []memctx.TurnItem{
		makeToolResult("call_1", "result 1 with substantial content to be compacted"),
		makeToolResult("call_2", "result 2 with substantial content to be compacted"),
		makeToolResult("call_3", "result 3 with substantial content to be compacted"),
		makeToolResult("call_4", "result 4 with substantial content kept intact"),
		makeToolResult("call_5", "result 5 with substantial content kept intact"),
	}

	mc := MicroCompactor{KeepRecent: 2}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	retained := result.RetainedItems
	// Last 2 tool results should keep original content
	if retained[3].Content != "result 4 with substantial content kept intact" {
		t.Errorf("expected 4th item intact, got %q", retained[3].Content)
	}
	if retained[4].Content != "result 5 with substantial content kept intact" {
		t.Errorf("expected 5th item intact, got %q", retained[4].Content)
	}

	// First 3 should be compacted
	if retained[0].Content != "[compacted tool result]" {
		t.Errorf("expected 1st item compacted, got %q", retained[0].Content)
	}
	if retained[1].Content != "[compacted tool result]" {
		t.Errorf("expected 2nd item compacted, got %q", retained[1].Content)
	}
	if retained[2].Content != "[compacted tool result]" {
		t.Errorf("expected 3rd item compacted, got %q", retained[2].Content)
	}
}

// MC-004: MicroCompactor preserves message chain structure.
func TestMicroCompactor_PreservesChainStructure_MC004(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "user", Content: "Question?"},
		{Role: "assistant", Content: "Calling tool", ToolCalls: []memctx.ToolCallRef{{ID: "call_1", Name: "search"}}},
		makeToolResult("call_1", "detailed search result content here"),
		{Role: "assistant", Content: "Final answer based on results"},
	}

	mc := MicroCompactor{KeepRecent: 0}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	retained := result.RetainedItems
	// All 4 items should still be present (no items removed)
	if len(retained) != 4 {
		t.Fatalf("expected 4 retained items, got %d", len(retained))
	}

	// Structure preserved: user → assistant → tool → assistant
	if retained[0].Role != "user" {
		t.Errorf("expected role user at index 0, got %s", retained[0].Role)
	}
	if retained[1].Role != "assistant" {
		t.Errorf("expected role assistant at index 1, got %s", retained[1].Role)
	}
	if retained[2].Role != "tool" {
		t.Errorf("expected role tool at index 2, got %s", retained[2].Role)
	}
	if retained[3].Role != "assistant" {
		t.Errorf("expected role assistant at index 3, got %s", retained[3].Role)
	}

	// Tool call ID should be preserved
	if retained[2].ToolCallID != "call_1" {
		t.Errorf("expected ToolCallID call_1, got %s", retained[2].ToolCallID)
	}
	// Tool call reference on assistant should be preserved
	if len(retained[1].ToolCalls) != 1 || retained[1].ToolCalls[0].ID != "call_1" {
		t.Errorf("expected assistant to retain tool call reference call_1")
	}
}

// MC-005: MicroCompactor uses custom placeholder when set.
func TestMicroCompactor_CustomPlaceholder_MC005(t *testing.T) {
	items := []memctx.TurnItem{
		makeToolResult("call_1", "long result content to be replaced"),
		makeToolResult("call_2", "another long result content"),
	}

	customPlaceholder := "[omitted]"
	mc := MicroCompactor{KeepRecent: 1, Placeholder: customPlaceholder}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	retained := result.RetainedItems
	// First tool result should use custom placeholder
	if retained[0].Content != customPlaceholder {
		t.Errorf("expected custom placeholder %q, got %q", customPlaceholder, retained[0].Content)
	}
	// Second tool result should be intact (within KeepRecent=1)
	if retained[1].Content != "another long result content" {
		t.Errorf("expected 2nd item intact, got %q", retained[1].Content)
	}
}

// MC-006: MicroCompactor handles no tool results gracefully.
func TestMicroCompactor_NoToolResults_MC006(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
	}

	mc := MicroCompactor{KeepRecent: 3}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// No changes — items should be identical
	if len(result.RetainedItems) != 2 {
		t.Fatalf("expected 2 retained items, got %d", len(result.RetainedItems))
	}
	if result.ItemsRemoved != 0 {
		t.Errorf("expected 0 items removed, got %d", result.ItemsRemoved)
	}
	if result.BeforeTokens != result.AfterTokens {
		t.Errorf("expected same token count, got before=%d after=%d", result.BeforeTokens, result.AfterTokens)
	}
	if result.RetainedItems[0].Content != "Hello" {
		t.Errorf("expected first item intact, got %q", result.RetainedItems[0].Content)
	}
}

// AC-14.2: MicroCompactor populates RetainedTail after compaction.
// After micro-compaction, RetainedTail should contain the full list of items
// (message structure preserved, only old tool result contents replaced with placeholders).
func TestMicroCompactor_RetainedTailPopulated(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "user", Content: "What is the weather?"},
		{Role: "assistant", Content: "Let me check.", ToolCalls: []memctx.ToolCallRef{{ID: "call_1", Name: "search"}}},
		makeToolResult("call_1", "The weather in Tokyo is sunny with a high of 28 degrees Celsius and light winds from the northeast."),
		{Role: "assistant", Content: "Let me check more.", ToolCalls: []memctx.ToolCallRef{{ID: "call_2", Name: "search"}}},
		makeToolResult("call_2", "The weather in Paris is rainy with a temperature of 15 degrees Celsius."),
		{Role: "assistant", Content: "Final answer"},
	}

	mc := MicroCompactor{KeepRecent: 1}
	result, err := mc.Compact(context.Background(), items, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// RetainedTail should be non-empty after compaction.
	if len(result.RetainedTail) == 0 {
		t.Fatal("expected non-empty RetainedTail after micro-compaction")
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
