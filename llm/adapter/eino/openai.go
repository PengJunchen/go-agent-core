package eino

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
)

// DefaultOpenAIModel 是 OpenAI 默认模型。
const DefaultOpenAIModel = "gpt-4o"

func init() {
	registry.DefaultRegistry.RegisterProvider("openai", newOpenAIProvider)
}

func newOpenAIProvider(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
	modelName := cfg.Model
	if modelName == "" {
		modelName = DefaultOpenAIModel
	}

	einoCfg := &openai.ChatModelConfig{
		Model: modelName,
		APIKey: cfg.APIKey,
		BaseURL: cfg.BaseURL,
	}

	chatModel, err := openai.NewChatModel(context.Background(), einoCfg)
	if err != nil {
		return nil, fmt.Errorf("openai: create chat model: %w", err)
	}

	return NewEinoProvider(chatModel, "openai", modelName, 128000), nil
}
