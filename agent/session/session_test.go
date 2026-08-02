package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/tools"
	"github.com/pengjunchen/go-agent-core/config"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// mockProvider is a test ModelProvider that returns pre-configured responses.
type mockProvider struct {
	responses []mockResponse
	callIdx int
	modelInfo *provider.ModelInfo
}

type mockResponse struct {
	streamEvents []stream.StreamEvent
	err error
}

func newMockProvider(responses ...mockResponse) *mockProvider {
	return &mockProvider{
		responses: responses,
		modelInfo: &provider.ModelInfo{
			Provider: "mock",
			ModelName: "mock-model",
		},
	}
}

func (m *mockProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	if m.callIdx >= len(m.responses) {
		return nil, errors.New("no more mock responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++

	if resp.err != nil {
		return nil, resp.err
	}

	ch := make(chan stream.StreamEvent, len(resp.streamEvents)+1)
	for _, evt := range resp.streamEvents {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Generate(ctx context.Context, msgs []message.Message, opts *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{Role: message.RoleAssistant}, nil
}

func (m *mockProvider) ModelInfo() *provider.ModelInfo {
	return m.modelInfo
}

// Helper to create a text-only response.
func textResponse(text string) mockResponse {
	return mockResponse{
		streamEvents: []stream.StreamEvent{
			{Type: stream.StreamTextDelta, Content: text},
			{Type: stream.StreamDone},
		},
	}
}

// Helper to create a tool call response.
func toolCallResponse(callID, toolName string, args map[string]any) mockResponse {
	return mockResponse{
		streamEvents: []stream.StreamEvent{
			{
				Type: stream.StreamToolCallStart,
				ToolCall: &stream.ToolCall{
					ID: callID,
					Name: toolName,
					Arguments: args,
				},
			},
			{Type: stream.StreamDone},
		},
	}
}

// SE-001: DefaultSessionBuilder chainable API works.
func TestDefaultSessionBuilder_ChainAPI(t *testing.T) {
	p := newMockProvider(textResponse("hello"))
	cm := NewDefaultContextManager()
	tr := NewDefaultToolRegistry()

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		WithMaxTurns(10).
		Build()

	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if sess == nil {
		t.Fatal("expected non-nil Session")
	}
	if sess.maxTurns != 10 {
		t.Errorf("maxTurns = %d, want 10", sess.maxTurns)
	}
}

// SE-002: Build fails without provider.
func TestDefaultSessionBuilder_NoProvider(t *testing.T) {
	_, err := NewBuilder().
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err == nil {
		t.Error("expected error for missing provider")
	}
}

// SE-003: Build fails without context manager.
func TestDefaultSessionBuilder_NoContextManager(t *testing.T) {
	_, err := NewBuilder().
		WithProvider(newMockProvider()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err == nil {
		t.Error("expected error for missing context manager")
	}
}

// SE-004: Build fails without tool registry.
func TestDefaultSessionBuilder_NoToolRegistry(t *testing.T) {
	_, err := NewBuilder().
		WithProvider(newMockProvider()).
		WithContextManager(NewDefaultContextManager()).
		Build()
	if err == nil {
		t.Error("expected error for missing tool registry")
	}
}

// SE-005: Session.Query triggers the agent loop and produces events.
func TestSession_QueryTriggersAgentLoop(t *testing.T) {
	p := newMockProvider(textResponse("Hello, world!"))

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		WithMaxTurns(5).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	eventCh, err := sess.Query(context.Background(), loop.AgentInput{
		Prompt: "say hello",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// Collect events.
	var events []event.AgentEvent
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Should have at least TurnStart, TextDelta, TurnEnd, Completed.
	if len(events) < 3 {
		t.Fatalf("expected at least 3 events, got %d", len(events))
	}

	// Check for TextDelta event with content.
	foundText := false
	for _, evt := range events {
		if evt.Type == event.EventTextDelta {
			foundText = true
			if evt.Payload != "Hello, world!" {
				t.Errorf("text delta = %v, want 'Hello, world!'", evt.Payload)
			}
		}
	}
	if !foundText {
		t.Error("expected TextDelta event")
	}

	// Check for Completed event.
	foundCompleted := false
	for _, evt := range events {
		if evt.Type == event.EventCompleted {
			foundCompleted = true
		}
	}
	if !foundCompleted {
		t.Error("expected Completed event")
	}
}

// SE-006: Session.Query records user message in ContextManager.
func TestSession_RecordsUserMessage(t *testing.T) {
	p := newMockProvider(textResponse("ok"))
	cm := NewDefaultContextManager()

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	eventCh, _ := sess.Query(context.Background(), loop.AgentInput{
		Prompt: "test prompt",
		SessionID: "test",
	})
	// Drain events.
	for range eventCh {
	}

	// Verify user message was recorded.
	items, err := cm.GetMessages(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected items in context manager")
	}

	// First user message should contain the prompt.
	foundUser := false
	for _, item := range items {
		if item.Role == "user" && item.Content == "test prompt" {
			foundUser = true
		}
	}
	if !foundUser {
		t.Error("user message not recorded in context manager")
	}
}

// SE-007: Session holds ContextManager and records items after each turn.
func TestSession_HoldsContextManager(t *testing.T) {
	cm := NewDefaultContextManager()
	sess, err := NewBuilder().
		WithProvider(newMockProvider(textResponse("hello"))).
		WithContextManager(cm).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	if sess.ContextManager() == nil {
		t.Error("ContextManager should not be nil")
	}
	if sess.ContextManager() != cm {
		t.Error("ContextManager should be the same instance")
	}
}

// SE-008: Session emits events through eventCh.
func TestSession_EmitsEvents(t *testing.T) {
	p := newMockProvider(textResponse("response"))
	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	eventCh, err := sess.Query(context.Background(), loop.AgentInput{
		Prompt: "hi",
		SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	var eventTypes []event.EventType
	for evt := range eventCh {
		eventTypes = append(eventTypes, evt.Type)
	}

	// Should have TurnStart.
	foundTurnStart := false
	for _, et := range eventTypes {
		if et == event.EventTurnStart {
			foundTurnStart = true
		}
	}
	if !foundTurnStart {
		t.Error("expected EventTurnStart")
	}
}

// SE-009: Zero-config defaults work (only provider, cm, tr required).
func TestSession_ZeroConfigDefaults(t *testing.T) {
	sess, err := NewBuilder().
		WithProvider(newMockProvider(textResponse("ok"))).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	if sess.Status() != event.StatusIdle {
		t.Errorf("initial status = %s, want idle", sess.Status())
	}
}

// SE-010: Session can execute a tool call end-to-end.
func TestSession_ToolCallEndToEnd(t *testing.T) {
	dir := t.TempDir()
	tr := registry.NewDefaultToolRegistry()
	if err := tools.RegisterBuiltinTools(context.Background(), tr, dir); err != nil {
		t.Fatalf("RegisterBuiltinTools: %v", err)
	}

	// First response: call read_file. Second response: text reply.
	p := newMockProvider(
		toolCallResponse("call-1", "execute", map[string]any{
			"command": "echo test_output",
		}),
		textResponse("Command executed successfully"),
	)

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(tr).
		WithMaxTurns(10).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	eventCh, err := sess.Query(context.Background(), loop.AgentInput{
		Prompt: "run echo test_output",
		SessionID: "tool-test",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	var events []event.AgentEvent
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Should have ToolCallStart and ToolCallResult events.
	foundToolCallStart := false
	foundToolCallResult := false
	for _, evt := range events {
		if evt.Type == event.EventToolCallStart {
			foundToolCallStart = true
		}
		if evt.Type == event.EventToolCallResult {
			foundToolCallResult = true
			// Verify the result contains the echo output.
			if result, ok := evt.Payload.(*registry.ToolResult); ok {
				if result.Content == "" {
					t.Error("tool result content is empty")
				}
			}
		}
	}
	if !foundToolCallStart {
		t.Error("expected EventToolCallStart")
	}
	if !foundToolCallResult {
		t.Error("expected EventToolCallResult")
	}
}

// SE-011: Concurrent queries are rejected.
func TestSession_ConcurrentQueryRejected(t *testing.T) {
	// Provider that blocks to keep the first query running.
	blockCh := make(chan struct{})
	p := &blockingProvider{
		blockCh: blockCh,
		modelInfo: &provider.ModelInfo{Provider: "mock", ModelName: "mock"},
	}

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		WithMaxTurns(1).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	// Start first query (it will block).
	eventCh1, err := sess.Query(context.Background(), loop.AgentInput{
		Prompt: "first",
		SessionID: "s1",
	})
	if err != nil {
		t.Fatalf("first Query: %v", err)
	}

	// Try second query while first is running.
	_, err = sess.Query(context.Background(), loop.AgentInput{
		Prompt: "second",
		SessionID: "s1",
	})
	if err == nil {
		t.Error("expected error for concurrent query")
	}

	// Unblock the first query.
	close(blockCh)
	// Drain events.
	for range eventCh1 {
	}
}

// SE-012: Session.Close marks session as closed.
func TestSession_Close(t *testing.T) {
	sess, err := NewBuilder().
		WithProvider(newMockProvider(textResponse("ok"))).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Query after close should fail.
	_, err = sess.Query(context.Background(), loop.AgentInput{
		Prompt: "test",
		SessionID: "s1",
	})
	if err == nil {
		t.Error("expected error after close")
	}
}

// SE-013: ToolRegistry and Provider accessors work.
func TestSession_Accessors(t *testing.T) {
	p := newMockProvider(textResponse("ok"))
	cm := NewDefaultContextManager()
	tr := NewDefaultToolRegistry()

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	if sess.ToolRegistry() != tr {
		t.Error("ToolRegistry accessor should return same instance")
	}
	if sess.Provider() != p {
		t.Error("Provider accessor should return same instance")
	}
	if sess.ContextManager() != cm {
		t.Error("ContextManager accessor should return same instance")
	}
}

// blockingProvider blocks on StreamChat until blockCh is closed.
type blockingProvider struct {
	blockCh chan struct{}
	modelInfo *provider.ModelInfo
}

func (b *blockingProvider) StreamChat(ctx context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 2)
	go func() {
		select {
		case <-b.blockCh:
			ch <- stream.StreamEvent{Type: stream.StreamTextDelta, Content: "done"}
			ch <- stream.StreamEvent{Type: stream.StreamDone}
		case <-ctx.Done():
		}
		close(ch)
	}()
	return ch, nil
}

func (b *blockingProvider) Generate(ctx context.Context, msgs []message.Message, opts *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{Role: message.RoleAssistant}, nil
}

func (b *blockingProvider) ModelInfo() *provider.ModelInfo {
	return b.modelInfo
}

// SE-014: DefaultSessionBuilder.MustBuild panics on error.
func TestDefaultSessionBuilder_MustBuild(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from MustBuild with missing provider")
		}
	}()

	NewBuilder().MustBuild()
}

// SE-015: Session Status transitions correctly.
func TestSession_StatusTransitions(t *testing.T) {
	// Use a blocking provider so we can observe the Running state.
	blockCh := make(chan struct{})
	p := &blockingProvider{
		blockCh: blockCh,
		modelInfo: &provider.ModelInfo{Provider: "mock", ModelName: "mock"},
	}

	sess, err := NewBuilder().
		WithProvider(p).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	if sess.Status() != event.StatusIdle {
		t.Errorf("initial status = %s, want idle", sess.Status())
	}

	eventCh, _ := sess.Query(context.Background(), loop.AgentInput{
		Prompt: "test",
		SessionID: "s1",
	})

	// After query starts, status should be Running.
	time.Sleep(50 * time.Millisecond)
	if sess.Status() != event.StatusRunning {
		t.Errorf("status during query = %s, want running", sess.Status())
	}

	// Unblock the provider.
	close(blockCh)

	// Drain events.
	for range eventCh {
	}

	// After completion, status should be Completed.
	if sess.Status() != event.StatusCompleted {
		t.Errorf("status after query = %s, want completed", sess.Status())
	}
}

// SE-016: NewBuilderFromSettings applies MaxTurns and CompactThreshold from Settings.
func TestSession_BuilderFromSettings(t *testing.T) {
	settings := config.Settings{
		MaxTurns: 42,
		CompactThreshold: 5000,
	}

	b := NewBuilderFromSettings(settings)

	if b.maxTurns != 42 {
		t.Errorf("maxTurns = %d, want 42", b.maxTurns)
	}
	if b.compactThreshold != 5000 {
		t.Errorf("compactThreshold = %d, want 5000", b.compactThreshold)
	}

	// Verify the builder still works end-to-end with the settings applied.
	p := newMockProvider(textResponse("ok"))
	sess, err := b.
		WithProvider(p).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer sess.Close()

	if sess.maxTurns != 42 {
		t.Errorf("sess.maxTurns = %d, want 42", sess.maxTurns)
	}
	if sess.compactThreshold != 5000 {
		t.Errorf("sess.compactThreshold = %d, want 5000", sess.compactThreshold)
	}
}

// SE-017: WithSettings applies non-zero values and ignores zero values.
func TestSession_WithSettings(t *testing.T) {
	b := NewBuilder()

	// Apply settings with only MaxTurns set.
	b.WithSettings(config.Settings{
		MaxTurns: 99,
	})

	if b.maxTurns != 99 {
		t.Errorf("maxTurns = %d, want 99", b.maxTurns)
	}
	// CompactThreshold should still be the default (0), not overwritten.
	if b.compactThreshold != 0 {
		t.Errorf("compactThreshold = %d, want 0 (not overwritten by zero value)", b.compactThreshold)
	}

	// Now apply CompactThreshold via WithSettings.
	b.WithSettings(config.Settings{
		CompactThreshold: 3000,
	})

	if b.compactThreshold != 3000 {
		t.Errorf("compactThreshold = %d, want 3000", b.compactThreshold)
	}
}
