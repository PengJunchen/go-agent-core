package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── Mock Provider ──────────────────────────────────────────────

// mockProvider 是用于测试的 ModelProvider 实现。
type mockProvider struct {
	mu sync.Mutex
	responses [][]stream.StreamEvent
	callCount int
}

func newMockProvider(responses [][]stream.StreamEvent) *mockProvider {
	return &mockProvider{
		responses: responses,
	}
}

func (m *mockProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

	ch := make(chan stream.StreamEvent, 64)

	var resp []stream.StreamEvent
	if idx < len(m.responses) {
		resp = m.responses[idx]
	} else {
		resp = []stream.StreamEvent{{Type: stream.StreamDone}}
	}

	go func() {
		defer close(ch)
		for _, evt := range resp {
			ch <- evt
		}
	}()

	return ch, nil
}

func (m *mockProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock response"}},
	}, nil
}

func (m *mockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "mock", ModelName: "mock-model"}
}

// ─── 辅助函数 ──────────────────────────────────────────────────────

// setupSubAgent 创建一个 DefaultSubAgent，使用给定的 mockProvider 响应。
func setupSubAgent(name string, responses [][]stream.StreamEvent, maxTurns int) (*DefaultSubAgent, *mockProvider) {
	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	return setupSubAgentWithRegistry(name, p, cm, tr, maxTurns)
}

// setupSubAgentWithRegistry 使用指定的 Provider 和 ToolRegistry 创建 DefaultSubAgent。
func setupSubAgentWithRegistry(name string, p provider.ModelProvider, cm ctxpkg.ContextManager, tr registry.ToolRegistry, maxTurns int) (*DefaultSubAgent, *mockProvider) {
	cfg := &loop.LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: maxTurns,
	}
	if maxTurns <= 0 {
		cfg.MaxTurns = loop.DefaultMaxTurns
	}

	agent, err := loop.NewDefaultLoopAgent(cfg)
	if err != nil {
		panic(fmt.Sprintf("setupSubAgentWithRegistry: %v", err))
	}

	mp, _ := p.(*mockProvider)
	return NewDefaultSubAgent(name, agent), mp
}

// collectSubAgentEvents 从事件通道收集所有事件，直到通道关闭或超时。
func collectSubAgentEvents(ch <-chan SubAgentEvent, timeout time.Duration) []SubAgentEvent {
	var events []SubAgentEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-timer.C:
			return events
		}
	}
}

// subAgentEventTypes 提取事件类型列表。
func subAgentEventTypes(events []SubAgentEvent) []event.EventType {
	types := make([]event.EventType, len(events))
	for i, e := range events {
		types[i] = e.EventType
	}
	return types
}

// hasSubAgentEventType 检查事件列表中是否包含指定类型。
func hasSubAgentEventType(events []SubAgentEvent, t event.EventType) bool {
	for _, e := range events {
		if e.EventType == t {
			return true
		}
	}
	return false
}

// ─── 测试用例 ──────────────────────────────────────────────────────

// TestSubAgent_RunAndEvents 测试事件正确转发。
func TestSubAgent_RunAndEvents(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamTextDelta, Content: " from sub"},
			{Type: stream.StreamDone},
		},
	}

	sub, _ := setupSubAgent("test-agent", responses, 0)

	err := sub.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := collectSubAgentEvents(sub.Events(), 5*time.Second)

	// 验证关键事件被转发
	if !hasSubAgentEventType(events, event.EventTurnStart) {
		t.Errorf("missing EventTurnStart, got %v", subAgentEventTypes(events))
	}
	if !hasSubAgentEventType(events, event.EventTextDelta) {
		t.Errorf("missing EventTextDelta, got %v", subAgentEventTypes(events))
	}
	if !hasSubAgentEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd, got %v", subAgentEventTypes(events))
	}
	if !hasSubAgentEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", subAgentEventTypes(events))
	}

	// 验证事件携带 SubAgent 上下文
	for _, e := range events {
		if e.AgentName != "test-agent" {
			t.Errorf("AgentName = %q, want %q", e.AgentName, "test-agent")
		}
	}

	// 验证文本内容
	var textContent string
	for _, e := range events {
		if e.EventType == event.EventTextDelta {
			if text, ok := e.Payload.(string); ok {
				textContent += text
			}
		}
	}
	if textContent != "Hello from sub" {
		t.Errorf("text content = %q, want %q", textContent, "Hello from sub")
	}

	// 清理
	_ = sub.Close()
}

// TestSubAgent_Wait 测试 Wait 返回正确结果。
func TestSubAgent_Wait(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Result"},
			{Type: stream.StreamDone},
		},
	}

	sub, _ := setupSubAgent("wait-agent", responses, 0)

	err := sub.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	result, err := sub.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if result.Name != "wait-agent" {
		t.Errorf("result Name = %q, want %q", result.Name, "wait-agent")
	}
	if result.Output != "Result" {
		t.Errorf("result Output = %q, want %q", result.Output, "Result")
	}
	if result.Error != nil {
		t.Errorf("result Error = %v, want nil", result.Error)
	}
	if len(result.Events) == 0 {
		t.Error("result Events is empty")
	}

	// 清理
	_ = sub.Close()
}

// TestSubAgent_Interrupt 测试中断传播。
func TestSubAgent_Interrupt(t *testing.T) {
	// 使用慢速 provider 确保中断时有活跃执行
	slowP := &slowMockProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &loop.LoopAgentConfig{
		Provider: slowP,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: loop.DefaultMaxTurns,
	}

	agent, err := loop.NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	sub := NewDefaultSubAgent("interrupt-agent", agent)

	err = sub.Run(context.Background(), "long task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 短暂等待确保 goroutine 已启动
	time.Sleep(50 * time.Millisecond)

	// 中断
	err = sub.Interrupt(context.Background())
	if err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	// Wait 应返回（可能是 canceled 路径）
	result, err := sub.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait after Interrupt: %v", err)
	}

	if result.Name != "interrupt-agent" {
		t.Errorf("result Name = %q, want %q", result.Name, "interrupt-agent")
	}

	// 清理
	_ = sub.Close()
}

// slowMockProvider 是一个慢速 mock provider，用于测试 Interrupt。
type slowMockProvider struct{}

func (m *slowMockProvider) StreamChat(ctx context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
			ch <- stream.StreamEvent{Type: stream.StreamTextDelta, Content: "done"}
			ch <- stream.StreamEvent{Type: stream.StreamDone}
		}
	}()
	return ch, nil
}

func (m *slowMockProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "slow"}},
	}, nil
}

func (m *slowMockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "slow-mock", ModelName: "slow"}
}

// TestSubAgent_EventProxyAllTypes 测试所有事件类型都被代理。
func TestSubAgent_EventProxyAllTypes(t *testing.T) {
	// 工具调用场景：覆盖更多事件类型
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Checking..."},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "test_tool",
				Arguments: map[string]any{"x": 1},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Done."},
			{Type: stream.StreamDone},
		},
	}

	// 先注册工具再创建 SubAgent
	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	err := tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "test_tool",
		Description: "A test tool",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "tool result"}, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	sub, _ := setupSubAgentWithRegistry("proxy-agent", p, cm, tr, 0)

	err = sub.Run(context.Background(), "call tool")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := collectSubAgentEvents(sub.Events(), 5*time.Second)
	types := subAgentEventTypes(events)

	// 验证关键事件类型都被代理
	expectedTypes := []event.EventType{
		event.EventTurnStart,
		event.EventTextDelta,
		event.EventToolCallStart,
		event.EventToolCallResult,
		event.EventTurnEnd,
		event.EventCompleted,
	}

	for _, expected := range expectedTypes {
		found := false
		for _, got := range types {
			if got == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing event type %v in %v", expected, types)
		}
	}

	// 验证 Original 字段完整保留
	for _, e := range events {
		if e.Original.Type != e.EventType {
			t.Errorf("Original.Type = %v, EventType = %v, mismatch", e.Original.Type, e.EventType)
		}
		if e.Original.Timestamp != e.Timestamp {
			t.Errorf("Original.Timestamp = %d, Timestamp = %d, mismatch", e.Original.Timestamp, e.Timestamp)
		}
	}

	_ = sub.Close()
}

// TestSubAgent_Close 测试清理正确性。
func TestSubAgent_Close(t *testing.T) {
	sub, _ := setupSubAgent("close-agent", nil, 0)

	err := sub.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 关闭后事件通道应已关闭
	_, ok := <-sub.Events()
	if ok {
		t.Error("expected event channel to be closed after Close")
	}
}

// TestSubAgent_WaitContextCanceled 测试 Wait 在上下文取消时返回错误。
func TestSubAgent_WaitContextCanceled(t *testing.T) {
	slowP := &slowMockProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &loop.LoopAgentConfig{
		Provider: slowP,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: loop.DefaultMaxTurns,
	}

	agent, err := loop.NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	sub := NewDefaultSubAgent("cancel-agent", agent)

	err = sub.Run(context.Background(), "long task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 使用很快超时的 context
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := sub.Wait(ctx)
	if err == nil {
		t.Error("expected error from Wait with cancelled context")
		_ = sub.Close()
		return
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}

	// 清理：中断正在运行的任务
	_ = sub.Interrupt(context.Background())
	// 等待子代理完成
	_, _ = sub.Wait(context.Background())
	_ = sub.Close()
}

// TestSubAgent_InterfaceCompliance 编译时校验 DefaultSubAgent 实现了 SubAgent。
func TestSubAgent_InterfaceCompliance(t *testing.T) {
	var _ SubAgent = (*DefaultSubAgent)(nil)
}

// TestSubAgent_RunReturnsErrorOnBadState 测试在错误状态下 Run 返回错误。
func TestSubAgent_RunReturnsErrorOnBadState(t *testing.T) {
	sub, _ := setupSubAgent("bad-state-agent", nil, 0)

	// 关闭后再 Run 应返回错误
	_ = sub.Close()

	err := sub.Run(context.Background(), "test")
	if err == nil {
		t.Error("expected error when running on closed agent")
	}
}

// TestSubAgent_SendFollowUp 测试 Send 委托给 FollowUp。
func TestSubAgent_SendFollowUp(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "First."},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Follow-up."},
			{Type: stream.StreamDone},
		},
	}

	sub, _ := setupSubAgent("send-agent", responses, 0)

	err := sub.Run(context.Background(), "first question")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 等待第一次执行完成
	result, err := sub.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if result.Output != "First." {
		t.Errorf("first output = %q, want %q", result.Output, "First.")
	}

	// Send（FollowUp）— FollowUp 在 DefaultLoopAgent 内部排队新一轮查询
	err = sub.Send(context.Background(), "follow-up question")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// 等待 FollowUp 触发的查询完成：通过事件通道或足够超时
	collectSubAgentEvents(sub.Events(), 5*time.Second)

	_ = sub.Close()
}
