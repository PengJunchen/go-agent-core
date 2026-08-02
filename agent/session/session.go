// Package session provides the Session facade that assembles all agent
// dependencies (Provider, ContextManager, ToolRegistry, HookPipeline,
// ProductionBundle) into a single entry point.
//
// Session is the top-level API for running an agent. Users configure it
// via DefaultSessionBuilder and call Query to trigger the agent loop.
// Zero-configuration defaults are available via NewDefaultSession.
package session

import (
	"context"
	"errors"
	"sync"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
	"github.com/pengjunchen/go-agent-core/agent/middleware"
	"github.com/pengjunchen/go-agent-core/capability/mcp"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/production"
)

// Compile-time check that Session implements LoopAgent (delegates to it).
var _ loop.LoopAgent = (*Session)(nil)

// Session is the top-level facade that assembles all agent dependencies
// and provides a simple Query API.
//
// It wraps a DefaultLoopAgent and holds references to all injected
// dependencies. Users interact with the agent exclusively through Session.
type Session struct {
	// Configured dependencies (read-only after Build).
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

	// The underlying LoopAgent that executes the agent loop.
	agent loop.LoopAgent

	// Run-slot protection: only one query can run at a time.
	mu sync.Mutex
	running bool
	closed bool
}

// Query triggers the agent loop with the given prompt.
// It delegates to the underlying LoopAgent's Query method.
// Only one query can run at a time; concurrent queries return an error.
func (s *Session) Query(ctx context.Context, input loop.AgentInput) (<-chan event.AgentEvent, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("session is closed")
	}
	if s.running {
		s.mu.Unlock()
		return nil, errors.New("a query is already running")
	}
	s.running = true
	s.mu.Unlock()

	// Use a wrapper to track when the query finishes.
	// We do this by wrapping the event channel.
	eventCh, err := s.agent.Query(ctx, input)
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return nil, err
	}

	// Wrap the event channel to detect completion.
	outCh := make(chan event.AgentEvent, loop.EventChannelSize)
	go func() {
		defer close(outCh)
		for evt := range eventCh {
			outCh <- evt
			if evt.Type == event.EventCompleted || evt.Type == event.EventError {
				// Mark as not running.
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
			}
		}
		// If the channel closed without EventCompleted/Error, still reset.
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	return outCh, nil
}

// Interrupt delegates to the underlying LoopAgent.
func (s *Session) Interrupt(ctx context.Context) error {
	return s.agent.Interrupt(ctx)
}

// Steer delegates to the underlying LoopAgent.
func (s *Session) Steer(ctx context.Context, message string) error {
	return s.agent.Steer(ctx, message)
}

// FollowUp delegates to the underlying LoopAgent.
func (s *Session) FollowUp(ctx context.Context, content string) error {
	return s.agent.FollowUp(ctx, content)
}

// Status returns the current agent status.
func (s *Session) Status() event.AgentStatus {
	return s.agent.Status()
}

// Close releases all resources.
func (s *Session) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.agent.Close()
}

// ContextManager returns the underlying ContextManager (for initial context injection).
func (s *Session) ContextManager() ctxpkg.ContextManager {
	return s.contextManager
}

// ToolRegistry returns the underlying ToolRegistry (for dynamic tool registration).
func (s *Session) ToolRegistry() registry.ToolRegistry {
	return s.toolRegistry
}

// Provider returns the underlying ModelProvider.
func (s *Session) Provider() provider.ModelProvider {
	return s.provider
}

// MCPServers returns the underlying MCP provider registry.
func (s *Session) MCPServers() *mcp.MCPProviderRegistry {
	return s.mcpServers
}
