package registry

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ─── 测试用 Mock ──────────────────────────────────────────────────

// testProvider 是用于注册表测试的 mock provider。
type testProvider struct {
	name string
}

func (p *testProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 1)
	go func() {
		defer close(ch)
		ch <- stream.StreamEvent{Type: stream.StreamDone}
	}()
	return ch, nil
}

func (p *testProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: p.name}},
	}, nil
}

func (p *testProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: p.name, ModelName: p.name + "-model"}
}

// ─── 测试用例 ──────────────────────────────────────────────────────

// TestProviderRegistry_SwapProvider 验证 SwapProvider 原子替换已注册的工厂。
func TestProviderRegistry_SwapProvider(t *testing.T) {
	reg := NewProviderRegistry()

	// 注册初始工厂
	factory1Called := false
	reg.RegisterProvider("test-provider", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		factory1Called = true
		return &testProvider{name: "factory1"}, nil
	})

	// 验证初始工厂可用
	p, err := reg.GetProvider("test-provider", &ProviderConfig{})
	if err != nil {
		t.Fatalf("GetProvider before swap: %v", err)
	}
	if p.ModelInfo().Provider != "factory1" {
		t.Errorf("before swap: provider = %q, want %q", p.ModelInfo().Provider, "factory1")
	}

	// SwapProvider 替换工厂
	factory2Called := false
	err = reg.SwapProvider("test-provider", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		factory2Called = true
		return &testProvider{name: "factory2"}, nil
	})
	if err != nil {
		t.Fatalf("SwapProvider: %v", err)
	}

	// 使用新工厂获取 provider
	factory1Called = false // 重置
	p, err = reg.GetProvider("test-provider", &ProviderConfig{})
	if err != nil {
		t.Fatalf("GetProvider after swap: %v", err)
	}

	if factory1Called {
		t.Error("factory1 should not be called after swap")
	}
	if !factory2Called {
		t.Error("factory2 should be called after swap")
	}
	if p.ModelInfo().Provider != "factory2" {
		t.Errorf("after swap: provider = %q, want %q", p.ModelInfo().Provider, "factory2")
	}
}

// TestProviderRegistry_SwapProviderNonexistent 验证 SwapProvider 对不存在的 provider 返回错误。
func TestProviderRegistry_SwapProviderNonexistent(t *testing.T) {
	reg := NewProviderRegistry()

	err := reg.SwapProvider("nonexistent", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		return &testProvider{name: "new"}, nil
	})
	if err == nil {
		t.Error("expected error for swapping nonexistent provider")
	}
}

// TestProviderRegistry_SwapProviderConcurrent 验证并发 SwapProvider 安全。
func TestProviderRegistry_SwapProviderConcurrent(t *testing.T) {
	reg := NewProviderRegistry()

	// 注册初始工厂
	reg.RegisterProvider("concurrent-provider", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		return &testProvider{name: "initial"}, nil
	})

	var wg sync.WaitGroup
	var errors atomic.Int32

	// 并发 Swap
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := reg.SwapProvider("concurrent-provider", func(_ *ProviderConfig) (provider.ModelProvider, error) {
				return &testProvider{name: "concurrent"}, nil
			})
			if err != nil {
				errors.Add(1)
			}
		}(i)
	}

	// 并发 GetProvider
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := reg.GetProvider("concurrent-provider", &ProviderConfig{})
			if err != nil {
				errors.Add(1)
			}
		}()
	}

	wg.Wait()

	if errors.Load() > 0 {
		t.Errorf("got %d errors during concurrent SwapProvider + GetProvider", errors.Load())
	}
}

// TestProviderRegistry_RegisterAndGet 验证基本注册和获取。
func TestProviderRegistry_RegisterAndGet(t *testing.T) {
	reg := NewProviderRegistry()

	reg.RegisterProvider("test", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		return &testProvider{name: "test"}, nil
	})

	p, err := reg.GetProvider("test", &ProviderConfig{})
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if p.ModelInfo().Provider != "test" {
		t.Errorf("provider = %q, want %q", p.ModelInfo().Provider, "test")
	}
}

// TestProviderRegistry_GetUnknownProvider 验证获取未注册的 provider 返回错误。
func TestProviderRegistry_GetUnknownProvider(t *testing.T) {
	reg := NewProviderRegistry()

	_, err := reg.GetProvider("unknown", &ProviderConfig{})
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

// TestProviderRegistry_ListProviders 验证 ListProviders 列出所有已注册的 provider。
func TestProviderRegistry_ListProviders(t *testing.T) {
	reg := NewProviderRegistry()

	reg.RegisterProvider("a", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		return &testProvider{name: "a"}, nil
	})
	reg.RegisterProvider("b", func(_ *ProviderConfig) (provider.ModelProvider, error) {
		return &testProvider{name: "b"}, nil
	})

	names := reg.ListProviders()
	if len(names) != 2 {
		t.Errorf("ListProviders count = %d, want 2", len(names))
	}
}
