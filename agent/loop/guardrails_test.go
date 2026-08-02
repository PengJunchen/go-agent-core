// Package loop — 防护栏测试（StopLength 截断保护 + forceTextReply 回退）。
//
// 本文件覆盖以下验收标准：
// - AC-1: StopLength 截断保护 — finish_reason=length 时跳过工具执行
// - AC-2: 循环检测触发 forceTextReply 生成文本摘要
// - AC-3: forceTextReply 使用 ToolChoiceNone
// - AC-4: forceTextReply 使用 context.WithoutCancel 不被父上下文取消
package loop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/production"
)

// ─── AC-1: StopLength 截断保护 ──────────────────────────────────────

// TestStopLength_TruncatedToolCallsSkipped 验证：
// 当 LLM 响应因 token 上限被截断（FinishReason="length"）时，
// 工具调用不被执行，而是记录错误结果，让 LLM 在下一轮重试。
func TestStopLength_TruncatedToolCallsSkipped(t *testing.T) {
	toolHandlerCalled := false

	responses := [][]stream.StreamEvent{
		// 第一轮：返回工具调用，但 FinishReason="length"（被截断）
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-trunc",
				Name: "trunc_tool",
				Arguments: map[string]any{"incomplete": true},
			}},
			{Type: stream.StreamDone, FinishReason: stream.FinishReasonLength},
		},
		// 第二轮：LLM 重试，给出文本回复
		{
			{Type: stream.StreamTextDelta, Content: "Retried after truncation."},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "trunc_tool",
		Description: "A tool that should not be executed when truncated",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			toolHandlerCalled = true
			return &registry.ToolResult{Content: "should not reach"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "test truncation",
		SessionID: "test-stoplength",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证工具 handler 未被调用
	if toolHandlerCalled {
		t.Error("tool handler should NOT be called when response is truncated")
	}

	// 验证 EventToolCallResult 包含截断错误信息
	truncResultFound := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				if strings.Contains(tr.Content, "truncated due to length limit") {
					truncResultFound = true
				}
			}
		}
	}
	if !truncResultFound {
		t.Errorf("expected EventToolCallResult with truncation error, got %v", eventTypes(events))
	}

	// 验证 Agent 正常完成（LLM 重试后给出文本回复）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}

	// 验证 LLM 被调用了 2 次（第一次截断，第二次重试）
	if p.callCount != 2 {
		t.Errorf("StreamChat call count = %d, want 2", p.callCount)
	}
}

// ─── AC-2: 循环检测触发 forceTextReply ──────────────────────────────

// TestForceTextReply_LoopDetectionTriggersTextReply 验证：
// 循环检测触发后，forceTextReply 被调用并生成文本摘要，
// Agent 以 StatusCompleted 结束而非 StatusError。
func TestForceTextReply_LoopDetectionTriggersTextReply(t *testing.T) {
	ld := production.NewDefaultLoopDetector(production.LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})

	pb := production.NewProductionBundle(production.WithLoopDetector(ld))

	toolArgs := map[string]any{"action": "loop"}
	responses := [][]stream.StreamEvent{
		sameToolCallResponse("tc-1", "loop_tool", toolArgs),
		sameToolCallResponse("tc-2", "loop_tool", toolArgs),
		sameToolCallResponse("tc-3", "loop_tool", toolArgs),
		// forceTextReply 使用此响应
		{
			{Type: stream.StreamTextDelta, Content: "I detected a loop and am providing a summary."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "loop_tool",
		Description: "A tool that loops",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "loop result"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "trigger loop",
		SessionID: "test-force-text-reply",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证 EventToolLoopDetected 被发射
	if !hasEventType(events, event.EventToolLoopDetected) {
		t.Errorf("missing EventToolLoopDetected, got %v", eventTypes(events))
	}

	// 验证 forceTextReply 生成了文本
	var forceTextContent string
	loopDetectedIdx := -1
	for i, e := range events {
		if e.Type == event.EventToolLoopDetected {
			loopDetectedIdx = i
		}
		// 收集循环检测之后的文本增量
		if loopDetectedIdx >= 0 && e.Type == event.EventTextDelta {
			if text, ok := e.Payload.(string); ok {
				forceTextContent += text
			}
		}
	}
	if forceTextContent == "" {
		t.Errorf("expected text from forceTextReply after loop detection, got empty string")
	}
	if !strings.Contains(forceTextContent, "loop") {
		t.Errorf("forceTextReply text = %q, want contains 'loop'", forceTextContent)
	}

	// 验证 Agent 以 StatusCompleted 结束
	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}

	// 验证 EventCompleted 被发送
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
}

// ─── AC-3: forceTextReply 使用 ToolChoiceNone ───────────────────────

// optionCapturingProvider 是一个记录 ChatOptions 的 mock provider。
type optionCapturingProvider struct {
	mu sync.Mutex
	responses [][]stream.StreamEvent
	callCount int
	capturedOpts []*provider.ChatOptions
}

func newOptionCapturingProvider(responses [][]stream.StreamEvent) *optionCapturingProvider {
	return &optionCapturingProvider{responses: responses}
}

func (m *optionCapturingProvider) StreamChat(_ context.Context, _ []message.Message, opts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callCount
	m.callCount++
	if opts != nil {
		optsCopy := *opts
		m.capturedOpts = append(m.capturedOpts, &optsCopy)
	} else {
		m.capturedOpts = append(m.capturedOpts, nil)
	}
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

func (m *optionCapturingProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock"}},
	}, nil
}

func (m *optionCapturingProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "option-capture-mock", ModelName: "capture"}
}

func (m *optionCapturingProvider) getCapturedOpts() []*provider.ChatOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*provider.ChatOptions, len(m.capturedOpts))
	copy(result, m.capturedOpts)
	return result
}

// TestForceTextReply_UsesToolChoiceNone 验证：
// forceTextReply 调用 LLM 时使用 ToolChoiceNone。
func TestForceTextReply_UsesToolChoiceNone(t *testing.T) {
	ld := production.NewDefaultLoopDetector(production.LoopDetectorConfig{
		ConsecutiveThreshold: 2,
		WindowSize: 10,
		ArgumentComparison: true,
	})

	pb := production.NewProductionBundle(production.WithLoopDetector(ld))

	toolArgs := map[string]any{"x": 1}
	responses := [][]stream.StreamEvent{
		sameToolCallResponse("tc-1", "choice_tool", toolArgs),
		sameToolCallResponse("tc-2", "choice_tool", toolArgs),
		// forceTextReply 使用此响应
		{
			{Type: stream.StreamTextDelta, Content: "Summary text."},
			{Type: stream.StreamDone},
		},
	}

	p := newOptionCapturingProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		ProductionBundle: pb,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "choice_tool",
		Description: "A tool for choice testing",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "result"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "test tool choice",
		SessionID: "test-toolchoice",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证循环被检测到
	if !hasEventType(events, event.EventToolLoopDetected) {
		t.Fatalf("missing EventToolLoopDetected, got %v", eventTypes(events))
	}

	capturedOpts := p.getCapturedOpts()

	// forceTextReply 是第 3 次调用（index=2）
	if len(capturedOpts) < 3 {
		t.Fatalf("expected at least 3 StreamChat calls, got %d", len(capturedOpts))
	}

	forceReplyOpts := capturedOpts[2]
	if forceReplyOpts == nil {
		t.Fatal("forceTextReply ChatOptions is nil")
	}
	if forceReplyOpts.ToolChoice == nil {
		t.Fatal("forceTextReply ToolChoice is nil")
	}
	if forceReplyOpts.ToolChoice.Mode != provider.ToolChoiceNone {
		t.Errorf("forceTextReply ToolChoice.Mode = %v, want %v (ToolChoiceNone)",
			forceReplyOpts.ToolChoice.Mode, provider.ToolChoiceNone)
	}
}

// ─── AC-4: forceTextReply 不被父上下文取消 ──────────────────────────

// cancelAwareProvider 是一个在 forceTextReply 调用时检查上下文是否被取消的 mock provider。
type cancelAwareProvider struct {
	mu sync.Mutex
	responses [][]stream.StreamEvent
	callCount int
	forceReplyCtxErr error // 记录 forceTextReply 调用时的 ctx.Err()
}

func newCancelAwareProvider(responses [][]stream.StreamEvent) *cancelAwareProvider {
	return &cancelAwareProvider{responses: responses}
}

func (m *cancelAwareProvider) StreamChat(ctx context.Context, _ []message.Message, opts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

	// 第 3 次调用（index=2）是 forceTextReply
	if idx == 2 {
		m.mu.Lock()
		m.forceReplyCtxErr = ctx.Err()
		m.mu.Unlock()
	}

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

func (m *cancelAwareProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "mock"}},
	}, nil
}

func (m *cancelAwareProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "cancel-aware-mock", ModelName: "cancel-aware"}
}

func (m *cancelAwareProvider) getForceReplyCtxErr() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.forceReplyCtxErr
}

// cancelProofContextManager 包装 HeuristicContextManager，
// 在 RecordItem 中忽略上下文取消，确保循环检测路径不被中断。
type cancelProofContextManager struct {
	inner *ctxpkg.HeuristicContextManager
}

func (m *cancelProofContextManager) RecordItem(ctx context.Context, item ctxpkg.TurnItem) error {
	// 使用未取消的上下文执行 RecordItem
	return m.inner.RecordItem(context.Background(), item)
}

func (m *cancelProofContextManager) GetMessages(ctx context.Context, opts *ctxpkg.ContextOptions) ([]ctxpkg.TurnItem, error) {
	return m.inner.GetMessages(ctx, opts)
}

func (m *cancelProofContextManager) TokenUsage(ctx context.Context) int {
	return m.inner.TokenUsage(ctx)
}

func (m *cancelProofContextManager) Compact(ctx context.Context, strategy ctxpkg.CompactStrategy) (*ctxpkg.CompactResult, error) {
	return m.inner.Compact(ctx, strategy)
}

func (m *cancelProofContextManager) SetInitialContext(ctx context.Context, items []ctxpkg.TurnItem) error {
	return m.inner.SetInitialContext(ctx, items)
}

// TestForceTextReply_NotCanceledByParentContext 验证：
// forceTextReply 使用 context.WithoutCancel 派生新上下文，
// 即使父上下文被取消，forceTextReply 仍能完成 LLM 调用。
func TestForceTextReply_NotCanceledByParentContext(t *testing.T) {
	ld := production.NewDefaultLoopDetector(production.LoopDetectorConfig{
		ConsecutiveThreshold: 2,
		WindowSize: 10,
		ArgumentComparison: true,
	})

	pb := production.NewProductionBundle(production.WithLoopDetector(ld))

	toolArgs := map[string]any{"x": 1}
	responses := [][]stream.StreamEvent{
		sameToolCallResponse("tc-1", "cancel_tool", toolArgs),
		sameToolCallResponse("tc-2", "cancel_tool", toolArgs),
		// forceTextReply 使用此响应
		{
			{Type: stream.StreamTextDelta, Content: "Completed despite parent cancellation."},
			{Type: stream.StreamDone},
		},
	}

	p := newCancelAwareProvider(responses)
	cm := &cancelProofContextManager{inner: ctxpkg.NewHeuristicContextManager()}
	tr := registry.NewDefaultToolRegistry()

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		ProductionBundle: pb,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	// 使用可取消的上下文
	testCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 注册工具，在第 2 次调用时取消父上下文
	var toolCallCount int32
	var cancelOnce sync.Once
	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "cancel_tool",
		Description: "A tool that cancels the parent context",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			toolCallCount++
			// 第 2 次工具调用时取消父上下文（在循环检测之前）
			if toolCallCount == 2 {
				cancelOnce.Do(func() {
					cancel()
				})
			}
			return &registry.ToolResult{Content: "result"}, nil
		},
	})

	ch, err := agent.Query(testCtx, AgentInput{
		Prompt: "test cancel resistance",
		SessionID: "test-not-canceled",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证循环被检测到
	if !hasEventType(events, event.EventToolLoopDetected) {
		t.Errorf("missing EventToolLoopDetected, got %v", eventTypes(events))
	}

	// 验证 forceTextReply 时的上下文未被取消
	forceReplyCtxErr := p.getForceReplyCtxErr()
	if forceReplyCtxErr != nil {
		t.Errorf("forceTextReply context should NOT be canceled (context.WithoutCancel), but got err: %v", forceReplyCtxErr)
	}

	// 验证 forceTextReply 生成了文本
	hasText := false
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			hasText = true
			break
		}
	}
	if !hasText {
		t.Errorf("expected text from forceTextReply, got %v", eventTypes(events))
	}

	// 验证 Agent 完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
}
