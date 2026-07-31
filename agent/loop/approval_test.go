package loop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
)

// ─── Mock ApprovalHandler ────────────────────────────────────────

// mockApprovalHandler is a test ApprovalHandler that returns a predetermined decision.
type mockApprovalHandler struct {
	decision ApprovalDecision
	err error
	delay time.Duration // optional delay before responding
	mu sync.Mutex
	calls int
}

func (h *mockApprovalHandler) RequestApproval(ctx context.Context, _ *ApprovalRequest) (ApprovalDecision, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()

	if h.delay > 0 {
		select {
		case <-ctx.Done():
			return ApprovalTimeout, ctx.Err()
		case <-time.After(h.delay):
		}
	}

	return h.decision, h.err
}

func (h *mockApprovalHandler) callCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// ─── HITLManager Tests ───────────────────────────────────────────

// TestHITLManager_RequestApproval tests that an approval request works.
func TestHITLManager_RequestApproval(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalApprove}
	hitl := NewHITLManager(handler, 0)

	req := &ApprovalRequest{
		ToolCallID: "tc-1",
		ToolName: "safe_tool",
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	decision, err := hitl.RequestApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if decision != ApprovalApprove {
		t.Errorf("decision = %v, want ApprovalApprove", decision)
	}
}

// TestHITLManager_RequestApprovalDenied tests that denial works.
func TestHITLManager_RequestApprovalDenied(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalDeny}
	hitl := NewHITLManager(handler, 0)

	req := &ApprovalRequest{
		ToolCallID: "tc-2",
		ToolName: "dangerous_tool",
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	decision, err := hitl.RequestApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if decision != ApprovalDeny {
		t.Errorf("decision = %v, want ApprovalDeny", decision)
	}
}

// TestHITLManager_RequestApprovalTimeout tests that timeout works.
func TestHITLManager_RequestApprovalTimeout(t *testing.T) {
	handler := &mockApprovalHandler{
		decision: ApprovalApprove,
		delay: 5 * time.Second, // long delay to trigger timeout
	}
	hitl := NewHITLManager(handler, 50*time.Millisecond)

	req := &ApprovalRequest{
		ToolCallID: "tc-3",
		ToolName: "slow_tool",
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	decision, _ := hitl.RequestApproval(context.Background(), req)
	if decision != ApprovalTimeout {
		t.Errorf("decision = %v, want ApprovalTimeout", decision)
	}
}

// TestHITLManager_CacheDecision tests that cache stores decisions.
func TestHITLManager_CacheDecision(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalDeny}
	hitl := NewHITLManager(handler, 0)

	hitl.CacheDecision("approved_tool", ApprovalApprove)
	hitl.CacheDecision("denied_tool", ApprovalDeny)

	decision, ok := hitl.IsApproved("approved_tool")
	if !ok {
		t.Error("IsApproved(approved_tool): expected cache hit")
	}
	if decision != ApprovalApprove {
		t.Errorf("IsApproved(approved_tool) = %v, want ApprovalApprove", decision)
	}

	decision, ok = hitl.IsApproved("denied_tool")
	if !ok {
		t.Error("IsApproved(denied_tool): expected cache hit")
	}
	if decision != ApprovalDeny {
		t.Errorf("IsApproved(denied_tool) = %v, want ApprovalDeny", decision)
	}
}

// TestHITLManager_IsApproved tests cache lookup works.
func TestHITLManager_IsApproved(t *testing.T) {
	hitl := NewHITLManager(nil, 0)

	// No cache entry should return (0, false)
	decision, ok := hitl.IsApproved("unknown_tool")
	if ok {
		t.Error("IsApproved(unknown_tool): expected cache miss")
	}
	if decision != ApprovalApprove {
		t.Errorf("IsApproved(unknown_tool) decision = %v, want 0 (ApprovalApprove default)", decision)
	}

	// After caching
	hitl.CacheDecision("known_tool", ApprovalDeny)
	decision, ok = hitl.IsApproved("known_tool")
	if !ok {
		t.Error("IsApproved(known_tool): expected cache hit")
	}
	if decision != ApprovalDeny {
		t.Errorf("IsApproved(known_tool) = %v, want ApprovalDeny", decision)
	}
}

// TestHITLManager_ClearCache tests that cache clear works.
func TestHITLManager_ClearCache(t *testing.T) {
	hitl := NewHITLManager(nil, 0)

	hitl.CacheDecision("tool_a", ApprovalApprove)
	hitl.CacheDecision("tool_b", ApprovalDeny)

	hitl.ClearCache()

	_, ok := hitl.IsApproved("tool_a")
	if ok {
		t.Error("IsApproved(tool_a) after ClearCache: expected cache miss")
	}

	_, ok = hitl.IsApproved("tool_b")
	if ok {
		t.Error("IsApproved(tool_b) after ClearCache: expected cache miss")
	}
}

// ─── ApprovalHook Tests ──────────────────────────────────────────

// TestApprovalHook_BeforeApproved tests that approval hook allows approved tool.
func TestApprovalHook_BeforeApproved(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalApprove}
	hitl := NewHITLManager(handler, 0)

	hook := NewApprovalHook(hitl, nil, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-1",
		Name: "safe_tool",
		Arguments: map[string]any{"key": "value"},
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	result, err := hook.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if result.Block {
		t.Errorf("Block = %v, want false (approved)", result.Block)
	}
}

// TestApprovalHook_BeforeDenied tests that approval hook blocks denied tool.
func TestApprovalHook_BeforeDenied(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalDeny}
	hitl := NewHITLManager(handler, 0)

	hook := NewApprovalHook(hitl, nil, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-2",
		Name: "dangerous_tool",
		Arguments: map[string]any{},
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	result, err := hook.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if !result.Block {
		t.Errorf("Block = %v, want true (denied)", result.Block)
	}
}

// TestApprovalHook_BeforeCacheHit tests that approval hook uses cache.
func TestApprovalHook_BeforeCacheHit(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalDeny} // would deny if called
	hitl := NewHITLManager(handler, 0)

	// Pre-approve the tool in cache
	hitl.CacheDecision("cached_tool", ApprovalApprove)

	hook := NewApprovalHook(hitl, nil, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-3",
		Name: "cached_tool",
		Arguments: map[string]any{},
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	result, err := hook.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if result.Block {
		t.Errorf("Block = %v, want false (cached approval)", result.Block)
	}

	// Verify handler was NOT called (cache hit)
	if handler.callCount() != 0 {
		t.Errorf("handler called %d times, want 0 (should use cache)", handler.callCount())
	}
}

// TestApprovalHook_After tests that after hook is no-op.
func TestApprovalHook_After(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalApprove}
	hitl := NewHITLManager(handler, 0)

	hook := NewApprovalHook(hitl, nil, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-4",
		Name: "some_tool",
		Arguments: map[string]any{},
	}
	result := &toolhook.ToolResult{
		Content: "tool output",
	}

	afterResult, err := hook.After(context.Background(), call, result)
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	if afterResult.Terminate {
		t.Errorf("Terminate = %v, want false (no-op)", afterResult.Terminate)
	}
	if afterResult.ModifiedResult != nil {
		t.Errorf("ModifiedResult should be nil (no-op), got %v", afterResult.ModifiedResult)
	}
}

// TestApprovalHandlerFunc tests that function adapter works.
func TestApprovalHandlerFunc(t *testing.T) {
	called := false
	fn := ApprovalHandlerFunc(func(_ context.Context, req *ApprovalRequest) (ApprovalDecision, error) {
		called = true
		if req.ToolName == "approved" {
			return ApprovalApprove, nil
		}
		return ApprovalDeny, nil
	})

	req := &ApprovalRequest{ToolName: "approved"}
	decision, err := fn.RequestApproval(context.Background(), req)
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if !called {
		t.Error("function was not called")
	}
	if decision != ApprovalApprove {
		t.Errorf("decision = %v, want ApprovalApprove", decision)
	}

	// Test denied path
	req2 := &ApprovalRequest{ToolName: "denied"}
	decision2, err := fn.RequestApproval(context.Background(), req2)
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if decision2 != ApprovalDeny {
		t.Errorf("decision = %v, want ApprovalDeny", decision2)
	}
}

// ─── Additional Tests ────────────────────────────────────────────

// TestApprovalHook_EventApprovalRequest tests that EventApprovalRequest is emitted.
func TestApprovalHook_EventApprovalRequest(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalApprove}
	hitl := NewHITLManager(handler, 0)

	eventCh := make(chan event.AgentEvent, 1)
	hook := NewApprovalHook(hitl, eventCh, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-event",
		Name: "tool_with_event",
		Arguments: map[string]any{"arg": "val"},
		SessionID: "sess-1",
		TurnID: "turn-1",
	}

	_, _ = hook.Before(context.Background(), call)

	// Verify EventApprovalRequest was emitted
	select {
	case evt := <-eventCh:
		if evt.Type != event.EventApprovalRequest {
			t.Errorf("event type = %v, want EventApprovalRequest", evt.Type)
		}
		if evt.SubmissionID != "sub-1" {
			t.Errorf("submissionID = %q, want %q", evt.SubmissionID, "sub-1")
		}
		if evt.SessionID != "sess-1" {
			t.Errorf("sessionID = %q, want %q", evt.SessionID, "sess-1")
		}
		payload, ok := evt.Payload.(*ApprovalRequest)
		if !ok {
			t.Fatalf("payload type = %T, want *ApprovalRequest", evt.Payload)
		}
		if payload.ToolName != "tool_with_event" {
			t.Errorf("payload ToolName = %q, want %q", payload.ToolName, "tool_with_event")
		}
	default:
		t.Error("no event emitted on eventCh")
	}
}

// TestHITLManager_HandlerError tests that handler error returns ApprovalDeny.
func TestHITLManager_HandlerError(t *testing.T) {
	handler := &mockApprovalHandler{err: errors.New("handler error")}
	hitl := NewHITLManager(handler, 0)

	req := &ApprovalRequest{ToolName: "error_tool"}
	decision, err := hitl.RequestApproval(context.Background(), req)
	if err == nil {
		t.Error("expected error, got nil")
	}
	if decision != ApprovalDeny {
		t.Errorf("decision = %v, want ApprovalDeny on error", decision)
	}
}

// TestHITLManager_NilHandler tests that nil handler returns error.
func TestHITLManager_NilHandler(t *testing.T) {
	hitl := NewHITLManager(nil, 0)

	req := &ApprovalRequest{ToolName: "any_tool"}
	decision, err := hitl.RequestApproval(context.Background(), req)
	if err == nil {
		t.Error("expected error for nil handler, got nil")
	}
	if decision != ApprovalDeny {
		t.Errorf("decision = %v, want ApprovalDeny", decision)
	}
}

// TestApprovalHook_CacheDeniedHit tests that cached denial blocks execution.
func TestApprovalHook_CacheDeniedHit(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalApprove} // would approve if called
	hitl := NewHITLManager(handler, 0)

	// Pre-deny the tool in cache
	hitl.CacheDecision("denied_cache_tool", ApprovalDeny)

	hook := NewApprovalHook(hitl, nil, "sub-1", "sess-1", "turn-1")

	call := &toolhook.ToolCall{
		ID: "tc-deny-cache",
		Name: "denied_cache_tool",
		Arguments: map[string]any{},
	}

	result, err := hook.Before(context.Background(), call)
	if err != nil {
		t.Fatalf("Before: %v", err)
	}
	if !result.Block {
		t.Errorf("Block = %v, want true (cached denial)", result.Block)
	}

	// Verify handler was NOT called (cache hit)
	if handler.callCount() != 0 {
		t.Errorf("handler called %d times, want 0 (should use cache)", handler.callCount())
	}
}
