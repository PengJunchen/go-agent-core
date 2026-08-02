package compactor

import (
	"strings"
	"testing"

	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// AC-1: splitTurn splits overly long tool results into multiple items.
func TestSplitTurn_LongToolResult(t *testing.T) {
	longContent := strings.Repeat("x", 300)
	items := []memctx.TurnItem{
		{Role: "assistant", Content: "calling tool", ToolCalls: []memctx.ToolCallRef{
			{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/tmp/big.go"}},
		}},
		{Role: "tool", Content: longContent, ToolCallID: "call_1", ToolName: "read_file"},
	}

	result := splitTurn(items, 100)
	if len(result) <= 2 {
		t.Fatalf("expected more than 2 items after split, got %d", len(result))
	}

	// All split parts should have the same ToolCallID and ToolName
	for _, item := range result {
		if item.Role == "tool" {
			if item.ToolCallID != "call_1" {
				t.Errorf("ToolCallID = %q, want %q", item.ToolCallID, "call_1")
			}
			if item.ToolName != "read_file" {
				t.Errorf("ToolName = %q, want %q", item.ToolName, "read_file")
			}
		}
	}

	// Each part should have a [part N/M] marker
	partCount := 0
	for _, item := range result {
		if item.Role == "tool" && strings.Contains(item.Content, "[part") {
			partCount++
		}
	}
	if partCount == 0 {
		t.Error("expected [part N/M] markers in split tool results")
	}
}

// splitTurn with short items returns unchanged.
func TestSplitTurn_ShortItems(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	result := splitTurn(items, 1000)
	if len(result) != 2 {
		t.Fatalf("expected 2 items (no split), got %d", len(result))
	}
}

// splitTurn preserves assistant messages.
func TestSplitTurn_PreservesAssistant(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "assistant", Content: "calling tool", ToolCalls: []memctx.ToolCallRef{
			{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/tmp/a.go"}},
		}},
		{Role: "tool", Content: strings.Repeat("y", 250), ToolCallID: "call_1", ToolName: "read_file"},
	}

	result := splitTurn(items, 80)
	// First item should be the assistant message, unchanged
	if result[0].Role != "assistant" {
		t.Errorf("first item role = %q, want assistant", result[0].Role)
	}
	if result[0].Content != "calling tool" {
		t.Errorf("first item content changed")
	}
	if len(result[0].ToolCalls) != 1 {
		t.Errorf("first item ToolCalls count = %d, want 1", len(result[0].ToolCalls))
	}
}

// splitTurn handles multiple tool results.
func TestSplitTurn_MultipleToolResults(t *testing.T) {
	items := []memctx.TurnItem{
		{Role: "assistant", Content: "calling tools", ToolCalls: []memctx.ToolCallRef{
			{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "/tmp/a.go"}},
			{ID: "call_2", Name: "read_file", Arguments: map[string]any{"path": "/tmp/b.go"}},
		}},
		{Role: "tool", Content: strings.Repeat("a", 250), ToolCallID: "call_1", ToolName: "read_file"},
		{Role: "tool", Content: strings.Repeat("b", 250), ToolCallID: "call_2", ToolName: "read_file"},
	}

	result := splitTurn(items, 80)
	if len(result) <= 4 {
		t.Fatalf("expected more than 4 items after split, got %d", len(result))
	}

	// Verify we still have tool results for both call IDs
	callIDs := make(map[string]bool)
	for _, item := range result {
		if item.Role == "tool" {
			callIDs[item.ToolCallID] = true
		}
	}
	if !callIDs["call_1"] {
		t.Error("missing tool result for call_1")
	}
	if !callIDs["call_2"] {
		t.Error("missing tool result for call_2")
	}
}

// splitTurn with empty items returns empty.
func TestSplitTurn_EmptyItems(t *testing.T) {
	result := splitTurn(nil, 100)
	if len(result) != 0 {
		t.Errorf("expected 0 items, got %d", len(result))
	}
}

// splitTurn: non-tool items longer than maxItemLength are not split.
func TestSplitTurn_NonToolNotSplit(t *testing.T) {
	longContent := strings.Repeat("z", 300)
	items := []memctx.TurnItem{
		{Role: "user", Content: longContent},
	}

	result := splitTurn(items, 100)
	if len(result) != 1 {
		t.Errorf("expected 1 item (user not split), got %d", len(result))
	}
	if result[0].Content != longContent {
		t.Error("user content should be unchanged")
	}
}
