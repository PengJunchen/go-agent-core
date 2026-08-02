package compactor

import (
	"context"
	"strings"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	memctx "github.com/pengjunchen/go-agent-core/memory/context"
)

// AC-2: SummaryCompactor supports incremental summary update.
func TestSummaryCompactor_UpdateSummary(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "updated summary with new events"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)

	existingSummary := "Previous conversation summary: user asked about Go."
	newItems := []memctx.TurnItem{
		{Role: "user", Content: "How do I test in Go?"},
		{Role: "assistant", Content: "Use the testing package."},
	}

	result, err := compactor.UpdateSummary(context.Background(), existingSummary, newItems)
	if err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}

	if result != "updated summary with new events" {
		t.Errorf("result = %q, want %q", result, "updated summary with new events")
	}

	// Verify the LLM was called once
	if mock.genCalls != 1 {
		t.Errorf("genCalls = %d, want 1", mock.genCalls)
	}

	// Verify the prompt contains existing summary and new items
	if len(mock.lastMessages) < 1 {
		t.Fatal("expected at least 1 message sent to Generate")
	}
	promptText := ""
	for _, c := range mock.lastMessages[0].Content {
		if c.Type == message.ContentText {
			promptText = c.Text
			break
		}
	}
	if !strings.Contains(promptText, existingSummary) {
		t.Error("prompt should contain existing summary")
	}
	if !strings.Contains(promptText, "How do I test in Go?") {
		t.Error("prompt should contain new items content")
	}
}

// AC-2: UpdateSummary with empty new items returns existing summary unchanged.
func TestSummaryCompactor_UpdateSummary_EmptyItems(t *testing.T) {
	mock := &mockModelProvider{}
	compactor := NewSummaryCompactor(mock, nil)

	result, err := compactor.UpdateSummary(context.Background(), "existing summary", nil)
	if err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}
	if result != "existing summary" {
		t.Errorf("result = %q, want %q", result, "existing summary")
	}
	if mock.genCalls != 0 {
		t.Errorf("genCalls = %d, want 0 for empty items", mock.genCalls)
	}
}

// AC-2: UpdateSummary with empty existing summary still works.
func TestSummaryCompactor_UpdateSummary_EmptyExistingSummary(t *testing.T) {
	mock := &mockModelProvider{genResp: &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "fresh summary"},
		},
	}}
	compactor := NewSummaryCompactor(mock, nil)

	newItems := []memctx.TurnItem{
		{Role: "user", Content: "Hello"},
	}

	result, err := compactor.UpdateSummary(context.Background(), "", newItems)
	if err != nil {
		t.Fatalf("UpdateSummary: %v", err)
	}
	if result != "fresh summary" {
		t.Errorf("result = %q, want %q", result, "fresh summary")
	}
}

// AC-2: UpdateSummary propagates LLM errors.
func TestSummaryCompactor_UpdateSummary_LLMError(t *testing.T) {
	mock := &mockModelProvider{
		genErr: context.DeadlineExceeded,
	}
	compactor := NewSummaryCompactor(mock, nil)

	newItems := []memctx.TurnItem{
		{Role: "user", Content: "Hello"},
	}

	_, err := compactor.UpdateSummary(context.Background(), "existing", newItems)
	if err == nil {
		t.Fatal("expected error when LLM fails")
	}
}
