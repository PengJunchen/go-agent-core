// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// prepare_next_turn.go 提供运行时模型切换机制：
// - PrepareNextTurnFunc: 每次 Turn 前回调，允许动态替换 ModelProvider
// - SwapableProvider: 原子可替换的 ModelProvider 包装器
package loop

import (
	"context"
	"sync"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ─── PrepareNextTurn 回调 ────────────────────────────────────────

// PrepareNextTurnFunc 在每个 Turn 执行前被调用，允许动态替换 ModelProvider。
//
// 它接收当前的 provider 和 turn 计数（从 0 开始），返回下一次 Turn 要使用的
// provider。返回 nil 表示保持当前 provider 不变。
type PrepareNextTurnFunc func(ctx context.Context, currentProvider provider.ModelProvider, turnCount int) provider.ModelProvider

// PrepareNextTurnConfig 配置 prepareNextTurn 回调行为。
type PrepareNextTurnConfig struct {
	// Fn 是回调函数。必填。
	Fn PrepareNextTurnFunc
}

// ─── SwapableProvider ────────────────────────────────────────────

// SwapableProvider 包装一个 ModelProvider，允许运行时原子替换。
//
// 所有方法（StreamChat/Generate/ModelInfo）都通过 RWMutex 保护，
// Swap 操作使用写锁，读操作使用读锁，保证并发安全。
type SwapableProvider struct {
	mu sync.RWMutex
	provider provider.ModelProvider
}

// NewSwapableProvider 创建一个 SwapableProvider。
func NewSwapableProvider(p provider.ModelProvider) *SwapableProvider {
	return &SwapableProvider{provider: p}
}

// StreamChat 流式聊天，委托给内部 provider。
func (sp *SwapableProvider) StreamChat(ctx context.Context, messages []message.Message, opts *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	sp.mu.RLock()
	p := sp.provider
	sp.mu.RUnlock()
	return p.StreamChat(ctx, messages, opts)
}

// Generate 同步生成，委托给内部 provider。
func (sp *SwapableProvider) Generate(ctx context.Context, messages []message.Message, opts *provider.ChatOptions) (*message.Message, error) {
	sp.mu.RLock()
	p := sp.provider
	sp.mu.RUnlock()
	return p.Generate(ctx, messages, opts)
}

// ModelInfo 返回模型元信息，委托给内部 provider。
func (sp *SwapableProvider) ModelInfo() *provider.ModelInfo {
	sp.mu.RLock()
	p := sp.provider
	sp.mu.RUnlock()
	return p.ModelInfo()
}

// Swap 原子替换内部 provider。
func (sp *SwapableProvider) Swap(newProvider provider.ModelProvider) {
	sp.mu.Lock()
	sp.provider = newProvider
	sp.mu.Unlock()
}

// Current 读取当前的 provider。
func (sp *SwapableProvider) Current() provider.ModelProvider {
	sp.mu.RLock()
	p := sp.provider
	sp.mu.RUnlock()
	return p
}
