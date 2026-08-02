// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// callbacks_test.go 测试可配置回调和消息级事件：
// - AC-1: EventMessageStart/Update/End 事件类型存在且能被创建
// - AC-2: ConvertToLlm 回调替换默认消息转换
// - AC-3: TransformContext 回调重写 LLM 输入消息
// - AC-4: nil 回调保持现有行为（向后兼容）
// - AC-5: go test -race 通过
package loop

import (
	stdcontext "context"
	"errors"
	"sync"
	"testing"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── Callback 专用 Mock ─────────────────────────────────────────

// genCaptureProvider 捕获发送给 LLM 的消息，用于验证回调效果。
type genCaptureProvider struct {
	mu sync.Mutex
	captured []message.Message
	responses [][]stream.StreamEvent
	callCount int
	modelInfo *provider.ModelInfo
}

func newGenCaptureProvider(responses [][]stream.StreamEvent) *genCaptureProvider {
	return &genCaptureProvider{
		responses: responses,
		modelInfo: &provider.ModelInfo{
			Provider: "gen-capture",
			ModelName: "gen-capture-model",
			SupportsStreaming: true,
		},
	}
}

func (m *genCaptureProvider) StreamChat(_ stdcontext.Context, msgs []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	m.captured = make([]message.Message, len(msgs))
	copy(m.captured, msgs)
	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
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

func (m *genCaptureProvider) Generate(_ stdcontext.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "gen-capture response"}},
	}, nil
}

func (m *genCaptureProvider) ModelInfo() *provider.ModelInfo {
	return m.modelInfo
}

func (m *genCaptureProvider) getCaptured() []message.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.captured
}

// ─── AC-1: Message-level Events ─────────────────────────────────

// TestCallbacks_MessageLevelEventsExist 验证 EventMessageStart/Update/End 常量存在且能创建事件。
func TestCallbacks_MessageLevelEventsExist(t *testing.T) {
	tests := []struct {
		name string
		etype event.EventType
	}{
		{"EventMessageStart", event.EventMessageStart},
		{"EventMessageUpdate", event.EventMessageUpdate},
		{"EventMessageEnd", event.EventMessageEnd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := event.AgentEvent{Type: tt.etype}
			if evt.Type != tt.etype {
				t.Errorf("expected %v, got %v", tt.etype, evt.Type)
			}
		})
	}
}

// TestCallbacks_MessageStartEndEmitted 验证 generator 在 LLM 流式响应时
// 发射 EventMessageStart（流开始前）和 EventMessageEnd（流结束后、工具调用前）。
func TestCallbacks_MessageStartEndEmitted(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventMessageStart) {
		t.Errorf("missing EventMessageStart, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventMessageEnd) {
		t.Errorf("missing EventMessageEnd, got %v", genEventTypes(events))
	}

	// 验证事件顺序：MessageStart → TextDelta → MessageEnd
	startIdx := -1
	endIdx := -1
	deltaIdx := -1
	for i, e := range events {
		switch e.Type {
		case event.EventMessageStart:
			startIdx = i
		case event.EventTextDelta:
			if deltaIdx == -1 {
				deltaIdx = i
			}
		case event.EventMessageEnd:
			endIdx = i
		}
	}
	if startIdx == -1 || deltaIdx == -1 || endIdx == -1 {
		t.Fatalf("expected MessageStart(%d), TextDelta(%d), MessageEnd(%d)", startIdx, deltaIdx, endIdx)
	}
	if !(startIdx < deltaIdx && deltaIdx < endIdx) {
		t.Errorf("event order wrong: start=%d, delta=%d, end=%d", startIdx, deltaIdx, endIdx)
	}
}

// TestCallbacks_MessageEndBeforeToolCalls 验证 EventMessageEnd 在工具执行结果之前发射。
// 注意：EventToolCallStart 是 LLM 流式响应的一部分，在 EventMessageEnd 之前发射；
// EventToolCallResult 是工具执行的结果，在 EventMessageEnd 之后发射。
func TestCallbacks_MessageEndBeforeToolCalls(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Let me check. "},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1", Name: "test_tool",
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
			return &registry.ToolResult{Content: "result"}, nil
		},
	})

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	// 找到第一个 MessageEnd 和第一个 ToolCallResult（工具执行结果）的位置
	msgEndIdx := -1
	toolResultIdx := -1
	for i, e := range events {
		if e.Type == event.EventMessageEnd && msgEndIdx == -1 {
			msgEndIdx = i
		}
		if e.Type == event.EventToolCallResult && toolResultIdx == -1 {
			toolResultIdx = i
		}
	}
	if msgEndIdx == -1 {
		t.Fatal("missing EventMessageEnd")
	}
	if toolResultIdx == -1 {
		t.Fatal("missing EventToolCallResult")
	}
	if msgEndIdx >= toolResultIdx {
		t.Errorf("EventMessageEnd(%d) should come before EventToolCallResult(%d)", msgEndIdx, toolResultIdx)
	}
}

// ─── AC-2: ConvertToLlm Callback ────────────────────────────────

// TestCallbacks_ConvertToLlm 验证 ConvertToLlm 回调替换默认消息转换。
func TestCallbacks_ConvertToLlm(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "response"},
			{Type: stream.StreamDone},
		},
	}

	p := newGenCaptureProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	convertCalled := false
	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-convert",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test prompt",
		ConvertToLlm: func(_ []ctxpkg.TurnItem) ([]message.Message, error) {
			convertCalled = true
			// 返回自定义消息，带有特殊标记
			return []message.Message{
				message.NewTextMessage(message.RoleSystem, "CUSTOM_CONVERT_MARKER"),
			}, nil
		},
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !convertCalled {
		t.Error("ConvertToLlm callback was not called")
	}

	captured := p.getCaptured()
	if len(captured) == 0 {
		t.Fatal("no messages captured by provider")
	}
	if captured[0].Role != message.RoleSystem {
		t.Errorf("first message role = %v, want %v", captured[0].Role, message.RoleSystem)
	}
	if len(captured[0].Content) == 0 || captured[0].Content[0].Text != "CUSTOM_CONVERT_MARKER" {
		t.Errorf("first message text = %q, want %q", captured[0].Content[0].Text, "CUSTOM_CONVERT_MARKER")
	}
}

// TestCallbacks_ConvertToLlmError 验证 ConvertToLlm 回调返回错误时的处理。
func TestCallbacks_ConvertToLlmError(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{{Type: stream.StreamDone}},
	}

	p := newGenCaptureProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	convertErr := errors.New("convert error")
	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-convert-err",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		ConvertToLlm: func(_ []ctxpkg.TurnItem) ([]message.Message, error) {
			return nil, convertErr
		},
	}

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusError {
		t.Errorf("status = %v, want %v", result.Status, event.StatusError)
	}
	if !errors.Is(result.Error, convertErr) {
		t.Errorf("error = %v, want %v", result.Error, convertErr)
	}
	if !genHasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", genEventTypes(events))
	}
}

// ─── AC-3: TransformContext Callback ─────────────────────────────

// TestCallbacks_TransformContext 验证 TransformContext 回调重写 LLM 输入消息。
func TestCallbacks_TransformContext(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "response"},
			{Type: stream.StreamDone},
		},
	}

	p := newGenCaptureProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	transformCalled := false
	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-transform",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test prompt",
		TransformContext: func(_ stdcontext.Context, msgs []message.Message) ([]message.Message, error) {
			transformCalled = true
			// 在消息末尾追加一条自定义系统消息
			msgs = append(msgs, message.NewTextMessage(message.RoleSystem, "TRANSFORM_MARKER"))
			return msgs, nil
		},
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !transformCalled {
		t.Error("TransformContext callback was not called")
	}

	captured := p.getCaptured()
	if len(captured) == 0 {
		t.Fatal("no messages captured by provider")
	}
	last := captured[len(captured)-1]
	if last.Role != message.RoleSystem {
		t.Errorf("last message role = %v, want %v", last.Role, message.RoleSystem)
	}
	if len(last.Content) == 0 || last.Content[0].Text != "TRANSFORM_MARKER" {
		t.Errorf("last message text = %q, want %q", last.Content[0].Text, "TRANSFORM_MARKER")
	}
}

// TestCallbacks_TransformContextError 验证 TransformContext 回调返回错误时的处理。
func TestCallbacks_TransformContextError(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{{Type: stream.StreamDone}},
	}

	p := newGenCaptureProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	transformErr := errors.New("transform error")
	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-transform-err",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		TransformContext: func(_ stdcontext.Context, _ []message.Message) ([]message.Message, error) {
			return nil, transformErr
		},
	}

	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusError {
		t.Errorf("status = %v, want %v", result.Status, event.StatusError)
	}
	if !errors.Is(result.Error, transformErr) {
		t.Errorf("error = %v, want %v", result.Error, transformErr)
	}
	if !genHasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", genEventTypes(events))
	}
}

// TestCallbacks_BothCallbacksTogether 验证两个回调同时使用时工作正常。
func TestCallbacks_BothCallbacksTogether(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "response"},
			{Type: stream.StreamDone},
		},
	}

	p := newGenCaptureProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	convertCalled := false
	transformCalled := false
	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-both",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "test",
		ConvertToLlm: func(_ []ctxpkg.TurnItem) ([]message.Message, error) {
			convertCalled = true
			return []message.Message{
				message.NewTextMessage(message.RoleUser, "converted message"),
			}, nil
		},
		TransformContext: func(_ stdcontext.Context, msgs []message.Message) ([]message.Message, error) {
			transformCalled = true
			msgs = append(msgs, message.NewTextMessage(message.RoleSystem, "TRANSFORM_MARKER"))
			return msgs, nil
		},
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !convertCalled {
		t.Error("ConvertToLlm callback was not called")
	}
	if !transformCalled {
		t.Error("TransformContext callback was not called")
	}

	captured := p.getCaptured()
	if len(captured) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(captured))
	}
	// 第一条来自 ConvertToLlm
	if captured[0].Role != message.RoleUser || len(captured[0].Content) == 0 || captured[0].Content[0].Text != "converted message" {
		t.Errorf("first message should be from ConvertToLlm, got role=%v text=%v", captured[0].Role, captured[0].Content)
	}
	// 最后一条来自 TransformContext
	last := captured[len(captured)-1]
	if last.Role != message.RoleSystem || len(last.Content) == 0 || last.Content[0].Text != "TRANSFORM_MARKER" {
		t.Errorf("last message should be from TransformContext, got role=%v text=%v", last.Role, last.Content)
	}
}

// ─── AC-4: Nil Callbacks Backward Compatible ─────────────────────

// TestCallbacks_NilCallbacksBackwardCompatible 验证 nil 回调保持现有行为（向后兼容）。
func TestCallbacks_NilCallbacksBackwardCompatible(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	// ConvertToLlm 和 TransformContext 均为 nil（默认行为）
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
	if !genHasEventType(events, event.EventMessageStart) {
		t.Errorf("missing EventMessageStart, got %v", genEventTypes(events))
	}
	if !genHasEventType(events, event.EventMessageEnd) {
		t.Errorf("missing EventMessageEnd, got %v", genEventTypes(events))
	}

	// 验证文本内容
	var textContent string
	for _, e := range events {
		if e.Type == event.EventTextDelta {
			textContent += e.Payload.(string)
		}
	}
	if textContent != "Hello" {
		t.Errorf("text content = %q, want %q", textContent, "Hello")
	}
}

// TestCallbacks_NilCallbacksUseDefaultConversion 验证 nil ConvertToLlm 使用默认 turnItemsToMessages。
func TestCallbacks_NilCallbacksUseDefaultConversion(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "response"},
			{Type: stream.StreamDone},
		},
	}

	p := newGenCaptureProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	params := &TurnParams{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		SessionID: "test-default-convert",
		TurnID: generateID("turn"),
		SubmissionID: generateID("sub"),
		Prompt: "hello world",
		// ConvertToLlm is nil → should use default turnItemsToMessages
		// TransformContext is nil → should not transform
	}

	result, _ := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Errorf("status = %v, want %v", result.Status, event.StatusCompleted)
	}

	captured := p.getCaptured()
	if len(captured) == 0 {
		t.Fatal("no messages captured by provider")
	}

	// 默认转换应包含用户消息
	foundUser := false
	for _, msg := range captured {
		if msg.Role == message.RoleUser {
			for _, c := range msg.Content {
				if c.Type == message.ContentText && c.Text == "hello world" {
					foundUser = true
				}
			}
		}
	}
	if !foundUser {
		t.Errorf("expected user message 'hello world' in captured messages, got %v", captured)
	}
}

// TestCallbacks_MessageUpdateEmittedWithText 验证 EventMessageUpdate 在
// EventTextDelta 之后发射，载荷为 MessageUpdatePayload。
func TestCallbacks_MessageUpdateEmittedWithText(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hi"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Fatalf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventMessageUpdate) {
		t.Fatalf("missing EventMessageUpdate, got %v", genEventTypes(events))
	}

	// 验证每个 TextDelta 之后紧跟 MessageUpdate(text)
	for i, e := range events {
		if e.Type == event.EventTextDelta && i+1 < len(events) {
			next := events[i+1]
			if next.Type != event.EventMessageUpdate {
				t.Errorf("event after TextDelta at index %d is %v, want EventMessageUpdate", i, next.Type)
				continue
			}
			payload, ok := next.Payload.(event.MessageUpdatePayload)
			if !ok {
				t.Errorf("MessageUpdate payload type = %T, want MessageUpdatePayload", next.Payload)
				continue
			}
			if payload.Type != event.MessageUpdateText {
				t.Errorf("MessageUpdate.Type = %v, want %v", payload.Type, event.MessageUpdateText)
			}
			if payload.Content != "Hi" {
				t.Errorf("MessageUpdate.Content = %q, want %q", payload.Content, "Hi")
			}
		}
	}
}

// TestCallbacks_MessageUpdateEmittedWithThinking 验证 EventMessageUpdate 在
// EventThinkingDelta 之后发射，载荷为 MessageUpdatePayload(thinking)。
func TestCallbacks_MessageUpdateEmittedWithThinking(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamThinkingDelta, Thinking: "hmm"},
			{Type: stream.StreamTextDelta, Content: "answer"},
			{Type: stream.StreamDone},
		},
	}

	params, _ := setupGeneratorParams(responses, 0)
	result, events := runGeneratorTurn(params)

	if result.Status != event.StatusCompleted {
		t.Fatalf("status = %v, want %v", result.Status, event.StatusCompleted)
	}
	if !genHasEventType(events, event.EventMessageUpdate) {
		t.Fatalf("missing EventMessageUpdate, got %v", genEventTypes(events))
	}

	// 找 ThinkingDelta 后的 MessageUpdate
	foundThinkingUpdate := false
	for i, e := range events {
		if e.Type == event.EventThinkingDelta && i+1 < len(events) {
			next := events[i+1]
			if next.Type == event.EventMessageUpdate {
				payload, ok := next.Payload.(event.MessageUpdatePayload)
				if !ok {
					continue
				}
				if payload.Type == event.MessageUpdateThinking && payload.Content == "hmm" {
					foundThinkingUpdate = true
				}
			}
		}
	}
	if !foundThinkingUpdate {
		t.Error("expected MessageUpdate(thinking, 'hmm') after ThinkingDelta")
	}
}
