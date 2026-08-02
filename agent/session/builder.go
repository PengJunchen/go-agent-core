package session

import (
	"errors"

	"github.com/pengjunchen/go-agent-core/agent/loop"
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/mcp"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	"github.com/pengjunchen/go-agent-core/config"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/production"
)

// DefaultSessionBuilder is a fluent builder for Session.
// It provides a chainable API to assemble all dependencies:
//
//	session, err := NewBuilder().
//	 WithProvider(p).
//	 WithContextManager(cm).
//	 WithToolRegistry(tr).
//	 Build()
type DefaultSessionBuilder struct {
	provider provider.ModelProvider
	contextManager ctxpkg.ContextManager
	toolRegistry registry.ToolRegistry
	hookPipeline *toolhook.HookPipeline
	middlewareChain *middleware.Chain
	maxTurns int
	retryConfig *loop.RetryConfig
	compactThreshold int
	productionBundle *production.ProductionBundle
	mcpServers *mcp.MCPProviderRegistry
	err error
}

// NewBuilder creates a new DefaultSessionBuilder with sensible defaults.
func NewBuilder() *DefaultSessionBuilder {
	return &DefaultSessionBuilder{
		hookPipeline: toolhook.NewHookPipeline(),
		middlewareChain: middleware.NewChain(),
		maxTurns: loop.DefaultMaxTurns,
	}
}

// WithProvider sets the ModelProvider (required).
func (b *DefaultSessionBuilder) WithProvider(p provider.ModelProvider) *DefaultSessionBuilder {
	b.provider = p
	return b
}

// WithContextManager sets the ContextManager (required).
func (b *DefaultSessionBuilder) WithContextManager(cm ctxpkg.ContextManager) *DefaultSessionBuilder {
	b.contextManager = cm
	return b
}

// WithToolRegistry sets the ToolRegistry (required).
func (b *DefaultSessionBuilder) WithToolRegistry(tr registry.ToolRegistry) *DefaultSessionBuilder {
	b.toolRegistry = tr
	return b
}

// WithHookPipeline sets the ToolHook pipeline. nil means no hooks.
func (b *DefaultSessionBuilder) WithHookPipeline(p *toolhook.HookPipeline) *DefaultSessionBuilder {
	b.hookPipeline = p
	return b
}

// WithMiddlewareChain sets the middleware chain. nil means no middleware.
func (b *DefaultSessionBuilder) WithMiddlewareChain(c *middleware.Chain) *DefaultSessionBuilder {
	b.middlewareChain = c
	return b
}

// WithMaxTurns sets the maximum number of agent loop iterations. Default: 20.
func (b *DefaultSessionBuilder) WithMaxTurns(n int) *DefaultSessionBuilder {
	b.maxTurns = n
	return b
}

// WithRetryConfig sets the retry configuration for LLM calls. nil means no retries.
func (b *DefaultSessionBuilder) WithRetryConfig(rc *loop.RetryConfig) *DefaultSessionBuilder {
	b.retryConfig = rc
	return b
}

// WithCompactThreshold sets the token threshold for auto-compaction. 0 disables.
func (b *DefaultSessionBuilder) WithCompactThreshold(threshold int) *DefaultSessionBuilder {
	b.compactThreshold = threshold
	return b
}

// WithProduction sets the production bundle (circuit breaker, security, etc.).
func (b *DefaultSessionBuilder) WithProduction(pb *production.ProductionBundle) *DefaultSessionBuilder {
	b.productionBundle = pb
	return b
}

// WithMCPServers sets the MCP provider registry. nil means no MCP servers.
func (b *DefaultSessionBuilder) WithMCPServers(reg *mcp.MCPProviderRegistry) *DefaultSessionBuilder {
	b.mcpServers = reg
	return b
}

// WithSettings applies config.Settings to the builder.
// Settings with non-zero values override the builder's current values.
func (b *DefaultSessionBuilder) WithSettings(settings config.Settings) *DefaultSessionBuilder {
	if settings.MaxTurns > 0 {
		b.maxTurns = settings.MaxTurns
	}
	if settings.CompactThreshold > 0 {
		b.compactThreshold = settings.CompactThreshold
	}
	return b
}

// NewBuilderFromSettings creates a DefaultSessionBuilder pre-configured from Settings.
// When Settings specifies Provider/Model, MCP servers, or tool subsets, they are
// auto-assembled via config.LoadAndAssemble. The caller still needs to set
// ContextManager if not already provided.
// If settings only has MaxTurns/CompactThreshold, this works as before (backward compatible).
func NewBuilderFromSettings(settings config.Settings, projectDir string) *DefaultSessionBuilder {
	b := NewBuilder()
	if settings.MaxTurns > 0 {
		b.maxTurns = settings.MaxTurns
	}
	if settings.CompactThreshold > 0 {
		b.compactThreshold = settings.CompactThreshold
	}

	// Config-driven auto-assembly: load Provider, MCP servers, and tool subset.
	ac, err := config.LoadAndAssemble(settings, projectDir)
	if err != nil {
		b.err = err
		return b
	}
	if ac.Provider != nil {
		b.provider = ac.Provider
	}
	if ac.ToolRegistry != nil {
		b.toolRegistry = ac.ToolRegistry
	}
	if ac.MCPServers != nil {
		b.mcpServers = ac.MCPServers
	}

	return b
}

// Build validates required fields and constructs the Session.
func (b *DefaultSessionBuilder) Build() (*Session, error) {
	if b.err != nil {
		return nil, b.err
	}

	if b.provider == nil {
		return nil, errors.New("model provider is required")
	}
	if b.contextManager == nil {
		return nil, errors.New("context manager is required")
	}
	if b.toolRegistry == nil {
		return nil, errors.New("tool registry is required")
	}

	// Build the underlying LoopAgent.
	agent, err := loop.NewDefaultLoopAgent(&loop.LoopAgentConfig{
		Provider: b.provider,
		ContextManager: b.contextManager,
		ToolRegistry: b.toolRegistry,
		HookPipeline: b.hookPipeline,
		MiddlewareChain: b.middlewareChain,
		MaxTurns: b.maxTurns,
		RetryConfig: b.retryConfig,
		CompactThreshold: b.compactThreshold,
		ProductionBundle: b.productionBundle,
	})
	if err != nil {
		return nil, err
	}

	return &Session{
		provider: b.provider,
		contextManager: b.contextManager,
		toolRegistry: b.toolRegistry,
		hookPipeline: b.hookPipeline,
		middlewareChain: b.middlewareChain,
		maxTurns: b.maxTurns,
		retryConfig: b.retryConfig,
		compactThreshold: b.compactThreshold,
		productionBundle: b.productionBundle,
		mcpServers: b.mcpServers,
		agent: agent,
	}, nil
}

// MustBuild builds the Session, panicking on error.
// Only use in tests or initialization where failure is not expected.
func (b *DefaultSessionBuilder) MustBuild() *Session {
	s, err := b.Build()
	if err != nil {
		panic(err)
	}
	return s
}
