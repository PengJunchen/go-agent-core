package eino

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
)

// DefaultDeepseekModel 是 DeepSeek 默认模型。
const DefaultDeepseekModel = "deepseek-chat"

func init() {
	registry.DefaultRegistry.RegisterProvider("deepseek", newDeepseekProvider)
}

// newDeepseekProvider 创建 DeepSeek provider（OpenAI 兼容协议）。
func newDeepseekProvider(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
	modelName := cfg.Model
	if modelName == "" {
		modelName = DefaultDeepseekModel
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}

	einoCfg := &openai.ChatModelConfig{
		Model: modelName,
		APIKey: cfg.APIKey,
		BaseURL: baseURL,
	}

	chatModel, err := openai.NewChatModel(context.Background(), einoCfg)
	if err != nil {
		return nil, fmt.Errorf("deepseek: create chat model: %w", err)
	}

	return NewEinoProvider(chatModel, "deepseek", modelName, 64000), nil
}
