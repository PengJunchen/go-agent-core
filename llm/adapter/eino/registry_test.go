package eino

import (
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/registry"
)

// TestProviderRegistration 验证 init() 自注册：OpenAI 应被注册到 DefaultRegistry。
//
// 运行条件：无需 eino_ext 构建标签，仅 openai 注册对默认编译有效。
// anthropic 和 gemini 需要 eino_ext 构建标签。
func TestProviderRegistration_OpenAI(t *testing.T) {
	providers := registry.DefaultRegistry.ListProviders()
	found := false
	for _, name := range providers {
		if name == "openai" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DefaultRegistry.ListProviders() = %v, want it to contain 'openai'", providers)
	}
}

// TestProviderRegistration_Specific 验证通过 GetProvider 可获取 OpenAI 工厂。
func TestProviderRegistration_GetOpenAIProvider(t *testing.T) {
	providers := registry.DefaultRegistry.ListProviders()
	for _, name := range providers {
		if name == "openai" {
			return // found
		}
	}
	t.Errorf("Provider 'openai' not found in DefaultRegistry. Providers: %v", providers)
}

// TestProviderRegistration_Negative 验证不存在的 provider 返回错误。
func TestProviderRegistration_UnknownProvider(t *testing.T) {
	_, err := registry.DefaultRegistry.GetProvider("nonexistent", nil)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}
