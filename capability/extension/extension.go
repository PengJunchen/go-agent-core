// Package extension 定义扩展系统。
//
// Extension 系统允许第三方代码在运行时扩展 Agent 能力：
// 注册工具、命令和 Provider，监听 Agent 生命周期事件，
// 通过 ExtensionAPI 执行操作。ExtensionRunner 管理所有扩展的
// 生命周期并分发事件。
package extension

import (
	"context"
	"errors"
	"sync"

	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	llmregistry "github.com/pengjunchen/go-agent-core/llm/registry"
)

// ExtensionContext 提供扩展运行时的上下文。
type ExtensionContext struct {
	Context context.Context
	ToolRegistry registry.ToolRegistry
	Provider provider.ModelProvider
}

// Extension 是所有扩展必须实现的接口。
type Extension interface {
	// Name 返回扩展的唯一名称。
	Name() string
	// Priority 返回扩展优先级，数值越高越先被调度。
	Priority() int
	// Activate 在扩展加载时调用。
	Activate(ctx context.Context, api *ExtensionAPI) error
	// Deactivate 在扩展卸载时调用。
	Deactivate(ctx context.Context) error
}

// BaseExtension 提供扩展接口的默认实现。
// 扩展可嵌入 BaseExtension 以获得默认的 Priority() 和 Name() 方法。
type BaseExtension struct{}

// Name 返回空字符串作为默认名称。
func (BaseExtension) Name() string { return "" }

// Priority 返回 0 作为默认优先级。
func (BaseExtension) Priority() int { return 0 }

// Activate 默认实现，无操作。
func (BaseExtension) Activate(_ context.Context, _ *ExtensionAPI) error { return nil }

// Deactivate 默认实现，无操作。
func (BaseExtension) Deactivate(_ context.Context) error { return nil }

// EventType 表示扩展可监听的生命周期事件类型。
type EventType string

const (
	EventAgentStart EventType = "agent_start"
	EventAgentStop EventType = "agent_stop"
	EventTurnStart EventType = "turn_start"
	EventTurnEnd EventType = "turn_end"
	EventToolCallStart EventType = "tool_call_start"
	EventToolCallResult EventType = "tool_call_result"
	EventError EventType = "error"
	// 以下为 Phase 13-2 新增事件类型
	EventBeforeProviderRequest EventType = "before_provider_request"
	EventAfterProviderResponse EventType = "after_provider_response"
	EventMessageStart EventType = "message_start"
	EventMessageEnd EventType = "message_end"
	EventSessionStart EventType = "session_start"
	EventSessionEnd EventType = "session_end"
	EventCompactionStart EventType = "compaction_start"
	EventCompactionEnd EventType = "compaction_end"
)

// Event 是传递给监听器的事件载荷。
type Event struct {
	Type EventType
	SessionID string
	TurnID string
	Payload any
}

// EventAction 表示事件监听器返回的行为动作。
type EventAction int

const (
	// EventActionAllow 允许事件继续传递（默认行为）。
	EventActionAllow EventAction = iota
	// EventActionBlock 阻止事件继续传递（终止后续监听器调用）。
	EventActionBlock
	// EventActionCancel 取消当前操作。
	EventActionCancel
	// EventActionReplace 替换事件载荷。
	EventActionReplace
)

// EventResult 是事件监听器的返回值，可用于修改 Agent 行为。
type EventResult struct {
	Action EventAction
	Reason string
	Replace any // 替换载荷，仅在 Action == EventActionReplace 时有效
}

// EventListener 是事件回调函数。
// 返回 *EventResult 可修改行为（Block/Cancel/Replace），返回 nil 表示允许。
type EventListener func(event Event) *EventResult

// CommandHandler 处理自定义命令。
type CommandHandler func(ctx context.Context, args map[string]any) (any, error)

// priorityListener 将监听器与其优先级关联。
type priorityListener struct {
	listener EventListener
	priority int
}

// ExtensionAPI 提供扩展与 Agent 交互的 API。
type ExtensionAPI struct {
	mu sync.RWMutex
	toolRegistry registry.ToolRegistry
	listeners map[EventType][]priorityListener
	commands map[string]CommandHandler
	providerReg *llmregistry.ProviderRegistry // nil if not available
	currentPriority int // 由 ExtensionRunner 在激活时设置
}

// NewExtensionAPI 创建一个新的 ExtensionAPI。
// providerReg 是可选的 Provider 注册表，用于支持扩展注册自定义 Provider。
func NewExtensionAPI(toolRegistry registry.ToolRegistry, providerReg ...*llmregistry.ProviderRegistry) *ExtensionAPI {
	api := &ExtensionAPI{
		toolRegistry: toolRegistry,
		listeners: make(map[EventType][]priorityListener),
		commands: make(map[string]CommandHandler),
	}
	if len(providerReg) > 0 {
		api.providerReg = providerReg[0]
	}
	return api
}

// RegisterTool 通过扩展 API 注册一个工具。
func (api *ExtensionAPI) RegisterTool(ctx context.Context, tool registry.ToolDefinition) error {
	return api.toolRegistry.RegisterTool(ctx, tool)
}

// WithProviderRegistry 设置 Provider 注册表并返回 API 以支持链式调用。
func (api *ExtensionAPI) WithProviderRegistry(reg *llmregistry.ProviderRegistry) *ExtensionAPI {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.providerReg = reg
	return api
}

// RegisterProvider 注册一个 Provider 工厂。
// 如果未设置 ProviderRegistry，返回错误。
func (api *ExtensionAPI) RegisterProvider(name string, factory llmregistry.ProviderFactory) error {
	api.mu.RLock()
	defer api.mu.RUnlock()
	if api.providerReg == nil {
		return errors.New("provider registry not available")
	}
	api.providerReg.RegisterProvider(name, factory)
	return nil
}

// OnEvent 订阅特定事件类型。
// 监听器的优先级由 setCurrentPriority 设置（由 ExtensionRunner 在激活扩展时调用）。
func (api *ExtensionAPI) OnEvent(eventType EventType, listener EventListener) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.listeners[eventType] = append(api.listeners[eventType], priorityListener{
		listener: listener,
		priority: api.currentPriority,
	})
}

// setCurrentPriority 设置当前扩展的优先级。
// 由 ExtensionRunner 在激活扩展前调用，确保 OnEvent 注册的监听器继承扩展优先级。
func (api *ExtensionAPI) setCurrentPriority(p int) {
	api.currentPriority = p
}

// RegisterCommand 注册一个自定义命令。
func (api *ExtensionAPI) RegisterCommand(name string, handler CommandHandler) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.commands[name] = handler
}

// GetCommand 按名称返回命令处理器。
func (api *ExtensionAPI) GetCommand(name string) (CommandHandler, bool) {
	api.mu.RLock()
	defer api.mu.RUnlock()
	h, ok := api.commands[name]
	return h, ok
}
