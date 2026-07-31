package registry

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Compile-time check that DefaultToolRegistry implements ToolRegistry.
var _ ToolRegistry = (*DefaultToolRegistry)(nil)

// DefaultToolRegistry 是线程安全的内存工具注册表实现。
type DefaultToolRegistry struct {
	mu sync.RWMutex
	tools map[string]ToolDefinition
}

// NewDefaultToolRegistry 创建一个空的 DefaultToolRegistry。
func NewDefaultToolRegistry() *DefaultToolRegistry {
	return &DefaultToolRegistry{
		tools: make(map[string]ToolDefinition),
	}
}

// RegisterTool 注册一个工具。拒绝空名称和重复注册。
func (r *DefaultToolRegistry) RegisterTool(_ context.Context, tool ToolDefinition) error {
	if tool.Name == "" {
		return errors.New("tool name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[tool.Name]; exists {
		return errors.New("tool already registered: " + tool.Name)
	}
	r.tools[tool.Name] = tool
	return nil
}

// UnregisterTool 卸载一个工具。若工具不存在则返回错误。
func (r *DefaultToolRegistry) UnregisterTool(_ context.Context, toolName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[toolName]; !exists {
		return errors.New("tool not found: " + toolName)
	}
	delete(r.tools, toolName)
	return nil
}

// GetTool 按名称查询工具。若工具不存在则返回错误。
func (r *DefaultToolRegistry) GetTool(_ context.Context, toolName string) (ToolDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[toolName]
	if !exists {
		return ToolDefinition{}, errors.New("tool not found: " + toolName)
	}
	return tool, nil
}

// ListTools 返回所有已注册工具，按名称排序以保证确定性。
func (r *DefaultToolRegistry) ListTools(_ context.Context) ([]ToolDefinition, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ToolDefinition, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Reload 当前为 no-op，始终返回 nil。
// 未来可扩展为重新扫描 MCP 服务器等。
func (r *DefaultToolRegistry) Reload(_ context.Context) error {
	return nil
}
