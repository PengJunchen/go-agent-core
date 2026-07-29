// Package stream 定义 LLM 流式响应的事件类型。
//
// StreamEvent 由 ModelProvider.StreamChat 产出，经 agent 引擎消费后
// 转换为 AgentEvent。事件类型
// 文本增量、思维增量、工具调用开始/结果、完成、错误。
package stream

// StreamEventType 标识流式事件的种类。
type StreamEventType int

const (
	// StreamTextDelta 文本增量。
	StreamTextDelta StreamEventType = iota
	// StreamThinkingDelta 思维增量。
	StreamThinkingDelta
	// StreamToolCallStart 工具调用开始。
	StreamToolCallStart
	// StreamToolCallResult 工具调用结果。
	StreamToolCallResult
	// StreamDone 流结束。
	StreamDone
	// StreamError 流错误。
	StreamError
)

// StreamEvent 是流中的一个事件。
type StreamEvent struct {
	Type StreamEventType
	Content string // StreamTextDelta 时为文本增量
	Thinking string // StreamThinkingDelta 时为思维增量
	ToolCall *ToolCall // StreamToolCall* 时非 nil
	Error error // StreamError 时非 nil
}

// ToolCall 描述一次模型发起的工具调用。
type ToolCall struct {
	ID string
	Name string
	Arguments map[string]any
}

// EventStream 是消费流式事件的抽象接口。
//
// 默认实现为带背压控制的 BoundedEventStream。Next 阻塞拉取下一个事件，
// ok==false 表示流结束。Close 释放底层资源。
type EventStream interface {
	Next() (StreamEvent, bool)
	Events() <-chan StreamEvent
	Err() error
	Close() error
}
