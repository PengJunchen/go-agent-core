// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// generator_test.go 包含 DefaultLoopGenerator 的单元测试，覆盖：
// - 接口合规性
// - 基本文本查询
// - 工具调用与结果
// - 多工具调用
// - MaxTurns 限制
// - Context 取消
// - Steer 消息注入
// - 重试逻辑（429）
// - Hook Before/After 拦截
// - 自动压缩
// - 中间件 BeforeTurn/AfterTurn
// - 并发安全
// - 工具未找到
// - 流式错误
// - 思维增量
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
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── Generator 专用 Mock ─────────────────────────────────────────

// genMockProvider 是用于 generator 测试的 ModelProvider 实现。
type genMockProvider struct {
	mu sync.Mutex
	responses [][]stream.StreamEvent
	callCount int
	modelInfo *provider.ModelInfo
}

func newGenMockProvider(responses [][]stream.StreamEvent) *genMockProvider {
	return &genMockProvider{
		responses: responses,
		modelInfo: &provider.ModelInfo{
			Provider: "gen-mock",
			ModelName: "gen-mock-model",
			SupportsStreaming: true,
		},
	}
}

func (m *genMockProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
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

func (m *genMockProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "gen-mock response"}},
	}, nil
}

func (m *genMockProvider) ModelInfo() *provider.ModelInfo {
	return m.modelInfo
}

// ─── 辅助函数 ──────────────────────────────────────────────────────

// setupGeneratorParams 创建基本的 TurnParams。
func setupGeneratorParams(responses [][]stream.StreamEvent, maxTurns int) (*TurnParams, *genMockProvider) {
	p := newGenMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: maxTurns,
		SessionID: "test-session",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		SteerCh: nil,
		Prompt: "test prompt",
	}

	return params, p
}

// collectGenEvents 从事件通道收集所有事件，直到通道关闭或超时。
func collectGenEvents(ch <-chan event.AgentEvent, timeout time.Duration) []event.AgentEvent {
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

// genEventTypes 提取事件类型列表。
func genEventTypes(events []event.AgentEvent) []event.EventType {
	types := make([]event.EventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// genHasEventType 检查事件列表中是否包含指定类型。
func genHasEventType(events []event.AgentEvent, t event.EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// genCountEventType 统计指定类型事件的出现次数。
func genCountEventType(events []event.AgentEvent, t event.EventType) int {
	count := 0
	for _, e := range events {
		if e.Type == t {
			count++
		}
	}
	return count
}

// runGeneratorTurn 运行一次 generator Turn 并收集事件。
func runGeneratorTurn(params *TurnParams) (*TurnResult, []event.AgentEvent) {
	g := NewDefaultLoopGenerator()
	eventCh := make(chan event.AgentEvent, EventChannelSize)

	var result *TurnResult
	done := make(chan struct{})
	go func() {
		result = g.RunTurn(stdcontext.Background(), params, eventCh)
		close(done)
	}()

	var events []event.AgentEvent
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
outer:
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				break outer
			}
			events = append(events, evt)
		case <-done:
			// 消费剩余事件
			for {
				select {
				case evt, ok := <-eventCh:
					if !ok {
						break outer
					}
					events = append(events, evt)
				default:
					break outer
				}
			}
		case <-timer.C:
			break outer
		}
	}

	return result, events
}

// runGeneratorTurnWithCtx 使用指定 context 运行一次 generator Turn 并收集事件。
func runGeneratorTurnWithCtx(ctx stdcontext.Context, params *TurnParams) (*TurnResult, []event.AgentEvent) {
	g := NewDefaultLoopGenerator()
	eventCh := make(chan event.AgentEvent, EventChannelSize)

	var result *TurnResult
	done := make(chan struct{})
	go func() {
		result = g.RunTurn(ctx, params, eventCh)
		close(done)
	}()

	var events []event.AgentEvent
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
outer:
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				break outer
			}
			events = append(events, evt)
		case <-done:
			for {
				select {
				case evt, ok := <-eventCh:
					if !ok {
						break outer
					}
					events = append(events, evt)
				default:
					break outer
				}
			}
		case <-timer.C:
			break outer
		}
	}

	return result, events
}

// ─── 测试用例 ──────────────────────────────────────────────────────

// TestLoopGenerator_Interface 验证 DefaultLoopGenerator 实现了 LoopGenerator 接口。
func TestLoopGenerator_Interface(t *testing.T) {
	var _ LoopGenerator = (*DefaultLoopGenerator)(nil)
	var _ LoopGenerator = NewDefaultLoopGenerator()
}

// TestLoopGenerator_SimpleText 测试简单文本查询：
// LLM 返回文本，无工具调用 → TurnResult.Status == Completed
func TestLoopGenerator_SimpleText(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamTextDelta, Content: " world"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
	if !genHasEventType(events, event.EventTextDelta) {
		t.Errorf("missing EventTextDelta, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd, got %v", genEventTypes(events))
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
}

// TestLoopGenerator_ToolCallAndResult 测试工具调用与结果：
// LLM 调用工具 → 工具返回结果 → LLM 完成回答
func TestLoopGenerator_ToolCallAndResult(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Let me check. "},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "get_weather",
				Arguments: map[string]any{"city": "Beijing"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Beijing is sunny today."},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
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

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventToolCallStart) {
		t.Errorf("missing EventToolCallStart, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventToolCallResult) {
		t.Errorf("missing EventToolCallResult, got %v", genEventTypes(events))
	}

	// 验证工具调用结果内容
	var toolResultContent string
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && !tr.IsError {
				toolResultContent = tr.Content
			}
		}
	}
	if toolResultContent != "Weather in Beijing: sunny, 25°C" {
		t.Errorf("tool result content = %q, want %q", toolResultContent, "Weather in Beijing: sunny, 25°C")
	}
}

// TestLoopGenerator_MultipleToolCalls 测试一轮中有多个工具调用。
func TestLoopGenerator_MultipleToolCalls(t *testing.T) {
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

	params, _ := setupGeneratorParams(responses, 0)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "tool_a", Description: "Tool A",
		Handler: func(_ stdcontext.Context, args map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: fmt.Sprintf("A result: %v", args["x"])}, nil
		},
	})
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "tool_b", Description: "Tool B",
		Handler: func(_ stdcontext.Context, args map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: fmt.Sprintf("B result: %v", args["y"])}, nil
		},
	})

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	toolCallStartCount := genCountEventType(events, event.EventToolCallStart)
	if toolCallStartCount != 2 {
		t.Errorf("EventToolCallStart count = %d, want 2", toolCallStartCount)
	}

	toolCallResultCount := genCountEventType(events, event.EventToolCallResult)
	if toolCallResultCount != 2 {
		t.Errorf("EventToolCallResult count = %d, want 2", toolCallResultCount)
	}
}

// TestLoopGenerator_MaxTurns 测试 MaxTurns 限制。
func TestLoopGenerator_MaxTurns(t *testing.T) {
	toolCallResponse := []stream.StreamEvent{
		{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
			ID: "tc-loop", Name: "loop_tool",
			Arguments: map[string]any{},
		}},
		{Type: stream.StreamDone},
	}

	maxTurns := 3
	responses := make([][]stream.StreamEvent, maxTurns+2)
	for i := range responses {
		responses[i] = toolCallResponse
	}

	params, _ := setupGeneratorParams(responses, maxTurns)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "loop_tool", Description: "A tool that loops",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "loop result"}, nil
		},
	})

	result, events := runGeneratorTurn(params)

	if !genHasEventType(events, event.EventMaxTurnsReached) {
		t.Errorf("missing EventMaxTurnsReached, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on MaxTurns path, got %v", genEventTypes(events))
	}
	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
}

// TestLoopGenerator_ContextCancellation 测试 context 取消。
func TestLoopGenerator_ContextCancellation(t *testing.T) {
	p := &genSlowProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-cancel",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
	}

	ctx, cancel := stdcontext.WithCancel(stdcontext.Background())

	// 短暂等待后取消 context
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, events := runGeneratorTurnWithCtx(ctx, params)

	if result.Status != event.StatusCanceled {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCanceled)
	}
	if !genHasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on cancel path, got %v", genEventTypes(events))
	}
}

// genSlowProvider 是一个慢速 mock provider，用于测试取消。
type genSlowProvider struct{}

func (m *genSlowProvider) StreamChat(ctx stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
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

func (m *genSlowProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "slow"}},
	}, nil
}

func (m *genSlowProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "gen-slow-mock", ModelName: "slow"}
}

// TestLoopGenerator_SteerMessage 测试 steer 消息注入。
func TestLoopGenerator_SteerMessage(t *testing.T) {
	steerCh := make(chan string, SteerChannelSize)

	// 创建一个 provider，在第一次调用时发送 steer 消息
	p := &genSteerProvider{
		steerCh: steerCh,
		responses: [][]stream.StreamEvent{
			{
				{Type: stream.StreamTextDelta, Content: "First part. "},
				{Type: stream.StreamDone},
			},
			{
				{Type: stream.StreamTextDelta, Content: "Final answer."},
				{Type: stream.StreamDone},
			},
		},
	}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-steer",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		SteerCh: steerCh,
		Prompt: "test",
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证 steer 消息被记录到上下文
	items, err := cm.GetMessages(stdcontext.Background(), nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	foundSteer := false
	for _, item := range items {
		if item.Metadata != nil {
			if typ, ok := item.Metadata["type"].(string); ok && typ == "steer" {
				foundSteer = true
			}
		}
	}
	if !foundSteer {
		t.Error("steer message not found in context")
	}
}

// genSteerProvider 在流中注入 steer 消息。
type genSteerProvider struct {
	mu sync.Mutex
	responses [][]stream.StreamEvent
	callCount int
	steerCh chan string
}

func (m *genSteerProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
		// 在第一次调用时注入 steer 消息
		if idx == 0 {
			m.steerCh <- "change direction"
		}
		var resp []stream.StreamEvent
		if idx < len(m.responses) {
			resp = m.responses[idx]
		} else {
			resp = []stream.StreamEvent{{Type: stream.StreamDone}}
		}
		for _, evt := range resp {
			ch <- evt
		}
	}()
	return ch, nil
}

func (m *genSteerProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock"}},
	}, nil
}

func (m *genSteerProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "gen-steer-mock", ModelName: "steer"}
}

// TestLoopGenerator_RetryOnHTTP429 测试 429 错误重试。
func TestLoopGenerator_RetryOnHTTP429(t *testing.T) {
	var callCount atomic.Int32
	p := &genRetryProvider{
		callCount: &callCount,
		responses: []error{
			&HTTPError{StatusCode: 429, Message: "rate limited"},
			nil,
		},
		streamResponse: []stream.StreamEvent{
			{Type: stream.StreamTextDelta, Content: "retry success"},
			{Type: stream.StreamDone},
		},
	}

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		RetryConfig: &RetryConfig{
			MaxRetries: 3,
			RetryOnHTTP: []int{429},
			BaseDelay: 10 * time.Millisecond,
			MaxDelay: 100 * time.Millisecond,
		},
		SessionID: "test-retry",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
	}

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
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

// genRetryProvider 支持重试测试的 mock provider。
type genRetryProvider struct {
	callCount *atomic.Int32
	responses []error
	streamResponse []stream.StreamEvent
}

func (m *genRetryProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	idx := int(m.callCount.Add(1)) - 1

	if idx < len(m.responses) && m.responses[idx] != nil {
		return nil, m.responses[idx]
	}

	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
		for _, evt := range m.streamResponse {
			ch <- evt
		}
	}()
	return ch, nil
}

func (m *genRetryProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock"}},
	}, nil
}

func (m *genRetryProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "gen-retry-mock", ModelName: "retry"}
}

// TestLoopGenerator_HookBefore 测试 before hook 拦截（修改调用）。
func TestLoopGenerator_HookBefore(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "original_tool",
				Arguments: map[string]any{"key": "value"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Done."},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)

	// 注册原始工具和重定向后的工具
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "original_tool", Description: "Original",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "original result"}, nil
		},
	})
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "redirected_tool", Description: "Redirected",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "redirected result"}, nil
		},
	})

	// Before hook 将工具名从 original_tool 改为 redirected_tool
	hp := toolhook.NewHookPipeline()
	hp.Register(&genBeforeModifierHook{}, 10)
	params.HookPipeline = hp

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证工具结果是 redirected_tool 的结果
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && !tr.IsError {
				if tr.Content != "redirected result" {
					t.Errorf("tool result = %q, want %q", tr.Content, "redirected result")
				}
			}
		}
	}
}

// genBeforeModifierHook 修改工具调用的 Before 钩子。
type genBeforeModifierHook struct{}

func (h *genBeforeModifierHook) Before(_ stdcontext.Context, call *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	call.Name = "redirected_tool"
	return &toolhook.BeforeResult{ModifiedCall: call}, nil
}

func (h *genBeforeModifierHook) After(_ stdcontext.Context, _ *toolhook.ToolCall, result *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	return &toolhook.AfterResult{}, nil
}

// TestLoopGenerator_HookBeforeBlock 测试 before hook 阻止执行。
func TestLoopGenerator_HookBeforeBlock(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "blocked_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Continuing after block."},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "blocked_tool", Description: "Blocked",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "should not be called"}, nil
		},
	})

	hp := toolhook.NewHookPipeline()
	hp.Register(&genBeforeBlockHook{}, 10)
	params.HookPipeline = hp

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证有 blocked 错误的 tool result
	found := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected tool result with IsError=true from block, got %v", genEventTypes(events))
	}
}

// genBeforeBlockHook 阻止工具执行的 Before 钩子。
type genBeforeBlockHook struct{}

func (h *genBeforeBlockHook) Before(_ stdcontext.Context, _ *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	return &toolhook.BeforeResult{Block: true, Reason: "tool is not allowed"}, nil
}

func (h *genBeforeBlockHook) After(_ stdcontext.Context, _ *toolhook.ToolCall, result *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	return &toolhook.AfterResult{}, nil
}

// TestLoopGenerator_HookBeforeTerminate 测试 before hook 终止循环。
func TestLoopGenerator_HookBeforeTerminate(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "terminating_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "terminating_tool", Description: "Terminating",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "executed"}, nil
		},
	})

	hp := toolhook.NewHookPipeline()
	hp.Register(&genBeforeTerminateHook{}, 10)
	params.HookPipeline = hp

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd, got %v", genEventTypes(events))
	}
}

// genBeforeTerminateHook 终止循环的 Before 钩子。
type genBeforeTerminateHook struct{}

func (h *genBeforeTerminateHook) Before(_ stdcontext.Context, _ *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	return &toolhook.BeforeResult{Terminate: true}, nil
}

func (h *genBeforeTerminateHook) After(_ stdcontext.Context, _ *toolhook.ToolCall, result *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	return &toolhook.AfterResult{}, nil
}

// TestLoopGenerator_HookAfter 测试 after hook 修改结果。
func TestLoopGenerator_HookAfter(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "test_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Done."},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool", Description: "Test",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "original result"}, nil
		},
	})

	hp := toolhook.NewHookPipeline()
	hp.Register(&genAfterModifierHook{}, 10)
	params.HookPipeline = hp

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证结果被修改
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && !tr.IsError {
				if tr.Content != "modified result" {
					t.Errorf("tool result = %q, want %q", tr.Content, "modified result")
				}
			}
		}
	}
}

// genAfterModifierHook 修改工具结果的 After 钩子。
type genAfterModifierHook struct{}

func (h *genAfterModifierHook) Before(_ stdcontext.Context, call *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	return &toolhook.BeforeResult{}, nil
}

func (h *genAfterModifierHook) After(_ stdcontext.Context, _ *toolhook.ToolCall, result *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	result.Content = "modified result"
	return &toolhook.AfterResult{ModifiedResult: result}, nil
}

// TestLoopGenerator_HookAfterTerminate 测试 after hook 终止循环。
func TestLoopGenerator_HookAfterTerminate(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "test_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool", Description: "Test",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "result"}, nil
		},
	})

	hp := toolhook.NewHookPipeline()
	hp.Register(&genAfterTerminateHook{}, 10)
	params.HookPipeline = hp

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd, got %v", genEventTypes(events))
	}
}

// genAfterTerminateHook 终止循环的 After 钩子。
type genAfterTerminateHook struct{}

func (h *genAfterTerminateHook) Before(_ stdcontext.Context, call *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	return &toolhook.BeforeResult{}, nil
}

func (h *genAfterTerminateHook) After(_ stdcontext.Context, _ *toolhook.ToolCall, result *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	return &toolhook.AfterResult{Terminate: true}, nil
}

// TestLoopGenerator_AutoCompact 测试自动压缩触发。
func TestLoopGenerator_AutoCompact(t *testing.T) {
	cm := ctxpkg.NewHeuristicContextManager(
		ctxpkg.WithCompactor(&genCompactCompactor{}),
		ctxpkg.WithMaxTokens(10),
	)

	responses := [][]stream.StreamEvent{
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
	}

	p := newGenMockProvider(responses)
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		CompactThreshold: 10,
		SessionID: "test-compact",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "a very long prompt that should exceed the token threshold",
	}

	_ = params.ToolRegistry.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool", Description: "A test tool",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "a very long tool result that should exceed the token threshold"}, nil
		},
	})

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventCompactStart) {
		t.Errorf("missing EventCompactStart, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventCompactEnd) {
		t.Errorf("missing EventCompactEnd, got %v", genEventTypes(events))
	}
}

// genCompactCompactor 是一个简单的 compactor，用于测试自动压缩。
type genCompactCompactor struct{}

func (c *genCompactCompactor) Compact(_ stdcontext.Context, items []ctxpkg.TurnItem, maxTokens int) (*ctxpkg.CompactResult, error) {
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

// TestLoopGenerator_MiddlewareBeforeTurn 测试 BeforeTurn 中间件。
func TestLoopGenerator_MiddlewareBeforeTurn(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)

	mw := &genBeforeTurnMiddleware{shouldFail: true}
	chain := middleware.NewChain()
	chain.Add(mw)
	params.MiddlewareChain = chain

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusError {
		t.Errorf("status = %v, want %v", result.Status, event.StatusError)
	}
	if result.Error == nil {
		t.Error("expected error from BeforeTurn middleware")
	}
	if !genHasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", genEventTypes(events))
	}
}

// genBeforeTurnMiddleware 是一个可控的 BeforeTurn 中间件。
type genBeforeTurnMiddleware struct {
	shouldFail bool
	called bool
}

func (m *genBeforeTurnMiddleware) BeforeTurn(_ stdcontext.Context, _ string) error {
	m.called = true
	if m.shouldFail {
		return errors.New("before turn middleware failed")
	}
	return nil
}

func (m *genBeforeTurnMiddleware) AfterTurn(_ stdcontext.Context, _ string) error {
	return nil
}

func (m *genBeforeTurnMiddleware) BeforeCompact(_ stdcontext.Context) error {
	return nil
}

func (m *genBeforeTurnMiddleware) AfterCompact(_ stdcontext.Context) error {
	return nil
}

// TestLoopGenerator_MiddlewareAfterTurn 测试 generator 不调用 AfterTurn 中间件。
// AfterTurn 由 agent（default.go）负责调用，generator 不应重复调用。
func TestLoopGenerator_MiddlewareAfterTurn(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)

	mw := &genAfterTurnMiddleware{shouldFail: true}
	chain := middleware.NewChain()
	chain.Add(mw)
	params.MiddlewareChain = chain

	result, _ := runGeneratorTurn(params)

	// generator 不调用 AfterTurn，即使中间件会失败也应返回 Completed
	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if result.Error != nil {
		t.Errorf("error = %v, want nil", result.Error)
	}
	if mw.called {
		t.Error("AfterTurn middleware should not be called by generator")
	}
}

// genAfterTurnMiddleware 是一个可控的 AfterTurn 中间件。
type genAfterTurnMiddleware struct {
	shouldFail bool
	called bool
}

func (m *genAfterTurnMiddleware) BeforeTurn(_ stdcontext.Context, _ string) error {
	return nil
}

func (m *genAfterTurnMiddleware) AfterTurn(_ stdcontext.Context, _ string) error {
	m.called = true
	if m.shouldFail {
		return errors.New("after turn middleware failed")
	}
	return nil
}

func (m *genAfterTurnMiddleware) BeforeCompact(_ stdcontext.Context) error {
	return nil
}

func (m *genAfterTurnMiddleware) AfterCompact(_ stdcontext.Context) error {
	return nil
}

// TestLoopGenerator_ConcurrentCalls 测试并发调用（无状态、并发安全）。
func TestLoopGenerator_ConcurrentCalls(t *testing.T) {
	_ = NewDefaultLoopGenerator() // 验证可构造

	var wg sync.WaitGroup
	results := make([]*TurnResult, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			responses := [][]stream.StreamEvent{
				{
					{Type: stream.StreamTextDelta, Content: fmt.Sprintf("response %d", idx)},
					{Type: stream.StreamDone},
				},
			}

			p := newGenMockProvider(responses)
			cm := ctxpkg.NewHeuristicContextManager()
			tr := registry.NewDefaultToolRegistry()

			params := &TurnParams{
				Provider: p,
				ContextManager: cm,
				ToolRegistry: tr,
				MaxTurns: DefaultMaxTurns,
				SessionID: fmt.Sprintf("concurrent-session-%d", idx),
				TurnID: fmt.Sprintf("concurrent-turn-%d", idx),
				SubmissionID: fmt.Sprintf("concurrent-sub-%d", idx),
				Prompt: fmt.Sprintf("query %d", idx),
			}

			// 使用 runGeneratorTurn 辅助函数，正确处理事件通道生命周期
			result, _ := runGeneratorTurn(params)
			results[idx] = result
		}(i)
	}

	wg.Wait()

	for i, r := range results {
		if r.Status != event.StatusCompleted {
			t.Errorf("concurrent call %d: status = %v, want %v", i, r.Status, event.StatusCompleted)
		}
	}
}

// TestLoopGenerator_ToolNotFound 测试工具未找到。
func TestLoopGenerator_ToolNotFound(t *testing.T) {
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

	params, _ := setupGeneratorParams(responses, 0)

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证有错误的 tool result
	found := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("expected tool result with error for nonexistent tool, got %v", genEventTypes(events))
	}
}

// TestLoopGenerator_StreamError 测试 LLM 返回流式错误。
func TestLoopGenerator_StreamError(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamError, Error: errors.New("LLM internal error")},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusError {
		t.Errorf("status = %v, want %v", result.Status, event.StatusError)
	}
	if !genHasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd on StreamError path, got %v", genEventTypes(events))
	}
}

// TestLoopGenerator_ThinkingDelta 测试思维增量事件。
func TestLoopGenerator_ThinkingDelta(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamThinkingDelta, Thinking: "Let me think..."},
			{Type: stream.StreamTextDelta, Content: "The answer is 42."},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventThinkingDelta) {
		t.Errorf("missing EventThinkingDelta, got %v", genEventTypes(events))
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
