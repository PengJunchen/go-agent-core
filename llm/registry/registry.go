// Package registry 定义 Provider 注册表。
//
// ProviderRegistry 替代硬编码 if-else 路由：Provider 通过
// init() 自注册，第三方通过空白导入即可扩展，无需修改核心代码。
// 设计 registry.go 全局注册表。
package registry

import (
	"context"
	"fmt"
	"sync"

	"github.com/pengjunchen/go-agent-core/llm/auth"
	"github.com/pengjunchen/go-agent-core/llm/provider"
)

// ProviderConfig 是构造一个 Provider 实例所需的配置。
type ProviderConfig struct {
	Name string
	Model string
	APIKey string
	BaseURL string
	Extra map[string]any

	// TokenSource 提供动态令牌（如 OAuth2），可选。
	// 当设置时，Provider 工厂应优先使用 TokenSource 获取访问令牌，
	// 而非静态 APIKey。典型用法：
	// - APIKey 认证：留空，工厂使用 config.APIKey
	// - OAuth2 认证：设置为 auth.NewTokenRefresher(...) 或 auth.NewOAuthTokenSource(...)
	// - 回退模式：设置为 auth.NewFallbackTokenSource(oauthSrc, staticSrc)
	//
	// 工厂实现示例：
	// if cfg.TokenSource != nil {
	// tok, _ := cfg.TokenSource.Token(ctx)
	// // 使用 tok.AccessToken 替代 cfg.APIKey
	// }
	TokenSource auth.TokenSource
}

// ResolveAPIKey 返回可用的 API 密钥。
// 当 TokenSource 已设置时，调用 TokenSource.Token 获取动态令牌并返回
// 其 AccessToken；否则返回静态 APIKey 字段。
// 工厂函数应优先使用此方法替代直接访问 cfg.APIKey，以支持 OAuth2 认证。
func (c *ProviderConfig) ResolveAPIKey(ctx context.Context) (string, error) {
	if c.TokenSource != nil {
		tok, err := c.TokenSource.Token(ctx)
		if err != nil {
			return "", fmt.Errorf("resolve token from TokenSource: %w", err)
		}
		return tok.AccessToken, nil
	}
	return c.APIKey, nil
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

// SwapProvider 原子替换已注册的 Provider 工厂（线程安全）。
// 如果 name 不存在，返回错误。
func (r *ProviderRegistry) SwapProvider(name string, factory ProviderFactory) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[name]; !ok {
		return fmt.Errorf("unknown provider: %s", name)
	}
	r.factories[name] = factory
	return nil
}

// DefaultRegistry 是全局默认注册表，供 init() 自注册使用。
var DefaultRegistry = NewProviderRegistry()
