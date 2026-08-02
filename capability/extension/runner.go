package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ExtensionFactory 创建 Extension 实例的工厂函数。
type ExtensionFactory func() Extension

var (
	factoryMu sync.RWMutex
	factories = make(map[string]ExtensionFactory)
)

// RegisterFactory 注册一个扩展工厂。
// 工厂名称在全局注册表中必须唯一。
func RegisterFactory(name string, factory ExtensionFactory) {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	factories[name] = factory
}

// LookupFactory 按名称查找已注册的扩展工厂。
func LookupFactory(name string) (ExtensionFactory, bool) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	f, ok := factories[name]
	return f, ok
}

// manifestEntry 描述 extensions.json 中的一个扩展条目。
type manifestEntry struct {
	Name string `json:"name"`
	Factory string `json:"factory"`
}

// manifest 描述 extensions.json 的完整结构。
type manifest struct {
	Extensions []manifestEntry `json:"extensions"`
}

// ExtensionRunner 管理所有扩展的生命周期。
// 它将事件分发给监听器，并管理激活/停用。
type ExtensionRunner struct {
	mu sync.RWMutex
	extensions []Extension
	api *ExtensionAPI
}

// NewExtensionRunner 创建一个新的 ExtensionRunner。
func NewExtensionRunner(api *ExtensionAPI) *ExtensionRunner {
	return &ExtensionRunner{api: api}
}

// Register 添加一个扩展。
func (r *ExtensionRunner) Register(ext Extension) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extensions = append(r.extensions, ext)
}

// ActivateAll 激活所有已注册的扩展（按优先级从高到低）。
func (r *ExtensionRunner) ActivateAll(ctx context.Context) error {
	r.mu.RLock()
	sorted := make([]Extension, len(r.extensions))
	copy(sorted, r.extensions)
	r.mu.RUnlock()

	// 按优先级从高到低排序
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority() > sorted[j].Priority()
	})

	for _, ext := range sorted {
		r.api.setCurrentPriority(ext.Priority())
		if err := ext.Activate(ctx, r.api); err != nil {
			r.api.setCurrentPriority(0)
			return fmt.Errorf("activate extension %s: %w", ext.Name(), err)
		}
		slog.Info("extension activated", "name", ext.Name())
	}
	r.api.setCurrentPriority(0)
	return nil
}

// DeactivateAll 停用所有已注册的扩展。
func (r *ExtensionRunner) DeactivateAll(ctx context.Context) {
	r.mu.RLock()
	sorted := make([]Extension, len(r.extensions))
	copy(sorted, r.extensions)
	r.mu.RUnlock()

	// 按优先级从低到高停用（与激活顺序相反）
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority() < sorted[j].Priority()
	})

	for _, ext := range sorted {
		if err := ext.Deactivate(ctx); err != nil {
			slog.Warn("extension deactivation failed", "name", ext.Name(), "error", err)
		}
	}
}

// Load 从目录加载扩展。
// 扫描目录中的 extensions.json 清单文件，使用已注册的工厂创建扩展并注册。
func (r *ExtensionRunner) Load(_ context.Context, dir string) error {
	manifestPath := filepath.Join(dir, "extensions.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %s: %w", manifestPath, err)
	}

	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse manifest %s: %w", manifestPath, err)
	}

	for _, entry := range m.Extensions {
		f, ok := LookupFactory(entry.Factory)
		if !ok {
			return fmt.Errorf("factory %q not registered for extension %q", entry.Factory, entry.Name)
		}
		ext := f()
		r.Register(ext)
		slog.Info("extension loaded from manifest", "name", entry.Name, "factory", entry.Factory)
	}

	return nil
}

// Init 初始化所有已注册的扩展。
// 按优先级从高到低调用每个扩展的 Activate 方法。
// 如果任何激活失败，返回错误。
func (r *ExtensionRunner) Init(ctx context.Context) error {
	return r.ActivateAll(ctx)
}

// Shutdown 优雅关闭所有扩展。
// 按优先级从低到高调用每个扩展的 Deactivate 方法。
// 如果任何停用失败，聚合所有错误返回。
func (r *ExtensionRunner) Shutdown(ctx context.Context) error {
	r.mu.RLock()
	sorted := make([]Extension, len(r.extensions))
	copy(sorted, r.extensions)
	r.mu.RUnlock()

	// 按优先级从低到高停用（与激活顺序相反）
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Priority() < sorted[j].Priority()
	})

	var errs []string
	for _, ext := range sorted {
		if err := ext.Deactivate(ctx); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", ext.Name(), err.Error()))
		}
		slog.Info("extension deactivated", "name", ext.Name())
	}

	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// EmitEvent 将事件分发给所有已注册的监听器。
// 监听器按优先级从高到低调用。
// 返回聚合后的 *EventResult：若任一监听器返回 Block 则返回 Block，
// 若任一返回 Cancel 则返回 Cancel，若任一返回 Replace 则使用最后一个 Replace，
// 否则返回 nil（允许）。
func (r *ExtensionRunner) EmitEvent(event Event) *EventResult {
	r.api.mu.RLock()
	listeners := make([]priorityListener, len(r.api.listeners[event.Type]))
	copy(listeners, r.api.listeners[event.Type])
	r.api.mu.RUnlock()

	// 按优先级从高到低排序
	sort.SliceStable(listeners, func(i, j int) bool {
		return listeners[i].priority > listeners[j].priority
	})

	var lastReplace *EventResult
	for _, pl := range listeners {
		result := func() *EventResult {
			defer func() {
				if rv := recover(); rv != nil {
					slog.Error("event listener panic", "error", rv)
				}
			}()
			return pl.listener(event)
		}()

		if result == nil {
			continue
		}
		switch result.Action {
		case EventActionBlock:
			return result
		case EventActionCancel:
			return result
		case EventActionReplace:
			lastReplace = result
		}
	}
	return lastReplace
}
