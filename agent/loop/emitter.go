package loop

import (
	"github.com/pengjunchen/go-agent-core/agent/event"
)

// EventEmitter 解耦事件发射与具体通道所有权。
// 多个 UI（WebSocket、SSE、CLI 等）可通过不同的 EventEmitter 实现
// 消费同一个 Agent 的事件流，而无需直接持有 channel。
type EventEmitter interface {
	// Emit 发射一个 Agent 事件。
	// 实现应保证非阻塞：若消费者无法及时处理，可丢弃事件而非阻塞调用方。
	Emit(evt event.AgentEvent)
}

// ChannelEmitter 将 EventEmitter 适配到 chan<- event.AgentEvent。
// 通道满时丢弃事件（非阻塞），防止慢消费者阻塞 Agent 主循环。
type ChannelEmitter struct {
	ch chan<- event.AgentEvent
}

// NewChannelEmitter 从发送端通道创建 ChannelEmitter。
func NewChannelEmitter(ch chan<- event.AgentEvent) *ChannelEmitter {
	return &ChannelEmitter{ch: ch}
}

// Emit 实现 EventEmitter 接口。
// 通道满时静默丢弃事件。
func (c *ChannelEmitter) Emit(evt event.AgentEvent) {
	if c.ch == nil {
		return
	}
	select {
	case c.ch <- evt:
	default:
		// 通道满，丢弃事件
	}
}

// MultiEmitter 将事件扇出（fan-out）到多个 EventEmitter。
// 典型场景：同一个 Agent 事件需要同时推送给 WebSocket 客户端和 CLI 日志。
type MultiEmitter struct {
	emitters []EventEmitter
}

// NewMultiEmitter 创建一个扇出发射器。
// 传入零个 emitter 时，Emit 为 no-op。
func NewMultiEmitter(emitters ...EventEmitter) *MultiEmitter {
	return &MultiEmitter{emitters: emitters}
}

// Emit 实现 EventEmitter 接口。
// 按顺序调用所有子发射器；某个子发射器阻塞不影响其他子发射器
// （前提是子发射器的 Emit 本身非阻塞，如 ChannelEmitter）。
func (m *MultiEmitter) Emit(evt event.AgentEvent) {
	for _, e := range m.emitters {
		e.Emit(evt)
	}
}
