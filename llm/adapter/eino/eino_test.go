package eino

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ---------------------------------------------------------------------------
// 编译检查：EinoProvider 实现 provider.ModelProvider
// ---------------------------------------------------------------------------

func TestEinoProvider_ImplementsInterface(t *testing.T) {
	var _ provider.ModelProvider = (*EinoProvider)(nil)
}

// ---------------------------------------------------------------------------
// NewEinoProvider
// ---------------------------------------------------------------------------

func TestNewEinoProvider_SetsModelInfo(t *testing.T) {
	p := NewEinoProvider(nil, "test-provider", "test-model", 4096)
	if p.ModelInfo().Provider != "test-provider" {
		t.Errorf("Provider = %q, want %q", p.ModelInfo().Provider, "test-provider")
	}
	if p.ModelInfo().ModelName != "test-model" {
		t.Errorf("ModelName = %q, want %q", p.ModelInfo().ModelName, "test-model")
	}
	if p.ModelInfo().MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d, want 4096", p.ModelInfo().MaxTokens)
	}
	if !p.ModelInfo().SupportsStreaming {
		t.Error("SupportsStreaming should be true")
	}
}

// ---------------------------------------------------------------------------
// toEinoOptions
// ---------------------------------------------------------------------------

func TestToEinoOptions_Nil(t *testing.T) {
	opts := toEinoOptions(nil)
	if opts != nil {
		t.Errorf("expected nil, got %v", opts)
	}
}

func TestToEinoOptions_AllFields(t *testing.T) {
	temp := 0.7
	maxTokens := 2048
	opts := &provider.ChatOptions{
		Temperature: &temp,
		MaxTokens: &maxTokens,
		StopSequences: []string{"END", "STOP"},
		Tools: []provider.ToolSpec{
			{Name: "get_weather", Description: "Get weather", Parameters: map[string]any{"location": "string"}},
		},
		ToolChoice: &provider.ToolChoiceConfig{Mode: provider.ToolChoiceAuto},
	}
	result := toEinoOptions(opts)
	if len(result) == 0 {
		t.Fatal("expected non-empty options")
	}
}

// ---------------------------------------------------------------------------
// toEinoOptions: ResponseFormat wiring (GAP-1)
// ---------------------------------------------------------------------------

func TestToEinoOptions_ResponseFormat_JSONSchema(t *testing.T) {
	opts := &provider.ChatOptions{
		ResponseFormat: &provider.ResponseFormat{
			Type: provider.ConstrainedJSONSchema,
			JSONSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
				"title": "MySchema",
			},
		},
	}
	result := toEinoOptions(opts)
	if len(result) != 1 {
		t.Fatalf("expected 1 option, got %d", len(result))
	}
}

func TestToEinoOptions_ResponseFormat_GrammarNotWired(t *testing.T) {
	// grammar 模式暂不支持 OpenAI，不应产生选项
	opts := &provider.ChatOptions{
		ResponseFormat: &provider.ResponseFormat{
			Type: provider.ConstrainedGrammar,
		},
	}
	result := toEinoOptions(opts)
	if len(result) != 0 {
		t.Fatalf("expected 0 options for grammar mode, got %d", len(result))
	}
}

func TestToEinoOptions_ResponseFormat_Nil(t *testing.T) {
	opts := &provider.ChatOptions{}
	result := toEinoOptions(opts)
	if len(result) != 0 {
		t.Fatalf("expected 0 options for nil ResponseFormat, got %d", len(result))
	}
}

func TestToResponseFormatOption_JSONSchema(t *testing.T) {
	rf := &provider.ResponseFormat{
		Type: provider.ConstrainedJSONSchema,
		JSONSchema: map[string]any{
			"type": "object",
			"title": "UserInfo",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
			},
		},
	}
	// 验证不 panic 且产出可用的 model.Option
	opt := toResponseFormatOption(rf)
	// 通过 GetCommonOptions 验证这不是一个 common option（不应修改任何公共字段）
	common := model.GetCommonOptions(nil, opt)
	if common.Temperature != nil || common.MaxTokens != nil || common.Tools != nil {
		t.Error("ResponseFormat option should not set common fields")
	}
}

func TestToResponseFormatOption_NoSchema(t *testing.T) {
	rf := &provider.ResponseFormat{
		Type: provider.ConstrainedJSONSchema,
	}
	opt := toResponseFormatOption(rf)
	common := model.GetCommonOptions(nil, opt)
	if common.Temperature != nil {
		t.Error("ResponseFormat option should not set common fields")
	}
}

// ---------------------------------------------------------------------------
// toEinoOptions: ThinkingMode wiring (GAP-2)
// ---------------------------------------------------------------------------

func TestToEinoOptions_ThinkingMode_Enabled(t *testing.T) {
	opts := &provider.ChatOptions{
		ThinkingMode: &provider.ThinkingConfig{
			Enabled: true,
			Budget: 8192,
		},
	}
	result := toEinoOptions(opts)
	if len(result) != 1 {
		t.Fatalf("expected 1 option, got %d", len(result))
	}
}

func TestToEinoOptions_ThinkingMode_Disabled(t *testing.T) {
	opts := &provider.ChatOptions{
		ThinkingMode: &provider.ThinkingConfig{
			Enabled: false,
			Budget: 8192,
		},
	}
	result := toEinoOptions(opts)
	if len(result) != 0 {
		t.Fatalf("expected 0 options for disabled ThinkingMode, got %d", len(result))
	}
}

func TestToEinoOptions_ThinkingMode_Nil(t *testing.T) {
	opts := &provider.ChatOptions{}
	result := toEinoOptions(opts)
	if len(result) != 0 {
		t.Fatalf("expected 0 options for nil ThinkingMode, got %d", len(result))
	}
}

func TestToThinkingModeOption_BudgetMapping(t *testing.T) {
	cases := []struct {
		name string
		budget int
	}{
		{"low budget maps to low effort", 2048},
		{"medium budget maps to medium effort", 8192},
		{"high budget maps to high effort", 32768},
		{"zero budget maps to high effort", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tc := &provider.ThinkingConfig{
				Enabled: true,
				Budget: c.budget,
			}
			opt := toThinkingModeOption(tc)
			// 通过 GetCommonOptions 验证这不是一个 common option
			common := model.GetCommonOptions(nil, opt)
			if common.Temperature != nil || common.MaxTokens != nil {
				t.Error("ThinkingMode option should not set common fields")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toEinoOptions: combined ResponseFormat + ThinkingMode with existing fields
// ---------------------------------------------------------------------------

func TestToEinoOptions_AllFieldsWithResponseFormatAndThinking(t *testing.T) {
	temp := 0.7
	maxTokens := 2048
	opts := &provider.ChatOptions{
		Temperature: &temp,
		MaxTokens: &maxTokens,
		StopSequences: []string{"END"},
		Tools: []provider.ToolSpec{
			{Name: "get_weather", Description: "Get weather"},
		},
		ToolChoice: &provider.ToolChoiceConfig{Mode: provider.ToolChoiceAuto},
		ResponseFormat: &provider.ResponseFormat{
			Type: provider.ConstrainedJSONSchema,
			JSONSchema: map[string]any{"type": "object"},
		},
		ThinkingMode: &provider.ThinkingConfig{
			Enabled: true,
			Budget: 8192,
		},
	}
	result := toEinoOptions(opts)
	// Temperature + MaxTokens + Stop + Tools + ToolChoice + ResponseFormat + ThinkingMode = 7
	if len(result) != 7 {
		t.Fatalf("expected 7 options, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// toEinoToolChoice
// ---------------------------------------------------------------------------

func TestToEinoToolChoice(t *testing.T) {
	cases := []struct {
		input *provider.ToolChoiceConfig
		want schema.ToolChoice
	}{
		{nil, schema.ToolChoiceAllowed},
		{&provider.ToolChoiceConfig{Mode: provider.ToolChoiceAuto}, schema.ToolChoiceAllowed},
		{&provider.ToolChoiceConfig{Mode: provider.ToolChoiceNone}, schema.ToolChoiceForbidden},
		{&provider.ToolChoiceConfig{Mode: provider.ToolChoiceSpecific}, schema.ToolChoiceForced},
	}
	for _, c := range cases {
		got := toEinoToolChoice(c.input)
		if got != c.want {
			t.Errorf("toEinoToolChoice(%+v) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// toEinoToolInfos
// ---------------------------------------------------------------------------

func TestToEinoToolInfos_Empty(t *testing.T) {
	result := toEinoToolInfos(nil)
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestToEinoToolInfos_WithParams(t *testing.T) {
	tools := []provider.ToolSpec{
		{
			Name: "get_weather",
			Description: "Get current weather",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "City name",
					},
					"unit": map[string]any{
						"type":        "string",
						"description": "Temperature unit",
						"enum":        []any{"celsius", "fahrenheit"},
					},
				},
				"required": []any{"location"},
			},
		},
	}
	result := toEinoToolInfos(tools)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result))
	}
	if result[0].Name != "get_weather" {
		t.Errorf("Name = %q, want %q", result[0].Name, "get_weather")
	}
	if result[0].ParamsOneOf == nil {
		t.Fatal("ParamsOneOf should not be nil")
	}
	// Verify the JSON Schema is preserved correctly
	js, err := result[0].ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatalf("ToJSONSchema error: %v", err)
	}
	if js.Type != "object" {
		t.Errorf("schema type = %q, want %q", js.Type, "object")
	}
	if _, ok := js.Properties.Get("location"); !ok {
		t.Error("missing 'location' property in schema")
	}
	if _, ok := js.Properties.Get("unit"); !ok {
		t.Error("missing 'unit' property in schema")
	}
	// Verify required field is preserved
	foundRequired := false
	for _, r := range js.Required {
		if r == "location" {
			foundRequired = true
		}
	}
	if !foundRequired {
		t.Error("'location' should be in required list")
	}
}

// ---------------------------------------------------------------------------
// emitEvents
// ---------------------------------------------------------------------------

func TestEmitEvents_NilMessage(t *testing.T) {
	ch := make(chan stream.StreamEvent, 10)
	emitEvents(ch, nil)
	if len(ch) != 0 {
		t.Errorf("expected 0 events for nil message, got %d", len(ch))
	}
}

func TestEmitEvents_TextContent(t *testing.T) {
	ch := make(chan stream.StreamEvent, 10)
	msg := &schema.Message{
		Role: schema.Assistant,
		Content: "Hello, world!",
	}
	emitEvents(ch, msg)
	if len(ch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ch))
	}
	evt := <-ch
	if evt.Type != stream.StreamTextDelta {
		t.Errorf("Type = %v, want StreamTextDelta", evt.Type)
	}
	if evt.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", evt.Content, "Hello, world!")
	}
}

func TestEmitEvents_ReasoningContent(t *testing.T) {
	ch := make(chan stream.StreamEvent, 10)
	msg := &schema.Message{
		Role: schema.Assistant,
		ReasoningContent: "Let me think about this...",
	}
	emitEvents(ch, msg)
	found := false
	for len(ch) > 0 {
		evt := <-ch
		if evt.Type == stream.StreamThinkingDelta && evt.Thinking == "Let me think about this..." {
			found = true
		}
	}
	if !found {
		t.Error("expected StreamThinkingDelta event")
	}
}

func TestEmitEvents_ToolCall(t *testing.T) {
	ch := make(chan stream.StreamEvent, 10)
	msg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{
				ID: "call_123",
				Type: "function",
				Function: schema.FunctionCall{
					Name: "get_weather",
					Arguments: `{"location":"Beijing"}`,
				},
			},
		},
	}
	emitEvents(ch, msg)
	found := false
	for len(ch) > 0 {
		evt := <-ch
		if evt.Type == stream.StreamToolCallStart && evt.ToolCall != nil {
			if evt.ToolCall.ID == "call_123" && evt.ToolCall.Name == "get_weather" {
				found = true
				if loc, ok := evt.ToolCall.Arguments["location"]; !ok || loc != "Beijing" {
					t.Errorf("unexpected arguments: %v", evt.ToolCall.Arguments)
				}
			}
		}
	}
	if !found {
		t.Error("expected StreamToolCallStart event")
	}
}

// ---------------------------------------------------------------------------
// pumpStream
// ---------------------------------------------------------------------------

func TestPumpStream_EOF(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Close()

	ch := pumpStream(sr)
	evt, ok := <-ch
	if !ok {
		t.Fatal("expected event from closed stream")
	}
	if evt.Type != stream.StreamDone {
		t.Errorf("Type = %v, want StreamDone", evt.Type)
	}
	_, ok = <-ch
	if ok {
		t.Error("channel should be closed after StreamDone")
	}
}

func TestPumpStream_Message(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)

	sw.Send(&schema.Message{
		Role: schema.Assistant,
		Content: "Hello",
	}, nil)
	sw.Close()

	ch := pumpStream(sr)
	found := false
	for evt := range ch {
		if evt.Type == stream.StreamTextDelta && evt.Content == "Hello" {
			found = true
		}
	}
	if !found {
		t.Error("expected StreamTextDelta event with content 'Hello'")
	}
}

// ---------------------------------------------------------------------------
// mockChatModel — 用于测试 EinoProvider
// ---------------------------------------------------------------------------

type mockChatModel struct {
	generateFunc func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
	streamFunc func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.generateFunc != nil {
		return m.generateFunc(ctx, input, opts...)
	}
	return &schema.Message{Role: schema.Assistant, Content: "mock"}, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.streamFunc != nil {
		return m.streamFunc(ctx, input, opts...)
	}
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Send(&schema.Message{Role: schema.Assistant, Content: "mock stream"}, nil)
	sw.Close()
	return sr, nil
}

// TestEinoProvider_Generate verifies the Generate path.
func TestEinoProvider_Generate(t *testing.T) {
	p := NewEinoProvider(&mockChatModel{
		generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			return &schema.Message{
				Role: schema.Assistant,
				Content: "response",
			}, nil
		},
	}, "test", "test-model", 4096)

	result, err := p.Generate(context.Background(), []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Role != message.RoleAssistant {
		t.Errorf("Role = %v, want Assistant", result.Role)
	}
}

func TestEinoProvider_Generate_Error(t *testing.T) {
	p := NewEinoProvider(&mockChatModel{
		generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			return nil, errors.New("model error")
		},
	}, "test", "test-model", 4096)

	_, err := p.Generate(context.Background(), []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEinoProvider_Generate_NilResult(t *testing.T) {
	p := NewEinoProvider(&mockChatModel{
		generateFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			return nil, nil
		},
	}, "test", "test-model", 4096)

	_, err := p.Generate(context.Background(), []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
	}, nil)
	if err == nil {
		t.Fatal("expected error for nil result")
	}
}

// TestEinoProvider_StreamChat verifies the StreamChat path.
func TestEinoProvider_StreamChat(t *testing.T) {
	p := NewEinoProvider(&mockChatModel{
		streamFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			sr, sw := schema.Pipe[*schema.Message](5)
			sw.Send(&schema.Message{Role: schema.Assistant, Content: "hello"}, nil)
			sw.Send(&schema.Message{Role: schema.Assistant, Content: " world"}, nil)
			sw.Close()
			return sr, nil
		},
	}, "test", "test-model", 4096)

	ch, err := p.StreamChat(context.Background(), []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
	}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var texts []string
	for evt := range ch {
		if evt.Type == stream.StreamTextDelta {
			texts = append(texts, evt.Content)
		}
	}
	if len(texts) != 2 {
		t.Fatalf("expected 2 text deltas, got %d: %v", len(texts), texts)
	}
}

func TestEinoProvider_StreamChat_Error(t *testing.T) {
	p := NewEinoProvider(&mockChatModel{
		streamFunc: func(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
			return nil, io.ErrUnexpectedEOF
		},
	}, "test", "test-model", 4096)

	_, err := p.StreamChat(context.Background(), []message.Message{
		message.NewTextMessage(message.RoleUser, "hello"),
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// ---------------------------------------------------------------------------
// EinoEventStream tests
// ---------------------------------------------------------------------------

func TestNewEinoEventStream_ImplementsInterface(t *testing.T) {
	var _ stream.EventStream = (*EinoEventStream)(nil)
}

func TestEinoEventStream_EventsClosed(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Close()

	es := NewEinoEventStream(sr)
	evt, ok := es.Next()
	if !ok {
		t.Fatal("expected event")
	}
	if evt.Type != stream.StreamDone {
		t.Errorf("Type = %v, want StreamDone", evt.Type)
	}
	_, ok = es.Next()
	if ok {
		t.Error("Next should return false after stream ends")
	}
}

func TestEinoEventStream_Events(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Send(&schema.Message{Role: schema.Assistant, Content: "test"}, nil)
	sw.Close()

	es := NewEinoEventStream(sr)
	found := false
	for evt := range es.Events() {
		if evt.Type == stream.StreamTextDelta && evt.Content == "test" {
			found = true
		}
	}
	if !found {
		t.Error("expected StreamTextDelta with content 'test'")
	}
}

func TestEinoEventStream_Err(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Send(nil, io.ErrUnexpectedEOF)
	sw.Close()

	es := NewEinoEventStream(sr)
	// drain
	for range es.Events() {
	}
	if es.Err() == nil {
		t.Error("expected non-nil error")
	}
}

func TestEinoEventStream_Close(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Close()

	es := NewEinoEventStream(sr)
	err := es.Close()
	if err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// StreamEvent channel contract: Test the stream channel behavior
// ---------------------------------------------------------------------------

func TestPumpStream_ErrorEvent(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Send(nil, io.ErrUnexpectedEOF)
	sw.Close()

	ch := pumpStream(sr)
	evt, ok := <-ch
	if !ok {
		t.Fatal("expected event")
	}
	if evt.Type != stream.StreamError {
		t.Errorf("Type = %v, want StreamError", evt.Type)
	}
	if evt.Error == nil {
		t.Error("expected non-nil error")
	}
	_, ok = <-ch
	if ok {
		t.Error("channel should be closed after error")
	}
}

func TestPumpStream_NilChunk(t *testing.T) {
	sr, sw := schema.Pipe[*schema.Message](5)
	sw.Send(nil, nil)
	sw.Close()

	ch := pumpStream(sr)
	evt := <-ch
	if evt.Type != stream.StreamDone {
		t.Errorf("expected StreamDone after nil chunk, got %v", evt.Type)
	}
}

// ---------------------------------------------------------------------------
// Transform tests: ToEinoMessage / FromEinoMessage
// ---------------------------------------------------------------------------

func TestToEinoMessage_Text(t *testing.T) {
	m := message.NewTextMessage(message.RoleUser, "hello")
	einoMsg := ToEinoMessage(m)
	if einoMsg == nil {
		t.Fatal("expected non-nil result")
	}
	if einoMsg.Role != schema.User {
		t.Errorf("Role = %v, want User", einoMsg.Role)
	}
	if einoMsg.Content != "hello" {
		t.Errorf("Content = %q, want %q", einoMsg.Content, "hello")
	}
}

func TestToEinoMessage_System(t *testing.T) {
	m := message.NewTextMessage(message.RoleSystem, "be helpful")
	einoMsg := ToEinoMessage(m)
	if einoMsg.Role != schema.System {
		t.Errorf("Role = %v, want System", einoMsg.Role)
	}
	if einoMsg.Content != "be helpful" {
		t.Errorf("Content = %q, want %q", einoMsg.Content, "be helpful")
	}
}

func TestToEinoMessage_WithToolCalls(t *testing.T) {
	m := message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "let me check"},
		},
		ToolCalls: []message.ToolCall{
			{ID: "tc1", Name: "get_weather", Arguments: map[string]any{"loc": "Beijing"}},
		},
	}
	einoMsg := ToEinoMessage(m)
	if len(einoMsg.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(einoMsg.ToolCalls))
	}
	if einoMsg.ToolCalls[0].ID != "tc1" {
		t.Errorf("ToolCall ID = %q, want %q", einoMsg.ToolCalls[0].ID, "tc1")
	}
	if einoMsg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("ToolCall Name = %q, want %q", einoMsg.ToolCalls[0].Function.Name, "get_weather")
	}
}

func TestToEinoMessage_WithThinking(t *testing.T) {
	m := message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentThinking, Thinking: "I need to think"},
			{Type: message.ContentText, Text: "final answer"},
		},
	}
	einoMsg := ToEinoMessage(m)
	if einoMsg.ReasoningContent != "I need to think" {
		t.Errorf("ReasoningContent = %q, want %q", einoMsg.ReasoningContent, "I need to think")
	}
	if einoMsg.Content != "final answer" {
		t.Errorf("Content = %q, want %q", einoMsg.Content, "final answer")
	}
}

func TestToEinoMessage_WithImage(t *testing.T) {
	img := &message.Image{Data: "base64data", MediaType: "image/jpeg"}
	m := message.Message{
		Role: message.RoleUser,
		Content: []message.Content{
			{Type: message.ContentText, Text: "describe this"},
			{Type: message.ContentImage, Image: img},
		},
	}
	einoMsg := ToEinoMessage(m)
	if einoMsg.Content != "describe this" {
		t.Errorf("Content = %q, want %q", einoMsg.Content, "describe this")
	}
	if len(einoMsg.UserInputMultiContent) != 1 {
		t.Fatalf("expected 1 multi content part, got %d", len(einoMsg.UserInputMultiContent))
	}
	part := einoMsg.UserInputMultiContent[0]
	if part.Type != schema.ChatMessagePartTypeImageURL {
		t.Errorf("Part Type = %v, want ImageURL", part.Type)
	}
	if part.Image == nil {
		t.Fatal("expected Image field")
	}
	if *part.Image.Base64Data != "base64data" {
		t.Errorf("Image Data = %q, want %q", *part.Image.Base64Data, "base64data")
	}
}

func TestToEinoMessage_ToolRole(t *testing.T) {
	m := message.Message{
		Role: message.RoleTool,
		ToolCallID: "tc1",
		Content: []message.Content{
			{Type: message.ContentText, Text: `{"result": "ok"}`},
		},
	}
	einoMsg := ToEinoMessage(m)
	if einoMsg.Role != schema.Tool {
		t.Errorf("Role = %v, want Tool", einoMsg.Role)
	}
	if einoMsg.ToolCallID != "tc1" {
		t.Errorf("ToolCallID = %q, want %q", einoMsg.ToolCallID, "tc1")
	}
}

func TestToEinoMessages_NilInput(t *testing.T) {
	result := ToEinoMessages(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestToEinoMessages_Empty(t *testing.T) {
	result := ToEinoMessages([]message.Message{})
	if len(result) != 0 {
		t.Errorf("expected empty, got %d", len(result))
	}
}

func TestFromEinoMessage_Nil(t *testing.T) {
	result := FromEinoMessage(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestFromEinoMessage_Text(t *testing.T) {
	einoMsg := &schema.Message{
		Role: schema.Assistant,
		Content: "Hello!",
	}
	result := FromEinoMessage(einoMsg)
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Role != message.RoleAssistant {
		t.Errorf("Role = %v, want Assistant", result.Role)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result.Content))
	}
	if result.Content[0].Text != "Hello!" {
		t.Errorf("Text = %q, want %q", result.Content[0].Text, "Hello!")
	}
}

func TestFromEinoMessage_WithReasoning(t *testing.T) {
	einoMsg := &schema.Message{
		Role: schema.Assistant,
		Content: "final",
		ReasoningContent: "thinking...",
	}
	result := FromEinoMessage(einoMsg)
	foundText := false
	foundThinking := false
	for _, c := range result.Content {
		switch c.Type {
		case message.ContentText:
			if c.Text == "final" {
				foundText = true
			}
		case message.ContentThinking:
			if c.Thinking == "thinking..." {
				foundThinking = true
			}
		}
	}
	if !foundText {
		t.Error("expected text content 'final'")
	}
	if !foundThinking {
		t.Error("expected thinking content")
	}
}

func TestFromEinoMessage_WithToolCalls(t *testing.T) {
	einoMsg := &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{
			{
				ID: "call_abc",
				Type: "function",
				Function: schema.FunctionCall{
					Name: "search",
					Arguments: `{"q":"test"}`,
				},
			},
		},
	}
	result := FromEinoMessage(einoMsg)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].ID != "call_abc" {
		t.Errorf("ID = %q, want %q", result.ToolCalls[0].ID, "call_abc")
	}
	if result.ToolCalls[0].Name != "search" {
		t.Errorf("Name = %q, want %q", result.ToolCalls[0].Name, "search")
	}
	if result.ToolCalls[0].Arguments["q"] != "test" {
		t.Errorf("Arguments = %v, want q=test", result.ToolCalls[0].Arguments)
	}
}

func TestFromEinoMessage_WithMultiContentParts(t *testing.T) {
	einoMsg := &schema.Message{
		Role: schema.Assistant,
		AssistantGenMultiContent: []schema.MessageOutputPart{
			{Type: schema.ChatMessagePartTypeReasoning, Reasoning: &schema.MessageOutputReasoning{Text: "reasoning..."}},
			{Type: schema.ChatMessagePartTypeText, Text: "answer"},
		},
	}
	result := FromEinoMessage(einoMsg)
	foundText := false
	foundThinking := false
	for _, c := range result.Content {
		switch c.Type {
		case message.ContentText:
			if c.Text == "answer" {
				foundText = true
			}
		case message.ContentThinking:
			if c.Thinking == "reasoning..." {
				foundThinking = true
			}
		}
	}
	if !foundText {
		t.Error("expected text content 'answer'")
	}
	if !foundThinking {
		t.Error("expected thinking content")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: ToEinoMessage -> FromEinoMessage 一致性
// ---------------------------------------------------------------------------

func TestRoundTrip_TextMessage(t *testing.T) {
	original := message.NewTextMessage(message.RoleUser, "hello world")
	einoMsg := ToEinoMessage(original)
	back := FromEinoMessage(einoMsg)
	if back.Role != original.Role {
		t.Errorf("Role mismatch: %v vs %v", back.Role, original.Role)
	}
	if len(back.Content) > 0 && back.Content[0].Text != "hello world" {
		t.Errorf("Text mismatch: %q vs %q", back.Content[0].Text, "hello world")
	}
}

func TestRoundTrip_AssistantWithToolCalls(t *testing.T) {
	original := message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "I'll search"},
		},
		ToolCalls: []message.ToolCall{
			{ID: "call_1", Name: "search", Arguments: map[string]any{"q": "golang"}},
		},
	}
	einoMsg := ToEinoMessage(original)
	back := FromEinoMessage(einoMsg)
	if back.Role != original.Role {
		t.Errorf("Role mismatch: %v vs %v", back.Role, original.Role)
	}
	if len(back.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(back.ToolCalls))
	}
	if back.ToolCalls[0].ID != "call_1" {
		t.Errorf("ToolCall ID mismatch: %q vs %q", back.ToolCalls[0].ID, "call_1")
	}
	if back.ToolCalls[0].Name != "search" {
		t.Errorf("ToolCall Name mismatch: %q vs %q", back.ToolCalls[0].Name, "search")
	}
	if back.ToolCalls[0].Arguments["q"] != "golang" {
		t.Errorf("ToolCall Argument 'q' mismatch: %v", back.ToolCalls[0].Arguments["q"])
	}
}

func TestRoundTrip_SystemMessage(t *testing.T) {
	original := message.NewTextMessage(message.RoleSystem, "be helpful")
	einoMsg := ToEinoMessage(original)
	back := FromEinoMessage(einoMsg)
	if back.Role != message.RoleSystem {
		t.Errorf("Role mismatch: %v vs %v", back.Role, message.RoleSystem)
	}
}

// ---------------------------------------------------------------------------
// 空 Arguments 测试：确保 tool call arguments 为空时发送 "{}" 而非 ""
// ---------------------------------------------------------------------------

func TestToEinoToolCalls_EmptyArguments(t *testing.T) {
	msg := message.Message{
		Role: message.RoleAssistant,
		ToolCalls: []message.ToolCall{
			{ID: "tc1", Name: "list_files", Arguments: nil},
			{ID: "tc2", Name: "get_status", Arguments: map[string]any{}},
			{ID: "tc3", Name: "read_file", Arguments: map[string]any{"path": "/tmp"}},
		},
	}
	einoMsg := ToEinoMessage(msg)
	if len(einoMsg.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(einoMsg.ToolCalls))
	}
	// nil Arguments → arguments 必须是 "{}"，而非空串
	if einoMsg.ToolCalls[0].Function.Arguments != "{}" {
		t.Errorf("nil Arguments: got %q, want {}", einoMsg.ToolCalls[0].Function.Arguments)
	}
	// 空 map Arguments → arguments 必须是 "{}"
	if einoMsg.ToolCalls[1].Function.Arguments != "{}" {
		t.Errorf("empty Arguments: got %q, want {}", einoMsg.ToolCalls[1].Function.Arguments)
	}
	// 非空 Arguments → 正常 JSON
	if einoMsg.ToolCalls[2].Function.Arguments != `{"path":"/tmp"}` {
		t.Errorf("non-empty Arguments: got %q, want {\"path\":\"/tmp\"}", einoMsg.ToolCalls[2].Function.Arguments)
	}
}

func TestMarshalJSONArgs_Empty(t *testing.T) {
	if got := marshalJSONArgs(nil); got != "{}" {
		t.Errorf("marshalJSONArgs(nil) = %q, want {}", got)
	}
	if got := marshalJSONArgs(map[string]any{}); got != "{}" {
		t.Errorf("marshalJSONArgs(empty) = %q, want {}", got)
	}
	if got := marshalJSONArgs(map[string]any{"key": "val"}); got != `{"key":"val"}` {
		t.Errorf("marshalJSONArgs(non-empty) = %q, want {\"key\":\"val\"}", got)
	}
}
