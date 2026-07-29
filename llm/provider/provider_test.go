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

// noopProvider 用于编译验证的空实现。
type noopProvider struct{}

func (noopProvider) StreamChat(_ context.Context, _ []message.Message, _ *ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, nil
}
func (noopProvider) Generate(_ context.Context, _ []message.Message, _ *ChatOptions) (*message.Message, error) {
	return nil, nil
}
func (noopProvider) ModelInfo() *ModelInfo { return &ModelInfo{} }
