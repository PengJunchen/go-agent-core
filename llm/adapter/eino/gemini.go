//go:build eino_ext
// +build eino_ext

package eino

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/gemini"

	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
)

// DefaultGeminiModel 是 Gemini 默认模型。
const DefaultGeminiModel = "gemini-2.5-pro"

func init() {
	registry.DefaultRegistry.RegisterProvider("gemini", newGeminiProvider)
}

func newGeminiProvider(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
	modelName := cfg.Model
	if modelName == "" {
		modelName = DefaultGeminiModel
	}

	einoCfg := &gemini.Config{
		Model: modelName,
	}

	chatModel, err := gemini.NewChatModel(context.Background(), einoCfg)
	if err != nil {
		return nil, fmt.Errorf("gemini: create chat model: %w", err)
	}

	return NewEinoProvider(chatModel, "gemini", modelName, 1048576), nil
}
