package provider

import (
	"context"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// Interface-001: ModelProvider 接口可被实现。
func TestModelProvider_Interface(t *testing.T) {
	var _ ModelProvider = (*noopProvider)(nil)
}

// Interface-002: ChatOptions 可构造。
func TestChatOptions_Construct(t *testing.T) {
	temp := 0.7
	max := 4096
	opts := &ChatOptions{
		Temperature: &temp,
		MaxTokens: &max,
		StopSequences: []string{"END"},
		ThinkingMode: &ThinkingConfig{Enabled: true, Budget: 10000},
		ToolChoice: &ToolChoiceConfig{Mode: ToolChoiceAuto},
	}
	if *opts.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", *opts.Temperature)
	}
	if opts.ThinkingMode.Budget != 10000 {
		t.Errorf("Budget = %d, want 10000", opts.ThinkingMode.Budget)
	}
}

// Interface-003: ModelInfo 包含完整字段。
func TestModelInfo_Complete(t *testing.T) {
	info := &ModelInfo{
		Provider: "openai",
		ModelName: "gpt-4o",
		MaxTokens: 128000,
		SupportsThinking: true,
		SupportsVision: true,
		SupportsStreaming: true,
	}
	if info.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", info.Provider)
	}
}

// VQ-001: ChatOptions.ToolChoice 所有模式枚举值覆盖。
func TestToolChoiceMode_AllValues(t *testing.T) {
	if ToolChoiceAuto != 0 {
		t.Errorf("ToolChoiceAuto = %d, want 0", ToolChoiceAuto)
	}
	if ToolChoiceNone != 1 {
		t.Errorf("ToolChoiceNone = %d, want 1", ToolChoiceNone)
	}
	if ToolChoiceSpecific != 2 {
		t.Errorf("ToolChoiceSpecific = %d, want 2", ToolChoiceSpecific)
	}
}

// VQ-002: ToolSpec 可构造。
func TestToolSpec_Construct(t *testing.T) {
	spec := ToolSpec{
		Name: "get_weather",
		Description: "Get weather for a city",
		Parameters: map[string]any{"type": "object"},
	}
	if spec.Name != "get_weather" {
		t.Errorf("Name = %q, want get_weather", spec.Name)
	}
	if spec.Description != "Get weather for a city" {
		t.Errorf("Description = %q, want Get weather for a city", spec.Description)
	}
}

// VQ-003: ToolChoiceConfig 可构造。
func TestToolChoiceConfig_Construct(t *testing.T) {
	tcc := &ToolChoiceConfig{Mode: ToolChoiceSpecific, Name: "get_weather"}
	if tcc.Mode != ToolChoiceSpecific {
		t.Errorf("Mode = %d, want %d", tcc.Mode, ToolChoiceSpecific)
	}
	if tcc.Name != "get_weather" {
		t.Errorf("Name = %q, want get_weather", tcc.Name)
	}
}

// VQ-004: ModelProvider 实现可被调用。
func TestNoopProvider_StreamChatReturnNil(t *testing.T) {
	p := noopProvider{}
	ch, err := p.StreamChat(context.Background(), nil, nil)
	if err != nil {
		t.Errorf("StreamChat err = %v, want nil", err)
	}
	if ch != nil {
		t.Error("StreamChat ch should be nil")
	}
}

// VQ-005: ModelProvider 实现可被调用（Generate）。
func TestNoopProvider_GenerateReturnNil(t *testing.T) {
	p := noopProvider{}
	msg, err := p.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Errorf("Generate err = %v, want nil", err)
	}
	if msg != nil {
		t.Error("Generate msg should be nil")
	}
}

// VQ-006: ModelProvider 实现可被调用（ModelInfo）。
func TestNoopProvider_ModelInfoReturnNonNil(t *testing.T) {
	p := noopProvider{}
	info := p.ModelInfo()
	if info == nil {
		t.Error("ModelInfo should not be nil")
	}
}

// noopProvider 用于编译验证的空实现。
type noopProvider struct{}

func (noopProvider) StreamChat(_ context.Context, _ []message.Message, _ *ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, nil
}
func (noopProvider) Generate(_ context.Context, _ []message.Message, _ *ChatOptions) (*message.Message, error) {
	return nil, nil
}
func (noopProvider) ModelInfo() *ModelInfo { return &ModelInfo{} }
