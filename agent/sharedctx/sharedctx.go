// Package sharedctx 定义 Agent 间共享上下文机制。
//
// 提供线程安全的键值存储 SharedContext，支持乐观并发版本号、
// 快照、合并等操作。通过 context.Context 集成，可在编排场景中
// 在父 Agent 与子 Agent 之间传递共享状态。
package sharedctx

import "sync"

// SharedContext 是线程安全的键值存储，用于 Agent 间共享数据。
//
// 每次写操作递增 version，可用于乐观并发控制。
type SharedContext struct {
	mu sync.RWMutex
	data map[string]any
	version int64
}

// NewSharedContext 创建空的共享上下文。
func NewSharedContext() *SharedContext {
	return &SharedContext{
		data: make(map[string]any),
	}
}

// Set 存储一个键值对，并递增版本号。
func (sc *SharedContext) Set(key string, value any) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.data[key] = value
	sc.version++
}

// Get 按键检索值。若键不存在返回 nil, false。
func (sc *SharedContext) Get(key string) (any, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	v, ok := sc.data[key]
	return v, ok
}

// Delete 删除一个键，并递增版本号。
func (sc *SharedContext) Delete(key string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	delete(sc.data, key)
	sc.version++
}

// Keys 返回所有键。
func (sc *SharedContext) Keys() []string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	keys := make([]string, 0, len(sc.data))
	for k := range sc.data {
		keys = append(keys, k)
	}
	return keys
}

// Snapshot 返回所有数据的独立副本。
func (sc *SharedContext) Snapshot() map[string]any {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	cp := make(map[string]any, len(sc.data))
	for k, v := range sc.data {
		cp[k] = v
	}
	return cp
}

// Version 返回当前版本号（每次写操作递增）。
func (sc *SharedContext) Version() int64 {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sc.version
}

// Merge 将另一个 SharedContext 的数据合并到当前实例。
//
// 其他实例中的键会覆盖当前实例中同名的键，版本号按写入次数递增。
func (sc *SharedContext) Merge(other *SharedContext) {
	other.mu.RLock()
	snapshot := make(map[string]any, len(other.data))
	for k, v := range other.data {
		snapshot[k] = v
	}
	other.mu.RUnlock()

	sc.mu.Lock()
	defer sc.mu.Unlock()
	for k, v := range snapshot {
		sc.data[k] = v
		sc.version++
	}
}

// Clear 删除所有数据，并递增版本号。
func (sc *SharedContext) Clear() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.data = make(map[string]any)
	sc.version++
}
