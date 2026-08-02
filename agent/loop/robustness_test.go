// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// robustness_test.go 包含健壮性相关的测试：
// - 失败 Turn 合成（synthesized assistant message）
// - nil HITL 防御（ApprovalHook 空 handler 安全降级）
package loop

import (
	stdcontext "context"
	"errors"
	"strings"
	"testing"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── 失败 Turn 合成 测试 ──────────────────────────────────────────

// genErrorProvider 在 StreamChat 时返回错误的 mock provider。
type genErrorProvider struct {
	err error
}

func (p *genErrorProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, p.err
}

func (p *genErrorProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock"}},
	}, nil
}

func (p *genErrorProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "gen-error-mock", ModelName: "error"}
}

// TestFailedTurnSynthesis_RecordsAssistantMessage 验证可恢复错误时，
// 合成的助手消息被记录到 ContextManager。
func TestFailedTurnSynthesis_RecordsAssistantMessage(t *testing.T) {
	llmErr := errors.New("LLM service unavailable")
	p := &genErrorProvider{err: llmErr}
	cm := ctxpkg.NewHeuristicContextManager()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: nil,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-synth-session",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "hello",
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusError {
		t.Fatalf("status = %v, want %v", result.Status, event.StatusError)
	}

	// 验证 ContextManager 中存在合成消息
	items, err := cm.GetMessages(stdcontext.Background(), nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	foundSynthesized := false
	for _, item := range items {
		if item.Role == string(message.RoleAssistant) && item.Metadata != nil {
			if syn, ok := item.Metadata["synthesized"].(bool); ok && syn {
				foundSynthesized = true
				if !strings.Contains(item.Content, "I encountered an error") {
					t.Errorf("synthesized message content = %q, want to contain 'I encountered an error'", item.Content)
				}
				if !strings.Contains(item.Content, llmErr.Error()) {
					t.Errorf("synthesized message should contain error text %q, got %q", llmErr.Error(), item.Content)
				}
				break
			}
		}
	}

	if !foundSynthesized {
		t.Error("no synthesized assistant message found in ContextManager")
	}
}

// TestFailedTurnSynthesis_UserSeesExplanation 验证合成的消息作为
// EventTextDelta 发射到事件流，使用户看到解释。
func TestFailedTurnSynthesis_UserSeesExplanation(t *testing.T) {
	llmErr := errors.New("LLM internal error")
	p := &genErrorProvider{err: llmErr}
	cm := ctxpkg.NewHeuristicContextManager()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: nil,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-synth-event-session",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "hello",
	}

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusError {
		t.Fatalf("status = %v, want %v", result.Status, event.StatusError)
	}

	// 验证事件流中有合成的 EventTextDelta
	var synthesizedText string
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			if text, ok := e.Payload.(string); ok {
				if strings.Contains(text, "I encountered an error") {
					synthesizedText = text
				}
			}
		}
	}

	if synthesizedText == "" {
		t.Error("no synthesized EventTextDelta found in event stream")
	}

	if !strings.Contains(synthesizedText, llmErr.Error()) {
		t.Errorf("synthesized EventTextDelta should contain error text %q, got %q", llmErr.Error(), synthesizedText)
	}
}

// ─── nil HITL 防御 测试 ───────────────────────────────────────────

// TestNilHITL_DefaultsToBlock 验证当 HITL 为 nil 时，
// ApprovalHook.Before 不 panic，而是返回 Block=true。
func TestNilHITL_DefaultsToBlock(t *testing.T) {
	hook := NewApprovalHook(nil, nil, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-nil-hitl",
		Name: "dangerous_tool",
		Arguments: map[string]any{"key": "value"},
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	// 不应 panic
	result, err := hook.Before(stdcontext.Background(), call)
	if err != nil {
		t.Fatalf("Before with nil HITL returned error: %v", err)
	}
	if !result.Block {
		t.Error("Block = false, want true (should block when no HITL handler configured)")
	}
	if !strings.Contains(result.Reason, "no HITL handler configured") {
		t.Errorf("Reason = %q, want to contain 'no HITL handler configured'", result.Reason)
	}
}
