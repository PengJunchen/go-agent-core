package loop

import (
	stdcontext "context"
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

// ─── SwapableProvider 测试 ────────────────────────────────────────

// TestSwapableProvider_StreamChat 验证 StreamChat 委托给内部 provider。
func TestSwapableProvider_StreamChat(t *testing.T) {
	inner := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "hello from inner"},
			{Type: stream.StreamDone},
		},
	})

	sp := NewSwapableProvider(inner)
	ch, err := sp.StreamChat(stdcontext.Background(), nil, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var content string
	for evt := range ch {
		if evt.Type == stream.StreamTextDelta {
			content += evt.Content
		}
	}
	if content != "hello from inner" {
		t.Errorf("content = %q, want %q", content, "hello from inner")
	}
}

// TestSwapableProvider_Generate 验证 Generate 委托给内部 provider。
func TestSwapableProvider_Generate(t *testing.T) {
	inner := newGenMockProvider(nil)

	sp := NewSwapableProvider(inner)
	resp, err := sp.Generate(stdcontext.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(resp.Content) == 0 {
		t.Error("expected non-empty response from Generate")
	}
}

// TestSwapableProvider_ModelInfo 验证 ModelInfo 委托给内部 provider。
func TestSwapableProvider_ModelInfo(t *testing.T) {
	inner := newGenMockProvider(nil)

	sp := NewSwapableProvider(inner)
	info := sp.ModelInfo()

	if info.Provider != "gen-mock" {
		t.Errorf("Provider = %q, want %q", info.Provider, "gen-mock")
	}
	if info.ModelName != "gen-mock-model" {
		t.Errorf("ModelName = %q, want %q", info.ModelName, "gen-mock-model")
	}
}

// TestSwapableProvider_Swap 验证原子替换生效。
func TestSwapableProvider_Swap(t *testing.T) {
	inner1 := newGenMockProvider(nil)
	inner1.modelInfo = &provider.ModelInfo{
		Provider: "provider-1",
		ModelName: "model-1",
	}

	sp := NewSwapableProvider(inner1)

	// 验证初始状态
	info := sp.ModelInfo()
	if info.Provider != "provider-1" {
		t.Errorf("before swap: Provider = %q, want %q", info.Provider, "provider-1")
	}

	// 执行 Swap
	inner2 := newGenMockProvider(nil)
	inner2.modelInfo = &provider.ModelInfo{
		Provider: "provider-2",
		ModelName: "model-2",
	}
	sp.Swap(inner2)

	// 验证替换后状态
	info = sp.ModelInfo()
	if info.Provider != "provider-2" {
		t.Errorf("after swap: Provider = %q, want %q", info.Provider, "provider-2")
	}

	// 验证 Current 返回新 provider
	current := sp.Current()
	if current.ModelInfo().Provider != "provider-2" {
		t.Errorf("Current() Provider = %q, want %q", current.ModelInfo().Provider, "provider-2")
	}
}

// TestSwapableProvider_ConcurrentAccess 验证并发 Swap + StreamChat 安全。
func TestSwapableProvider_ConcurrentAccess(t *testing.T) {
	inner := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "response"},
			{Type: stream.StreamDone},
		},
	})

	sp := NewSwapableProvider(inner)

	var wg sync.WaitGroup
	var errors atomic.Int32

	// 并发 StreamChat 读取
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := sp.StreamChat(stdcontext.Background(), nil, nil)
			if err != nil {
				errors.Add(1)
			}
		}()
	}

	// 并发 Swap 写入
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := newGenMockProvider(nil)
			p.modelInfo = &provider.ModelInfo{
				Provider: fmt.Sprintf("provider-%d", idx),
				ModelName: fmt.Sprintf("model-%d", idx),
			}
			sp.Swap(p)
		}(i)
	}

	wg.Wait()

	if errors.Load() > 0 {
		t.Errorf("got %d errors during concurrent access", errors.Load())
	}
}

// ─── PrepareNextTurn 回调测试 ─────────────────────────────────────

// TestPrepareNextTurn_SwapProvider 验证回调在 Turn 间切换 Provider。
func TestPrepareNextTurn_SwapProvider(t *testing.T) {
	provider1 := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "from-provider-1"},
			{Type: stream.StreamDone},
		},
	})
	provider1.modelInfo = &provider.ModelInfo{Provider: "p1", ModelName: "m1"}

	provider2 := newGenMockProvider(nil)
	provider2.modelInfo = &provider.ModelInfo{Provider: "p2", ModelName: "m2"}

	// 回调：在 turnCount > 0 时切换到 provider2
	swapFn := PrepareNextTurnFunc(func(_ stdcontext.Context, current provider.ModelProvider, turnCount int) provider.ModelProvider {
		if turnCount > 0 {
			return provider2
		}
		return nil
	})

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: provider1,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-swap",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		PrepareNextTurn: swapFn,
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
}

// TestPrepareNextTurn_KeepProvider 验证回调返回 nil 时保持当前 Provider。
func TestPrepareNextTurn_KeepProvider(t *testing.T) {
	provider1 := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "same provider"},
			{Type: stream.StreamDone},
		},
	})

	// 回调始终返回 nil，表示保持当前 provider
	keepFn := PrepareNextTurnFunc(func(_ stdcontext.Context, _ provider.ModelProvider, _ int) provider.ModelProvider {
		return nil
	})

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: provider1,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-keep",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		PrepareNextTurn: keepFn,
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
}

// TestPrepareNextTurn_IntegrationWithGenerator 验证完整的 prepareNextTurn + Generator 集成。
func TestPrepareNextTurn_IntegrationWithGenerator(t *testing.T) {
	// provider1 第一轮返回工具调用
	provider1 := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "test_tool",
				Arguments: map[string]any{"x": 1},
			}},
			{Type: stream.StreamDone},
		},
	})
	provider1.modelInfo = &provider.ModelInfo{Provider: "p1", ModelName: "m1"}

	// provider2 第二轮返回文本
	provider2 := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "from-provider-2"},
			{Type: stream.StreamDone},
		},
	})
	provider2.modelInfo = &provider.ModelInfo{Provider: "p2", ModelName: "m2"}

	var calledWith []int
	swapFn := PrepareNextTurnFunc(func(_ stdcontext.Context, _ provider.ModelProvider, turnCount int) provider.ModelProvider {
		calledWith = append(calledWith, turnCount)
		if turnCount >= 1 {
			return provider2
		}
		return nil
	})

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()
	_ = tr.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool",
		Description: "A test tool",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "tool result"}, nil
		},
	})

	params := &TurnParams{
		Provider: provider1,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-integration",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		PrepareNextTurn: swapFn,
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证回调被调用了至少 2 次（turn 0 和 turn 1）
	if len(calledWith) < 2 {
		t.Errorf("PrepareNextTurn called %d times, want at least 2", len(calledWith))
	}
}

// TestPrepareNextTurn_SwapAtTurnBoundary 验证 Provider 切换发生在 Turn 边界。
func TestPrepareNextTurn_SwapAtTurnBoundary(t *testing.T) {
	var lastProviderName string
	var mu sync.Mutex

	// 创建记录 ModelInfo 的 mock provider
	makeTrackingProvider := func(name string, responses [][]stream.StreamEvent) *genMockProvider {
		p := newGenMockProvider(responses)
		p.modelInfo = &provider.ModelInfo{Provider: name, ModelName: name + "-model"}
		return p
	}

	provider1 := makeTrackingProvider("p1", [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "test_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
	})

	provider2 := makeTrackingProvider("p2", [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "from-p2"},
			{Type: stream.StreamDone},
		},
	})

	swapFn := PrepareNextTurnFunc(func(_ stdcontext.Context, current provider.ModelProvider, turnCount int) provider.ModelProvider {
		mu.Lock()
		lastProviderName = current.ModelInfo().Provider
		mu.Unlock()

		if turnCount >= 1 {
			return provider2
		}
		return nil
	})

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()
	_ = tr.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool",
		Description: "A test tool",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "tool result"}, nil
		},
	})

	params := &TurnParams{
		Provider: provider1,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-boundary",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		PrepareNextTurn: swapFn,
	}

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证回调在 turn 0 时看到的是 p1
	mu.Lock()
	if lastProviderName != "p1" {
		t.Errorf("last provider seen by callback = %q, want %q", lastProviderName, "p1")
	}
	mu.Unlock()

	// 验证最终有文本内容 "from-p2"
	var textContent string
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			if s, ok := e.Payload.(string); ok {
				textContent += s
			}
		}
	}
	if textContent != "from-p2" {
		t.Errorf("text content = %q, want %q", textContent, "from-p2")
	}
}

// swapTestProvider 是用于 SwapProvider 测试的 mock provider。
type swapTestProvider struct {
	name string
}

func (p *swapTestProvider) StreamChat(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 1)
	go func() {
		defer close(ch)
		ch <- stream.StreamEvent{Type: stream.StreamDone}
	}()
	return ch, nil
}

func (p *swapTestProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: p.name}},
	}, nil
}

func (p *swapTestProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: p.name, ModelName: p.name + "-model"}
}

// TestSwapableProvider_ImplementsModelProvider 验证 SwapableProvider 实现 ModelProvider 接口。
func TestSwapableProvider_ImplementsModelProvider(t *testing.T) {
	var _ provider.ModelProvider = (*SwapableProvider)(nil)
}

// TestPrepareNextTurn_WithSwapableProvider 验证 SwapableProvider 与 PrepareNextTurn 配合使用。
func TestPrepareNextTurn_WithSwapableProvider(t *testing.T) {
	inner1 := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "test_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
	})
	inner1.modelInfo = &provider.ModelInfo{Provider: "inner1", ModelName: "inner1-model"}

	inner2 := newGenMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "from-inner2"},
			{Type: stream.StreamDone},
		},
	})
	inner2.modelInfo = &provider.ModelInfo{Provider: "inner2", ModelName: "inner2-model"}

	sp := NewSwapableProvider(inner1)

	swapFn := PrepareNextTurnFunc(func(_ stdcontext.Context, current provider.ModelProvider, turnCount int) provider.ModelProvider {
		if turnCount >= 1 {
			sp.Swap(inner2)
		}
		return nil // 返回 nil，因为 SwapableProvider 内部已经被替换
	})

	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()
	_ = tr.RegisterTool(stdcontext.Background(), registry.ToolDefinition{
		Name: "test_tool",
		Description: "A test tool",
		Handler: func(_ stdcontext.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "tool result"}, nil
		},
	})

	params := &TurnParams{
		Provider: sp,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-swapable",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		PrepareNextTurn: swapFn,
	}

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 验证最终有来自 inner2 的文本内容
	var textContent string
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			if s, ok := e.Payload.(string); ok {
				textContent += s
			}
		}
	}
	if textContent != "from-inner2" {
		t.Errorf("text content = %q, want %q", textContent, "from-inner2")
	}
}

// TestPrepareNextTurn_NilCallback 验证 PrepareNextTurn 为 nil 时正常工作。
func TestPrepareNextTurn_NilCallback(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "hello"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	params.PrepareNextTurn = nil // 明确设为 nil

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
}

// TestPrepareNextTurn_WithDefaultLoopAgent 验证通过 Builder 设置 PrepareNextTurn。
func TestPrepareNextTurn_WithDefaultLoopAgent(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "hello"},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	var callbackCalled bool
	swapFn := PrepareNextTurnFunc(func(_ stdcontext.Context, _ provider.ModelProvider, _ int) provider.ModelProvider {
		callbackCalled = true
		return nil
	})

	agent, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		WithPrepareNextTurn(swapFn).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	ch, err := agent.Query(stdcontext.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	if !callbackCalled {
		t.Error("PrepareNextTurn callback was not called")
	}
}
