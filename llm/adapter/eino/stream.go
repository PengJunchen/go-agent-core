// Package eino 提供基于 Eino 框架的 ModelProvider 适配器。
package eino

import (
	"io"
	"sync"

	"github.com/cloudwego/eino/schema"

	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// EinoEventStream 适配 Eino 的 StreamReader 到 stream.EventStream 接口。
//
// Deprecated: EinoProvider.StreamChat 已改用 pumpStream（直接返回
// <-chan StreamEvent），EinoEventStream 不再被生产代码引用。
// 保留此类型仅供测试和向后兼容，未来版本可能移除。
//
// 从 StreamReader 读取 []*schema.Message 并填充内部 buffer，逐条通过
// Next() 或 Events() 通道消费。流结束后 Close() 释放底层资源。
type EinoEventStream struct {
	reader *schema.StreamReader[*schema.Message]
	events chan stream.StreamEvent
	err error
	done chan struct{}
	closeOnce sync.Once
}

// NewEinoEventStream 构造 EinoEventStream。
// Deprecated: 见 EinoEventStream 文档。
func NewEinoEventStream(reader *schema.StreamReader[*schema.Message]) *EinoEventStream {
	es := &EinoEventStream{
		reader: reader,
		events: make(chan stream.StreamEvent, 64),
		done: make(chan struct{}),
	}
	go es.pump()
	return es
}

// pump 从底层 reader 拉取数据并发送到 events channel。
func (es *EinoEventStream) pump() {
	defer close(es.events)
	defer close(es.done)

	for {
		msg, err := es.reader.Recv()
		if err != nil {
			if err == io.EOF {
				es.events <- stream.StreamEvent{Type: stream.StreamDone}
				return
			}
			es.err = err
			es.events <- stream.StreamEvent{Type: stream.StreamError, Error: err}
			return
		}
		if msg == nil {
			continue
		}

		emitEvents(es.events, msg)
	}
}

// Next 阻塞拉取下一个流事件。ok==false 表示流已结束。
func (es *EinoEventStream) Next() (stream.StreamEvent, bool) {
	evt, ok := <-es.events
	return evt, ok
}

// Events 返回底层只读事件通道。
func (es *EinoEventStream) Events() <-chan stream.StreamEvent {
	return es.events
}

// Err 返回流处理过程中遇到的错误（如有）。
func (es *EinoEventStream) Err() error {
	return es.err
}

// Close 释放底层 StreamReader 资源。
func (es *EinoEventStream) Close() error {
	es.closeOnce.Do(func() {
		es.reader.Close()
	})
	return nil
}

// compile-time interface check
var _ stream.EventStream = (*EinoEventStream)(nil)
