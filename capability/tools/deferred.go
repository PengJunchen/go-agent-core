package tools

import (
	"context"
	"sync"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// Compile-time check: DeferredTool implements registry.DeferredLoader.
var _ registry.DeferredLoader = (*DeferredTool)(nil)

// DeferredTool represents a tool that is not loaded into memory until
// explicitly requested by the LLM. The Definition is always available
// (for tool listing), but the Handler is loaded on demand via Loader.
type DeferredTool struct {
	Definition registry.ToolDefinition
	Loader     func() (registry.ToolHandler, error)

	loaded  bool
	handler registry.ToolHandler
	loadErr error
	mu      sync.Mutex
}

// Load invokes the Loader function and caches the result. Subsequent
// calls return the cached handler without re-invoking Loader.
func (dt *DeferredTool) Load() (registry.ToolHandler, error) {
	dt.mu.Lock()
	defer dt.mu.Unlock()

	if dt.loaded {
		return dt.handler, dt.loadErr
	}

	dt.loaded = true
	dt.handler, dt.loadErr = dt.Loader()
	return dt.handler, dt.loadErr
}

// RegisterTo 将此延迟工具注册到 ToolRegistry。
// 工具定义（名称、描述、参数）立即可用，Handler 仅在首次调用时加载。
func (dt *DeferredTool) RegisterTo(ctx context.Context, reg registry.ToolRegistry) error {
	return reg.RegisterDeferred(ctx, dt.Definition, dt)
}

// SplitDeferredTools splits a list of tool handlers into "active" (always
// loaded) and "deferred" (loaded on demand) groups. The first maxActive
// tools become active; the rest become deferred tools with a Loader that
// simply returns the original handler.
//
// This is useful when there are many tools but only a subset is commonly
// used — the deferred tools' definitions can be listed for the LLM, but
// their handlers are only instantiated when actually invoked.
func SplitDeferredTools(toolHandlers []registry.ToolHandler, maxActive int) (active []registry.ToolHandler, deferred []*DeferredTool) {
	if len(toolHandlers) == 0 {
		return nil, nil
	}

	if maxActive < 0 {
		maxActive = 0
	}
	if maxActive > len(toolHandlers) {
		maxActive = len(toolHandlers)
	}

	active = make([]registry.ToolHandler, maxActive)
	copy(active, toolHandlers[:maxActive])

	for i := maxActive; i < len(toolHandlers); i++ {
		handler := toolHandlers[i]
		dt := &DeferredTool{
			Loader: func() (registry.ToolHandler, error) {
				return handler, nil
			},
		}
		deferred = append(deferred, dt)
	}

	return active, deferred
}
