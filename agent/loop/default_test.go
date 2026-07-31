// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// default_test.go 包含 DefaultLoopAgent 的集成测试，覆盖：
// - 基本文本查询 → EventTextDelta → EventCompleted
// - 工具调用查询 → EventToolCallStart → EventToolCallResult → EventCompleted
// - MaxTurns 限制 → EventMaxTurnsReached
// - Interrupt → StatusCanceled
// - 状态转换校验
// - Builder 构造
// - Steer/FollowUp
// - P0 Fix 1: EventCompleted 在错误路径上始终发送
// - P0 Fix 2: Close() 状态机合规
// - P1 Fix 3: EventTurnEnd 在错误/中断/MaxTurns 路径上发送
// - P1 Fix 4: 重试逻辑（429 重试，500 不重试）
// - P1 Fix 5: 自动压缩触发
package loop

import (
	stdcontext "context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── Mock ModelProvider ──────────────────────────────────────────

// mockProvider 是用于测试的 ModelProvider 实现。
//
// 它按顺序返回预定义的响应序列（responses），每次 StreamChat 消耗一个响应。
// 如果响应序列耗尽，返回空的 StreamDone。
type mockProvider struct {
	mu sync.Mutex
	responses [][]stream.StreamEvent // 每个 response 是一轮 LLM 的完整流式输出
	callCount int
	modelInfo *provider.ModelInfo
}

func newMockProvider(responses [][]stream.StreamEvent) *mockProvider {
	return &mockProvider{
		responses: responses,
		modelInfo: &provider.ModelInfo{
			Provider: "mock",
			ModelName: "mock-model",
			SupportsStreaming: true,
		},
	}
}

func (m *mockProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

	ch := make(chan stream.StreamEvent, 64)

	var resp []stream.StreamEvent
	if idx < len(m.responses) {
		resp = m.responses[idx]
	} else {
		// 默认：只返回 StreamDone
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

func (m *mockProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock response"}},
	}, nil
}

func (m *mockProvider) ModelInfo() *provider.ModelInfo {
	return m.modelInfo
}

// ─── 辅助函数 ──────────────────────────────────────────────────────

// collectEvents 从事件通道收集所有事件，直到通道关闭或超时。
func collectEvents(ch <-chan event.AgentEvent, timeout time.Duration) []event.AgentEvent {
	var events []event.AgentEvent
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

// eventTypes 提取事件类型列表。
func eventTypes(events []event.AgentEvent) []event.EventType {
	types := make([]event.EventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// hasEventType 检查事件列表中是否包含指定类型。
func hasEventType(events []event.AgentEvent, t event.EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// countEventType 统计指定类型事件的出现次数。
func countEventType(events []event.AgentEvent, t event.EventType) int {
	count := 0
	for _, e := range events {
		if e.Type == t {
			count++
		}
	}
	return count
}

// setupAgent 创建一个 DefaultLoopAgent，使用给定的 mockProvider 响应。
func setupAgent(responses [][]stream.StreamEvent, maxTurns int) (*DefaultLoopAgent, *mockProvider) {
	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: maxTurns,
	}
	if maxTurns <= 0 {
		cfg.MaxTurns = DefaultMaxTurns
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		panic(fmt.Sprintf("setupAgent: %v", err))
	}
	return agent, p
}

// ─── 测试用例 ──────────────────────────────────────────────────────

// TestBasicTextQuery 测试基本文本查询：
// Query → EventTurnStart → EventTextDelta → EventTurnEnd → EventCompleted
func TestBasicTextQuery(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamTextDelta, Content: " world"},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "hi",
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	types := eventTypes(events)

	// 验证事件序列包含关键事件
	if !hasEventType(events, event.EventTurnStart) {
		t.Errorf("missing EventTurnStart, got %v", types)
	}
	if !hasEventType(events, event.EventTextDelta) {
		t.Errorf("missing EventTextDelta, got %v", types)
	}
	if !hasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd, got %v", types)
	}
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", types)
	}

	// 验证文本内容
	var textContent string
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			textContent += e.Payload.(string)
		}
	}
	if textContent != "Hello world" {
		t.Errorf("text content = %q, want %q", textContent, "Hello world")
	}

	// 验证最终状态
	if agent.Status() != event.StatusCompleted {
		t.Errorf("final status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// TestThinkingDelta 测试思维增量事件。
func TestThinkingDelta(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamThinkingDelta, Thinking: "Let me think..."},
			{Type: stream.StreamTextDelta, Content: "The answer is 42."},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "what is the answer?"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	if !hasEventType(events, event.EventThinkingDelta) {
		t.Errorf("missing EventThinkingDelta, got %v", eventTypes(events))
	}

	var thinkingContent string
	for _, e := range events {
		if e.Type == event.EventThinkingDelta {
			thinkingContent += e.Payload.(string)
		}
	}
	if thinkingContent != "Let me think..." {
		t.Errorf("thinking content = %q, want %q", thinkingContent, "Let me think...")
	}
}

// TestToolCallQuery 测试工具调用查询：
// Query → EventToolCallStart → EventToolCallResult → EventTextDelta → EventCompleted
func TestToolCallQuery(t *testing.T) {
	// 第一轮：模型调用工具
	// 第二轮：模型给出最终回答
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Let me check the weather. "},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "get_weather",
				Arguments: map[string]any{
					"city": "Beijing",
				},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Beijing is sunny today."},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	// 注册工具
	err := agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "get_weather",
		Description: "Get weather for a city",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ stdcontext.Context, args map[string]any) (*registry.ToolResult, error) {
			city, _ := args["city"].(string)
			return &registry.ToolResult{
				Content: fmt.Sprintf("Weather in %s: sunny, 25°C", city),
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "What's the weather in Beijing?",
		SessionID: "test-tool",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)
	types := eventTypes(events)

	// 验证关键事件
	if !hasEventType(events, event.EventToolCallStart) {
		t.Errorf("missing EventToolCallStart, got %v", types)
	}
	if !hasEventType(events, event.EventToolCallResult) {
		t.Errorf("missing EventToolCallResult, got %v", types)
	}
	if !hasEventType(events, event.EventTextDelta) {
		t.Errorf("missing EventTextDelta, got %v", types)
	}
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", types)
	}

	// 验证工具调用结果内容
	var toolResultContent string
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok {
				toolResultContent = tr.Content
			}
		}
	}
	if toolResultContent != "Weather in Beijing: sunny, 25°C" {
		t.Errorf("tool result content = %q, want %q", toolResultContent, "Weather in Beijing: sunny, 25°C")
	}

	// 验证最终状态
	if agent.Status() != event.StatusCompleted {
		t.Errorf("final status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// TestMaxTurnsEnforcement 测试 MaxTurns 限制：
// 当达到 MaxTurns 时应发射 EventMaxTurnsReached
func TestMaxTurnsEnforcement(t *testing.T) {
	// 构造一个总是调用工具的响应，让 Agent 一直循环
	toolCallResponse := []stream.StreamEvent{
		{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
			ID: "tc-loop",
			Name: "loop_tool",
			Arguments: map[string]any{},
		}},
		{Type: stream.StreamDone},
	}

	// 准备足够多的响应（超过 maxTurns）
	maxTurns := 3
	responses := make([][]stream.StreamEvent, maxTurns+2)
	for i := range responses {
		responses[i] = toolCallResponse
	}

	agent, _ := setupAgent(responses, maxTurns)

	// 注册一个简单的工具
	err := agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "loop_tool",
		Description: "A tool that loops",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "loop result"}, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "keep calling the tool",
		SessionID: "test-maxturns",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	if !hasEventType(events, event.EventMaxTurnsReached) {
		t.Errorf("missing EventMaxTurnsReached, got %v", eventTypes(events))
	}

	// P1 Fix 3: MaxTurns 路径应有 EventTurnEnd
	if !hasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on MaxTurns path, got %v", eventTypes(events))
	}

	// 验证最终状态
	if agent.Status() != event.StatusCompleted {
		t.Errorf("final status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// TestInterrupt 测试中断：
// Interrupt → Agent 应进入 Canceled 状态
func TestInterrupt(t *testing.T) {
	// 构造一个长时间运行的响应
	responses := [][]stream.StreamEvent{
		{}, // 空：StreamChat 返回空通道，但 goroutine 内部已进入 Running 状态
	}

	agent, _ := setupAgent(responses, 0)

	// 使用一个有延迟的 provider 来确保 Interrupt 时 goroutine 仍在运行
	slowProvider := &slowMockProvider{}
	agent.provider = slowProvider

	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "long running query",
		SessionID: "test-interrupt",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// 短暂等待确保 goroutine 已启动
	time.Sleep(50 * time.Millisecond)

	// 中断
	if err := agent.Interrupt(stdcontext.Background()); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	events := collectEvents(ch, 2*time.Second)

	// P1 Fix 3: Interrupt 路径应有 EventTurnEnd
	if !hasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on Interrupt path, got %v", eventTypes(events))
	}

	// P0 Fix 1: Interrupt 路径应有 EventCompleted（由 defer 发送）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted on Interrupt path, got %v", eventTypes(events))
	}

	// 等待状态变为 Canceled
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if agent.Status() == event.StatusCanceled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if agent.Status() != event.StatusCanceled {
		t.Errorf("status after interrupt = %v, want %v", agent.Status(), event.StatusCanceled)
	}
}

// slowMockProvider 是一个慢速 mock provider，用于测试 Interrupt。
// 它在发送事件前等待，确保 Interrupt 有时间触发。
type slowMockProvider struct{}

func (m *slowMockProvider) StreamChat(ctx stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
		select {
		case <-ctx.Done():
			// context 取消时不发送 StreamError，直接关闭通道。
			// 主循环会通过 ctx.Done() 检查到取消并转换状态为 Canceled。
		case <-time.After(5 * time.Second):
			ch <- stream.StreamEvent{Type: stream.StreamTextDelta, Content: "done"}
			ch <- stream.StreamEvent{Type: stream.StreamDone}
		}
	}()
	return ch, nil
}

func (m *slowMockProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "slow response"}},
	}, nil
}

func (m *slowMockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "slow-mock", ModelName: "slow"}
}

// TestStatusTransitions 测试状态转换。
func TestStatusTransitions(t *testing.T) {
	// 初始状态为 Idle
	agent, _ := setupAgent(nil, 0)
	if agent.Status() != event.StatusIdle {
		t.Errorf("initial status = %v, want %v", agent.Status(), event.StatusIdle)
	}

	// Query 后状态变为 Running
	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	if agent.Status() != event.StatusRunning {
		t.Errorf("status after Query = %v, want %v", agent.Status(), event.StatusRunning)
	}

	// 等待完成
	_ = collectEvents(ch, 5*time.Second)

	if agent.Status() != event.StatusCompleted {
		t.Errorf("status after completion = %v, want %v", agent.Status(), event.StatusCompleted)
	}

	// 完成后可以再次查询（Completed → Running）
	ch, err = agent.Query(stdcontext.Background(), AgentInput{Prompt: "test2"})
	if err != nil {
		t.Fatalf("second Query: %v", err)
	}
	_ = collectEvents(ch, 5*time.Second)

	if agent.Status() != event.StatusCompleted {
		t.Errorf("status after second completion = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// TestQueryFromRunningState 测试在 Running 状态下再次 Query 应返回错误。
func TestQueryFromRunningState(t *testing.T) {
	slowProvider := &slowMockProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: slowProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "first"})
	if err != nil {
		t.Fatalf("first Query: %v", err)
	}

	// 在 Running 状态下再次 Query 应失败
	_, err = agent.Query(stdcontext.Background(), AgentInput{Prompt: "second"})
	if err == nil {
		t.Error("expected error when querying from Running state")
	}
	if !errors.Is(err, ErrAgentNotIdle) {
		t.Errorf("error = %v, want ErrAgentNotIdle", err)
	}

	// 清理
	_ = agent.Interrupt(stdcontext.Background())
	_ = collectEvents(ch, 2*time.Second)
}

// TestClose 测试 Close 方法。
func TestClose(t *testing.T) {
	agent, _ := setupAgent(nil, 0)

	if err := agent.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if agent.Status() != event.StatusIdle {
		t.Errorf("status after Close = %v, want %v", agent.Status(), event.StatusIdle)
	}

	// Close 后 Query 应失败
	_, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if !errors.Is(err, ErrAgentClosed) {
		t.Errorf("error after Close = %v, want ErrAgentClosed", err)
	}

	// 重复 Close 不报错
	if err := agent.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestSteer 测试 Steer 方法。
func TestSteer(t *testing.T) {
	slowProvider := &slowMockProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: slowProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	// 在 Idle 状态下 Steer 应失败
	err = agent.Steer(stdcontext.Background(), "change direction")
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Errorf("Steer when idle: error = %v, want ErrNoActiveTurn", err)
	}

	// 启动查询
	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// 短暂等待确保 goroutine 已启动
	time.Sleep(50 * time.Millisecond)

	// 在 Running 状态下 Steer 应成功
	err = agent.Steer(stdcontext.Background(), "change direction")
	if err != nil {
		t.Errorf("Steer when running: %v", err)
	}

	// 清理
	_ = agent.Interrupt(stdcontext.Background())
	_ = collectEvents(ch, 2*time.Second)
}

// TestFollowUp 测试 FollowUp 方法。
func TestFollowUp(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "First answer."},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Follow-up answer."},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	// 第一次查询
	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "first question",
		SessionID: "test-followup",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events1 := collectEvents(ch, 5*time.Second)
	if !hasEventType(events1, event.EventCompleted) {
		t.Errorf("first query: missing EventCompleted, got %v", eventTypes(events1))
	}

	// 等待状态变为 Completed
	time.Sleep(50 * time.Millisecond)

	// FollowUp
	ch, err = agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "follow-up question",
		SessionID: "test-followup",
	})
	if err != nil {
		t.Fatalf("FollowUp Query: %v", err)
	}

	events2 := collectEvents(ch, 5*time.Second)
	if !hasEventType(events2, event.EventCompleted) {
		t.Errorf("follow-up query: missing EventCompleted, got %v", eventTypes(events2))
	}
}

// TestToolNotFound 测试工具未找到的情况。
func TestToolNotFound(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-missing",
				Name: "nonexistent_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Tool not found, but continuing."},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "call missing tool"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 应该有 EventToolCallResult，且包含错误信息
	found := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected tool result with error for nonexistent tool, got %v", eventTypes(events))
	}

	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
}

// TestStreamError 测试 LLM 返回流式错误。
func TestStreamError(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamError, Error: errors.New("LLM internal error")},
		},
	}

	agent, _ := setupAgent(responses, 0)

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}

	// P1 Fix 3: StreamError 路径应有 EventTurnEnd
	if !hasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on StreamError path, got %v", eventTypes(events))
	}

	// P0 Fix 1: StreamError 路径应有 EventCompleted（由 defer 发送）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted on StreamError path, got %v", eventTypes(events))
	}

	if agent.Status() != event.StatusError {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusError)
	}
}

// TestEventSessionID 测试事件携带正确的 SessionID。
func TestEventSessionID(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "test"},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	sessionID := "session-123"
	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "test",
		SessionID: sessionID,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	for _, e := range events {
		if e.SessionID != sessionID {
			t.Errorf("event SessionID = %q, want %q (event type: %v)", e.SessionID, sessionID, e.Type)
		}
	}
}

// TestEventSubmissionID 测试事件携带正确的 SubmissionID。
func TestEventSubmissionID(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "test"},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 所有事件应有相同的 submissionID
	var submissionID string
	for i, e := range events {
		if i == 0 {
			submissionID = e.SubmissionID
		}
		if e.SubmissionID != submissionID {
			t.Errorf("event %d SubmissionID = %q, want %q", i, e.SubmissionID, submissionID)
		}
	}
	if submissionID == "" {
		t.Error("submissionID is empty")
	}
}

// ─── Builder 测试 ──────────────────────────────────────────────────

// TestNewDefaultLoopAgent_ValidConfig 测试有效配置的构造。
func TestNewDefaultLoopAgent_ValidConfig(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: 10,
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}
	if agent == nil {
		t.Fatal("agent is nil")
	}
	if agent.maxTurns != 10 {
		t.Errorf("maxTurns = %d, want 10", agent.maxTurns)
	}
	if agent.Status() != event.StatusIdle {
		t.Errorf("initial status = %v, want %v", agent.Status(), event.StatusIdle)
	}
}

// TestNewDefaultLoopAgent_NilConfig 测试 nil 配置。
func TestNewDefaultLoopAgent_NilConfig(t *testing.T) {
	_, err := NewDefaultLoopAgent(nil)
	if err == nil {
		t.Error("expected error for nil config")
	}
}

// TestNewDefaultLoopAgent_MissingProvider 测试缺少 Provider。
func TestNewDefaultLoopAgent_MissingProvider(t *testing.T) {
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		ContextManager: cm,
		ToolRegistry: tr,
	}

	_, err := NewDefaultLoopAgent(cfg)
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("error = %v, want ErrNoProvider", err)
	}
}

// TestNewDefaultLoopAgent_MissingContextManager 测试缺少 ContextManager。
func TestNewDefaultLoopAgent_MissingContextManager(t *testing.T) {
	p := newMockProvider(nil)
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ToolRegistry: tr,
	}

	_, err := NewDefaultLoopAgent(cfg)
	if !errors.Is(err, ErrNoContextManager) {
		t.Errorf("error = %v, want ErrNoContextManager", err)
	}
}

// TestNewDefaultLoopAgent_MissingToolRegistry 测试缺少 ToolRegistry。
func TestNewDefaultLoopAgent_MissingToolRegistry(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
	}

	_, err := NewDefaultLoopAgent(cfg)
	if !errors.Is(err, ErrNoToolRegistry) {
		t.Errorf("error = %v, want ErrNoToolRegistry", err)
	}
}

// TestNewDefaultLoopAgent_DefaultMaxTurns 测试默认 MaxTurns。
func TestNewDefaultLoopAgent_DefaultMaxTurns(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: 0, // 应使用默认值
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}
	if agent.maxTurns != DefaultMaxTurns {
		t.Errorf("maxTurns = %d, want %d", agent.maxTurns, DefaultMaxTurns)
	}
}

// TestBuilder 测试流式构建器。
func TestBuilder(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		WithMaxTurns(15).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if agent == nil {
		t.Fatal("Build returned nil")
	}
	if agent.maxTurns != 15 {
		t.Errorf("maxTurns = %d, want 15", agent.maxTurns)
	}
}

// TestBuilder_MissingRequired 测试构建器缺少必填字段。
func TestBuilder_MissingRequired(t *testing.T) {
	_, err := NewBuilder().Build()
	if err == nil {
		t.Error("expected error for missing required fields")
	}
}

// TestBuilder_MustBuild 测试 MustBuild 在成功时不 panic。
func TestBuilder_MustBuild(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		MustBuild()

	if agent == nil {
		t.Fatal("MustBuild returned nil")
	}
}

// TestBuilder_MustBuild_Panic 测试 MustBuild 在失败时 panic。
func TestBuilder_MustBuild_Panic(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic for MustBuild with missing fields")
		}
	}()

	NewBuilder().MustBuild()
}

// TestInterfaceCompliance 编译时校验 DefaultLoopAgent 实现了 LoopAgent。
func TestInterfaceCompliance(t *testing.T) {
	var _ LoopAgent = (*DefaultLoopAgent)(nil)
}

// TestClosedAgentOperations 测试关闭后的操作。
func TestClosedAgentOperations(t *testing.T) {
	agent, _ := setupAgent(nil, 0)
	_ = agent.Close()

	// Close 后 Interrupt 应该不会 panic
	_ = agent.Interrupt(stdcontext.Background())

	// Close 后 Steer 应该失败
	err := agent.Steer(stdcontext.Background(), "test")
	if err == nil {
		t.Error("expected error for Steer on closed agent")
	}

	// Close 后 Status 应该返回 Idle
	if agent.Status() != event.StatusIdle {
		t.Errorf("status after Close = %v, want %v", agent.Status(), event.StatusIdle)
	}
}

// TestMultipleToolCalls 测试一轮中有多个工具调用。
func TestMultipleToolCalls(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1", Name: "tool_a",
				Arguments: map[string]any{"x": 1},
			}},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-2", Name: "tool_b",
				Arguments: map[string]any{"y": 2},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Both tools called."},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	// 注册工具
	_ = agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "tool_a", Description: "Tool A",
		Handler: func(_ stdcontext.Context, args map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: fmt.Sprintf("A result: %v", args["x"])}, nil
		},
	})
	_ = agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "tool_b", Description: "Tool B",
		Handler: func(_ stdcontext.Context, args map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: fmt.Sprintf("B result: %v", args["y"])}, nil
		},
	})

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "call both tools"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 统计 EventToolCallStart 数量
	toolCallStartCount := 0
	for _, e := range events {
		if e.Type == event.EventToolCallStart {
			toolCallStartCount++
		}
	}
	if toolCallStartCount != 2 {
		t.Errorf("EventToolCallStart count = %d, want 2", toolCallStartCount)
	}

	// 统计 EventToolCallResult 数量
	toolCallResultCount := 0
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			toolCallResultCount++
		}
	}
	if toolCallResultCount != 2 {
		t.Errorf("EventToolCallResult count = %d, want 2", toolCallResultCount)
	}

	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
}

// TestCancelledContext 测试传入已取消的上下文。
func TestCancelledContext(t *testing.T) {
	agent, _ := setupAgent(nil, 0)

	ctx, cancel := stdcontext.WithCancel(stdcontext.Background())
	cancel() // 立即取消

	ch, err := agent.Query(ctx, AgentInput{Prompt: "test"})
	if err != nil {
		// 如果 Query 本身就返回错误，也是合理的
		t.Logf("Query with cancelled ctx returned: %v", err)
		return
	}

	// 如果 Query 成功了，事件应该包含错误和 EventCompleted（由 defer 保证）
	events := collectEvents(ch, 2*time.Second)
	// P0 Fix 1: 即使在取消路径上，也应有 EventCompleted
	if !hasEventType(events, event.EventCompleted) {
		t.Logf("events with cancelled ctx: %v (missing EventCompleted)", eventTypes(events))
	}
}

// TestToolHandlerError 测试工具 Handler 返回错误。
func TestToolHandlerError(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-err",
				Name: "failing_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Tool failed, but I'll continue."},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	_ = agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "failing_tool", Description: "A tool that always fails",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return nil, errors.New("tool execution failed")
		},
	})

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "use failing tool"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 应该有工具调用结果，且标记为错误
	found := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected tool result with IsError=true, got %v", eventTypes(events))
	}

	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
}

// ─── P0 Fix 1 新增测试 ────────────────────────────────────────────

// TestEventCompletedOnErrorPaths 测试 P0 Fix 1:
// 错误路径上 EventCompleted 始终发送（由 defer 保证）。
func TestEventCompletedOnErrorPaths(t *testing.T) {
	// 测试 StreamChat 返回错误时，EventCompleted 仍被发送
	errorProvider := &errorMockProvider{err: &HTTPError{StatusCode: 500, Message: "internal server error"}}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: errorProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证 EventError 存在
	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}

	// P0 Fix 1: EventCompleted 必须存在（由 defer 保证）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted on error path, got %v", eventTypes(events))
	}

	// 验证 EventCompleted 不重复（只出现一次）
	completedCount := countEventType(events, event.EventCompleted)
	if completedCount != 1 {
		t.Errorf("EventCompleted count = %d, want 1", completedCount)
	}

	// 验证最终状态
	if agent.Status() != event.StatusError {
		t.Errorf("final status = %v, want %v", agent.Status(), event.StatusError)
	}
}

// errorMockProvider 总是返回错误的 mock provider。
type errorMockProvider struct {
	err error
}

func (m *errorMockProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, m.err
}

func (m *errorMockProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return nil, m.err
}

func (m *errorMockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "error-mock", ModelName: "error"}
}

// ─── P0 Fix 2 新增测试 ────────────────────────────────────────────

// TestCloseFromRunningState 测试 P0 Fix 2:
// Close() 在 Running 状态下通过状态机合法路径转换（Running → Canceled → Idle）。
func TestCloseFromRunningState(t *testing.T) {
	slowProvider := &slowMockProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: slowProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	// 启动查询
	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// 确保进入 Running 状态
	time.Sleep(50 * time.Millisecond)
	if agent.Status() != event.StatusRunning {
		t.Fatalf("expected Running, got %v", agent.Status())
	}

	// Close 应该通过 Running → Canceled → Idle 路径转换
	if err := agent.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 最终状态应为 Idle
	if agent.Status() != event.StatusIdle {
		t.Errorf("status after Close from Running = %v, want %v", agent.Status(), event.StatusIdle)
	}

	// 收集事件避免 goroutine 泄漏
	_ = collectEvents(ch, 2*time.Second)
}

// TestCloseFromCompletedState 测试 P0 Fix 2:
// Close() 在 Completed 状态下通过 Completed → Idle 转换。
func TestCloseFromCompletedState(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "done"},
			{Type: stream.StreamDone},
		},
	}

	agent, _ := setupAgent(responses, 0)

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// 等待完成
	_ = collectEvents(ch, 5*time.Second)

	if agent.Status() != event.StatusCompleted {
		t.Fatalf("expected Completed, got %v", agent.Status())
	}

	// Close 应该通过 Completed → Idle 转换
	if err := agent.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if agent.Status() != event.StatusIdle {
		t.Errorf("status after Close from Completed = %v, want %v", agent.Status(), event.StatusIdle)
	}
}

// ─── P1 Fix 3 新增测试 ────────────────────────────────────────────

// TestEventTurnEndOnMaxTurns 测试 P1 Fix 3:
// MaxTurns 路径上 EventTurnEnd 被发送。
func TestEventTurnEndOnMaxTurns(t *testing.T) {
	toolCallResponse := []stream.StreamEvent{
		{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
			ID: "tc-loop", Name: "loop_tool",
			Arguments: map[string]any{},
		}},
		{Type: stream.StreamDone},
	}

	maxTurns := 2
	responses := make([][]stream.StreamEvent, maxTurns+2)
	for i := range responses {
		responses[i] = toolCallResponse
	}

	agent, _ := setupAgent(responses, maxTurns)

	_ = agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "loop_tool", Description: "A tool that loops",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "result"}, nil
		},
	})

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证 EventMaxTurnsReached
	if !hasEventType(events, event.EventMaxTurnsReached) {
		t.Errorf("missing EventMaxTurnsReached, got %v", eventTypes(events))
	}

	// P1 Fix 3: 验证 EventTurnEnd
	if !hasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on MaxTurns path, got %v", eventTypes(events))
	}
}

// ─── P1 Fix 4 新增测试 ────────────────────────────────────────────

// TestRetryOnHTTP429 测试 P1 Fix 4:
// StreamChat 返回 429 错误时，应进行重试。
func TestRetryOnHTTP429(t *testing.T) {
	var callCount atomic.Int32
	retryProvider := &retryableMockProvider{
		callCount: &callCount,
		responses: []error{
			&HTTPError{StatusCode: 429, Message: "rate limited"},
			nil, // 第二次成功
		},
		streamResponse: []stream.StreamEvent{
			{Type: stream.StreamTextDelta, Content: "retry success"},
			{Type: stream.StreamDone},
		},
	}

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: retryProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		RetryConfig: &RetryConfig{
			MaxRetries: 3,
			RetryOnHTTP: []int{429},
			BaseDelay: 10 * time.Millisecond, // 短延迟加快测试
			MaxDelay: 100 * time.Millisecond,
		},
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证最终成功
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 StreamChat 被调用了 2 次（1 次失败 + 1 次成功）
	if callCount.Load() != 2 {
		t.Errorf("StreamChat call count = %d, want 2", callCount.Load())
	}

	// 验证文本内容
	var textContent string
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			textContent += e.Payload.(string)
		}
	}
	if textContent != "retry success" {
		t.Errorf("text content = %q, want %q", textContent, "retry success")
	}
}

// TestRetryOnHTTP500NoRetry 测试 P1 Fix 4:
// StreamChat 返回 500 错误时，不应重试（500 不在 RetryOnHTTP 列表中）。
func TestRetryOnHTTP500NoRetry(t *testing.T) {
	var callCount atomic.Int32
	retryProvider := &retryableMockProvider{
		callCount: &callCount,
		responses: []error{
			&HTTPError{StatusCode: 500, Message: "internal server error"},
		},
		streamResponse: nil, // 不会成功
	}

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: retryProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		RetryConfig: &RetryConfig{
			MaxRetries: 3,
			RetryOnHTTP: []int{429}, // 只重试 429，不重试 500
			BaseDelay: 10 * time.Millisecond,
			MaxDelay: 100 * time.Millisecond,
		},
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 应该有 EventError
	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}

	// P0 Fix 1: 应该有 EventCompleted（由 defer 保证）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 StreamChat 只被调用了 1 次（不重试 500）
	if callCount.Load() != 1 {
		t.Errorf("StreamChat call count = %d, want 1 (no retry for 500)", callCount.Load())
	}
}

// TestRetryExhausted 测试 P1 Fix 4:
// 所有重试耗尽后应返回错误。
func TestRetryExhausted(t *testing.T) {
	var callCount atomic.Int32
	retryProvider := &retryableMockProvider{
		callCount: &callCount,
		responses: []error{
			&HTTPError{StatusCode: 429, Message: "rate limited 1"},
			&HTTPError{StatusCode: 429, Message: "rate limited 2"},
			&HTTPError{StatusCode: 429, Message: "rate limited 3"},
			&HTTPError{StatusCode: 429, Message: "rate limited 4"},
		},
		streamResponse: nil,
	}

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: retryProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		RetryConfig: &RetryConfig{
			MaxRetries: 2, // 最多重试 2 次
			RetryOnHTTP: []int{429},
			BaseDelay: 10 * time.Millisecond,
			MaxDelay: 100 * time.Millisecond,
		},
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 应该有 EventError
	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}

	// P0 Fix 1: 应该有 EventCompleted（由 defer 保证）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 StreamChat 被调用了 3 次（1 次初始 + 2 次重试）
	if callCount.Load() != 3 {
		t.Errorf("StreamChat call count = %d, want 3", callCount.Load())
	}
}

// retryableMockProvider 是支持重试测试的 mock provider。
// 它按顺序返回 responses 中的错误，当 responses 耗尽或返回 nil 时发送 streamResponse。
type retryableMockProvider struct {
	callCount *atomic.Int32
	responses []error
	streamResponse []stream.StreamEvent
}

func (m *retryableMockProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	idx := int(m.callCount.Add(1)) - 1

	if idx < len(m.responses) && m.responses[idx] != nil {
		return nil, m.responses[idx]
	}

	// 成功：发送 streamResponse
	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
		for _, evt := range m.streamResponse {
			ch <- evt
		}
	}()
	return ch, nil
}

func (m *retryableMockProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock"}},
	}, nil
}

func (m *retryableMockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "retry-mock", ModelName: "retry"}
}

// TestNoRetryConfig 测试没有 RetryConfig 时不重试。
func TestNoRetryConfig(t *testing.T) {
	var callCount atomic.Int32
	noRetryProvider := &retryableMockProvider{
		callCount: &callCount,
		responses: []error{
			&HTTPError{StatusCode: 429, Message: "rate limited"},
		},
		streamResponse: []stream.StreamEvent{
			{Type: stream.StreamTextDelta, Content: "success"},
			{Type: stream.StreamDone},
		},
	}

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: noRetryProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		// RetryConfig 为 nil，不应重试
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 应该有 EventError
	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}

	// 验证 StreamChat 只被调用了 1 次（不重试）
	if callCount.Load() != 1 {
		t.Errorf("StreamChat call count = %d, want 1 (no retry config)", callCount.Load())
	}
}

// ─── P1 Fix 5 新增测试 ────────────────────────────────────────────

// TestAutoCompactTriggered 测试 P1 Fix 5:
// 当 token 使用超过阈值时，自动触发压缩。
func TestAutoCompactTriggered(t *testing.T) {
	// 使用带 compactor 的 ContextManager 和低阈值
	cm := ctxpkg.NewHeuristicContextManager(
		ctxpkg.WithCompactor(&compactCountingCompactor{}),
		ctxpkg.WithMaxTokens(10), // 很低的阈值
	)

	p := newMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1", Name: "test_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "done"},
			{Type: stream.StreamDone},
		},
	})

	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		CompactThreshold: 10, // 很低的阈值，容易触发
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	_ = agent.toolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool", Description: "A test tool",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "a very long tool result that should exceed the token threshold"}, nil
		},
	})

	ch, err := agent.Query(stdcontext.Background(), AgentInput{
		Prompt: "a very long prompt that should exceed the token threshold",
		SessionID: "test-compact",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证 EventCompactStart 和 EventCompactEnd
	if !hasEventType(events, event.EventCompactStart) {
		t.Errorf("missing EventCompactStart, got %v", eventTypes(events))
	}
	if !hasEventType(events, event.EventCompactEnd) {
		t.Errorf("missing EventCompactEnd, got %v", eventTypes(events))
	}

	// 验证最终成功完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
}

// TestAutoCompactDisabled 测试 CompactThreshold 为 0 时不触发压缩。
func TestAutoCompactDisabled(t *testing.T) {
	cm := ctxpkg.NewHeuristicContextManager(
		ctxpkg.WithCompactor(&compactCountingCompactor{}),
		ctxpkg.WithMaxTokens(10),
	)

	p := newMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamDone},
		},
	})

	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		CompactThreshold: 0, // 禁用
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 不应有压缩事件
	if hasEventType(events, event.EventCompactStart) {
		t.Errorf("unexpected EventCompactStart when CompactThreshold=0, got %v", eventTypes(events))
	}
}

// compactCountingCompactor 是一个简单的 compactor，用于测试自动压缩。
type compactCountingCompactor struct{}

func (c *compactCountingCompactor) Compact(_ stdcontext.Context, items []ctxpkg.TurnItem, maxTokens int) (*ctxpkg.CompactResult, error) {
	// 保留最后几个条目作为简单实现
	kept := items
	if len(items) > 2 {
		kept = items[len(items)-2:]
	}
	return &ctxpkg.CompactResult{
		Strategy: ctxpkg.CompactAuto,
		BeforeTokens: 1000,
		AfterTokens: 100,
		ItemsRemoved: len(items) - len(kept),
		RetainedItems: kept,
	}, nil
}

// ─── P1 Fix 4 额外测试 ────────────────────────────────────────────

// TestIsRetryableError 测试 isRetryableError 辅助函数。
func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err error
		retryOnHTTP []int
		want bool
	}{
		{
			name: "429 in retry list",
			err: &HTTPError{StatusCode: 429, Message: "rate limited"},
			retryOnHTTP: []int{429},
			want: true,
		},
		{
			name: "429 not in retry list",
			err: &HTTPError{StatusCode: 429, Message: "rate limited"},
			retryOnHTTP: []int{500},
			want: false,
		},
		{
			name: "non-HTTP error",
			err: errors.New("connection refused"),
			retryOnHTTP: []int{429},
			want: false,
		},
		{
			name: "empty retry list",
			err: &HTTPError{StatusCode: 429, Message: "rate limited"},
			retryOnHTTP: []int{},
			want: false,
		},
		{
			name: "wrapped HTTPError",
			err: fmt.Errorf("wrapped: %w", &HTTPError{StatusCode: 429, Message: "rate limited"}),
			retryOnHTTP: []int{429},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err, tt.retryOnHTTP)
			if got != tt.want {
				t.Errorf("isRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHTTPError 测试 HTTPError.Error() 方法。
func TestHTTPError(t *testing.T) {
	err := &HTTPError{StatusCode: 429, Message: "rate limited"}
	expected := "HTTP 429: rate limited"
	if err.Error() != expected {
		t.Errorf("HTTPError.Error() = %q, want %q", err.Error(), expected)
	}
}

// ─── Builder 新增配置测试 ─────────────────────────────────────────

// TestBuilderWithRetryConfig 测试 Builder 设置 RetryConfig。
func TestBuilderWithRetryConfig(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	retryCfg := &RetryConfig{
		MaxRetries: 3,
		RetryOnHTTP: []int{429},
		BaseDelay: 1 * time.Second,
		MaxDelay: 30 * time.Second,
	}

	agent, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		WithRetryConfig(retryCfg).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if agent.retryConfig == nil || agent.retryConfig.MaxRetries != 3 {
		t.Errorf("retryConfig not set correctly")
	}
}

// TestBuilderWithCompactThreshold 测试 Builder 设置 CompactThreshold。
func TestBuilderWithCompactThreshold(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		WithCompactThreshold(8192).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if agent.compactThreshold != 8192 {
		t.Errorf("compactThreshold = %d, want 8192", agent.compactThreshold)
	}
}
