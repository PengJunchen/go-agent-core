// Package registry 定义 Provider 注册表。
//
// ProviderRegistry 替代硬编码 if-else 路由：Provider 通过
// init() 自注册，第三方通过空白导入即可扩展，无需修改核心代码。
// 设计 registry.go 全局注册表。
package registry

import (
	"fmt"
	"sync"

	"github.com/pengjunchen/go-agent-core/llm/provider"
)

// ProviderConfig 是构造一个 Provider 实例所需的配置。
type ProviderConfig struct {
	Name string
	Model string
	APIKey string
	BaseURL string
	Extra map[string]any
}

// ProviderFactory 根据配置构造一个 ModelProvider 实例。
type ProviderFactory func(cfg *ProviderConfig) (provider.ModelProvider, error)

// ProviderRegistry 是 Provider 工厂注册表。
type ProviderRegistry struct {
	mu sync.RWMutex
	factories map[string]ProviderFactory
}

// NewProviderRegistry 构造空注册表。
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{factories: make(map[string]ProviderFactory)}
}

// RegisterProvider 注册一个 Provider 工厂（线程安全）。
func (r *ProviderRegistry) RegisterProvider(name string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// GetProvider 按名获取 Provider 实例。
func (r *ProviderRegistry) GetProvider(name string, cfg *ProviderConfig) (provider.ModelProvider, error) {
	r.mu.RLock()
	factory, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return factory(cfg)
}

// ListProviders 列出已注册的 Provider 名。
func (r *ProviderRegistry) ListProviders() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// DefaultRegistry 是全局默认注册表，供 init() 自注册使用。
var DefaultRegistry = NewProviderRegistry()
