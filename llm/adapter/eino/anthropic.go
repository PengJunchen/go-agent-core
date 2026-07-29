//go:build eino_ext
// +build eino_ext

package eino

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/agenticclaude"

	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/registry"
)

// DefaultAnthropicModel 是 Anthropic 默认模型。
const DefaultAnthropicModel = "claude-sonnet-4-20250514"

func init() {
	registry.DefaultRegistry.RegisterProvider("anthropic", newAnthropicProvider)
}

func newAnthropicProvider(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
	modelName := cfg.Model
	if modelName == "" {
		modelName = DefaultAnthropicModel
	}

	einoCfg := &agenticclaude.Config{
		Model: modelName,
		APIKey: cfg.APIKey,
		BaseURL: cfg.BaseURL,
	}

	agenticModel, err := agenticclaude.New(context.Background(), einoCfg)
	if err != nil {
		return nil, fmt.Errorf("anthropic: create chat model: %w", err)
	}

	return NewEinoAgenticProvider(agenticModel, "anthropic", modelName, 200000), nil
}
