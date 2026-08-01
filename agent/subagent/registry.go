// Package subagent 定义 SubAgent 及其事件代理机制。
package subagent

import (
	"fmt"
	"sync"
)

// SubAgentRegistry 管理 SubAgent 注册表，用于 AgentTransfer。
type SubAgentRegistry struct {
	mu sync.RWMutex
	agents map[string]SubAgent
}

// NewSubAgentRegistry 创建 SubAgentRegistry。
func NewSubAgentRegistry() *SubAgentRegistry {
	return &SubAgentRegistry{
		agents: make(map[string]SubAgent),
	}
}

// Register 注册 SubAgent。
//
// 如果同名 Agent 已存在，返回错误。
func (r *SubAgentRegistry) Register(agent SubAgent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := agent.Name()
	if _, exists := r.agents[name]; exists {
		return fmt.Errorf("sub-agent %q already registered", name)
	}
	r.agents[name] = agent
	return nil
}

// Get 按名称获取 SubAgent。
func (r *SubAgentRegistry) Get(name string) (SubAgent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	a, ok := r.agents[name]
	return a, ok
}

// List 返回所有已注册的 SubAgent 名称。
func (r *SubAgentRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}

// CloseAll 关闭所有已注册的 SubAgent 并清空注册表。
func (r *SubAgentRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var firstErr error
	for name, agent := range r.agents {
		if err := agent.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close sub-agent %q: %w", name, err)
		}
	}
	r.agents = make(map[string]SubAgent)
	return firstErr
}
