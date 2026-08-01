// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// builder.go 提供 DefaultLoopAgent 的构造函数与便捷构建器，
// 以及 AgentHarness 的构建器。
package loop

import (
	"errors"
	"fmt"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/memory/session"
	"github.com/pengjunchen/go-agent-core/production"
)

// ─── 构造错误 ────────────────────────────────────────────────────

var (
	// ErrNoProvider 未设置 ModelProvider。
	ErrNoProvider = errors.New("model provider is required")
	// ErrNoContextManager 未设置 ContextManager。
	ErrNoContextManager = errors.New("context manager is required")
	// ErrNoToolRegistry 未设置 ToolRegistry。
	ErrNoToolRegistry = errors.New("tool registry is required")
	// ErrNoAgent 未设置 LoopAgent（HarnessBuilder 必填）。
	ErrNoAgent = errors.New("loop agent is required")
)

// NewDefaultLoopAgent 根据 LoopAgentConfig 构造一个 DefaultLoopAgent。
//
// 必填字段：
// - Provider
// - ContextManager
// - ToolRegistry
//
// 可选字段（nil 时使用安全默认值）：
// - HookPipeline（nil 表示不执行钩子）
// - MiddlewareChain（nil 表示不执行中间件）
// - Logger（nil 表示不记录执行日志）
// - MaxTurns（0 表示使用 DefaultMaxTurns=20）
func NewDefaultLoopAgent(cfg *LoopAgentConfig) (*DefaultLoopAgent, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}

	// 校验必填字段
	if cfg.Provider == nil {
		return nil, ErrNoProvider
	}
	if cfg.ContextManager == nil {
		return nil, ErrNoContextManager
	}
	if cfg.ToolRegistry == nil {
		return nil, ErrNoToolRegistry
	}

	// MaxTurns 默认值
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}

	agent := &DefaultLoopAgent{
		provider: cfg.Provider,
		contextManager: cfg.ContextManager,
		toolRegistry: cfg.ToolRegistry,
		hookPipeline: cfg.HookPipeline,
		middlewareChain: cfg.MiddlewareChain,
		logger: cfg.Logger,
		maxTurns: maxTurns,
		retryConfig: cfg.RetryConfig,
		compactThreshold: cfg.CompactThreshold,
		prepareNextTurn: cfg.PrepareNextTurn,
		productionBundle: cfg.ProductionBundle,
		generator: NewDefaultLoopGenerator(),
		status: event.StatusIdle,
	}

	return agent, nil
}

// ─── LoopAgent 构建器 ────────────────────────────────────────────

// LoopAgentBuilder 是 DefaultLoopAgent 的流式构建器。
//
// 提供链式 API 以简化配置组装：
//
//	agent, err := NewBuilder().
//	 WithProvider(p).
//	 WithContextManager(cm).
//	 WithToolRegistry(tr).
//	 WithMaxTurns(30).
//	 Build()
type LoopAgentBuilder struct {
	cfg *LoopAgentConfig
	generator LoopGenerator
	err error
}

// NewBuilder 创建一个新的 LoopAgentBuilder。
func NewBuilder() *LoopAgentBuilder {
	return &LoopAgentBuilder{
		cfg: &LoopAgentConfig{
			HookPipeline: toolhook.NewHookPipeline(),
			MiddlewareChain: middleware.NewChain(),
		},
	}
}

// WithProvider 设置 ModelProvider。
func (b *LoopAgentBuilder) WithProvider(p provider.ModelProvider) *LoopAgentBuilder {
	b.cfg.Provider = p
	return b
}

// WithContextManager 设置 ContextManager。
func (b *LoopAgentBuilder) WithContextManager(cm ctxpkg.ContextManager) *LoopAgentBuilder {
	b.cfg.ContextManager = cm
	return b
}

// WithToolRegistry 设置 ToolRegistry。
func (b *LoopAgentBuilder) WithToolRegistry(tr registry.ToolRegistry) *LoopAgentBuilder {
	b.cfg.ToolRegistry = tr
	return b
}

// WithHookPipeline 设置 ToolHook 管道。传入 nil 表示禁用钩子。
func (b *LoopAgentBuilder) WithHookPipeline(p *toolhook.HookPipeline) *LoopAgentBuilder {
	b.cfg.HookPipeline = p
	return b
}

// WithMiddlewareChain 设置中间件链。传入 nil 表示禁用中间件。
func (b *LoopAgentBuilder) WithMiddlewareChain(c *middleware.Chain) *LoopAgentBuilder {
	b.cfg.MiddlewareChain = c
	return b
}

// WithLogger 设置执行日志记录器。
func (b *LoopAgentBuilder) WithLogger(l log.ExecLogger) *LoopAgentBuilder {
	b.cfg.Logger = l
	return b
}

// WithMaxTurns 设置最大轮次。
func (b *LoopAgentBuilder) WithMaxTurns(n int) *LoopAgentBuilder {
	b.cfg.MaxTurns = n
	return b
}

// WithRetryConfig 设置重试配置。传入 nil 表示不重试。
func (b *LoopAgentBuilder) WithRetryConfig(rc *RetryConfig) *LoopAgentBuilder {
	b.cfg.RetryConfig = rc
	return b
}

// WithCompactThreshold 设置自动压缩的 token 阈值。0 表示禁用自动压缩。
func (b *LoopAgentBuilder) WithCompactThreshold(threshold int) *LoopAgentBuilder {
	b.cfg.CompactThreshold = threshold
	return b
}

// WithPrepareNextTurn 设置运行时 Provider 切换函数。传入 nil 表示不启用。
func (b *LoopAgentBuilder) WithPrepareNextTurn(fn PrepareNextTurnFunc) *LoopAgentBuilder {
	b.cfg.PrepareNextTurn = fn
	return b
}

// WithProduction 设置生产化组件包。传入 nil 表示不启用。
func (b *LoopAgentBuilder) WithProduction(pb *production.ProductionBundle) *LoopAgentBuilder {
	b.cfg.ProductionBundle = pb
	return b
}

// WithGenerator 设置自定义 LoopGenerator。不调用此方法时使用 DefaultLoopGenerator。
func (b *LoopAgentBuilder) WithGenerator(g LoopGenerator) *LoopAgentBuilder {
	b.generator = g
	return b
}

// Build 构建 DefaultLoopAgent。
//
// 调用前必须已设置 Provider、ContextManager、ToolRegistry。
func (b *LoopAgentBuilder) Build() (*DefaultLoopAgent, error) {
	if b.err != nil {
		return nil, b.err
	}
	agent, err := NewDefaultLoopAgent(b.cfg)
	if err != nil {
		return nil, err
	}

	// 如果设置了自定义 Generator，覆盖默认值
	if b.generator != nil {
		agent.generator = b.generator
	}

	return agent, nil
}

// MustBuild 构建 DefaultLoopAgent，出错时 panic。
// 仅用于测试或初始化时确定不会出错的场景。
func (b *LoopAgentBuilder) MustBuild() *DefaultLoopAgent {
	agent, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("loop: build failed: %v", err))
	}
	return agent
}

// ─── AgentHarness 构建器 ─────────────────────────────────────────

// HarnessBuilder 是 DefaultAgentHarness 的流式构建器。
//
// 提供链式 API 以简化 AgentHarness 配置组装：
//
//	harness, err := NewHarnessBuilder(agent).
//	 WithSessionManager(sm).
//	 WithLogger(l).
//	 Build()
type HarnessBuilder struct {
	agent LoopAgent
	session session.SessionManager
	context ctxpkg.ContextManager
	logger log.ExecLogger
	err error
}

// NewHarnessBuilder 创建一个新的 HarnessBuilder。
func NewHarnessBuilder(agent LoopAgent) *HarnessBuilder {
	return &HarnessBuilder{
		agent: agent,
	}
}

// WithSessionManager 设置 SessionManager。
func (b *HarnessBuilder) WithSessionManager(sm session.SessionManager) *HarnessBuilder {
	b.session = sm
	return b
}

// WithContextManager 设置 ContextManager（用于会话恢复）。
func (b *HarnessBuilder) WithContextManager(cm ctxpkg.ContextManager) *HarnessBuilder {
	b.context = cm
	return b
}

// WithLogger 设置 ExecLogger。
func (b *HarnessBuilder) WithLogger(l log.ExecLogger) *HarnessBuilder {
	b.logger = l
	return b
}

// Build 构建 DefaultAgentHarness。
//
// 调用前必须已设置 LoopAgent。
func (b *HarnessBuilder) Build() (*DefaultAgentHarness, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.agent == nil {
		return nil, ErrNoAgent
	}

	harness := &DefaultAgentHarness{
		agent: b.agent,
		session: b.session,
		context: b.context,
		logger: b.logger,
	}

	return harness, nil
}
