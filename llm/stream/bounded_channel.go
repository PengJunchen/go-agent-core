// Package stream 的 BoundedEventChannel 实现。
//
// 带背压控制的事件流通道， + 参考文档。
// 解决 UnboundedEventChannel 无背压风险：
// - maxSize: 通道最大容量
// - DropPolicy: 满时的处理策略（DropOldest/DropNewest/BlockUntilConsumed）
//
// 监控指标：event_buffer_size / event_dropped_total
package stream

import (
	"sync"
	"sync/atomic"
)

// DropPolicy 枚举背压策略。
type DropPolicy int

const (
	// DropOldest 丢弃最旧的事件（先进先出）。
	DropOldest DropPolicy = iota
	// DropNewest 丢弃最新的事件。
	DropNewest
	// BlockUntilConsumed 阻塞直到消费者取走事件。
	BlockUntilConsumed
)

// BoundedEventChannel 是带背压控制的事件流通道。
//
// 默认 maxSize=256, DropPolicy=DropOldest。
// 适用于高频流式事件场景，避免无界通道导致内存溢出。
type BoundedEventChannel struct {
	maxSize int
	policy DropPolicy
	ch chan StreamEvent

	mu sync.Mutex
	closed bool
	dropped atomic.Int64 // 累计丢弃数
	err error
}

// NewBoundedEventChannel 创建一个有界事件通道。
func NewBoundedEventChannel(maxSize int, policy DropPolicy) *BoundedEventChannel {
	if maxSize <= 0 {
		maxSize = 256
	}
	return &BoundedEventChannel{
		maxSize: maxSize,
		policy: policy,
		ch: make(chan StreamEvent, maxSize),
	}
}

// Send 尝试发送一个事件。根据 DropPolicy 决定满时行为。
func (b *BoundedEventChannel) Send(e StreamEvent) bool {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return false
	}
	b.mu.Unlock()

	switch b.policy {
	case DropOldest:
		select {
		case b.ch <- e:
			return true
		default:
			// 满了，丢弃最旧的
			select {
			case <-b.ch:
			default:
			}
			b.dropped.Add(1)
			select {
			case b.ch <- e:
				return true
			default:
				b.dropped.Add(1)
				return false
			}
		}
	case DropNewest:
		select {
		case b.ch <- e:
			return true
		default:
			b.dropped.Add(1)
			return false
		}
	case BlockUntilConsumed:
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return false
		}
		b.ch <- e // 阻塞直到有空间
		return true
	default:
		select {
		case b.ch <- e:
			return true
		default:
			b.dropped.Add(1)
			return false
		}
	}
}

// Next 拉取下一个事件（实现 EventStream 接口）。
func (b *BoundedEventChannel) Next() (StreamEvent, bool) {
	e, ok := <-b.ch
	return e, ok
}

// Events 返回事件通道（实现 EventStream 接口）。
func (b *BoundedEventChannel) Events() <-chan StreamEvent {
	return b.ch
}

// Err 返回错误（实现 EventStream 接口）。
func (b *BoundedEventChannel) Err() error {
	return b.err
}

// Close 关闭通道（实现 EventStream 接口）。
func (b *BoundedEventChannel) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	close(b.ch)
	return nil
}

// SetErr 设置错误并关闭通道。
func (b *BoundedEventChannel) SetErr(err error) {
	b.err = err
	_ = b.Close()
}

// Dropped 返回累计丢弃的事件数。
func (b *BoundedEventChannel) Dropped() int64 {
	return b.dropped.Load()
}

// Len 返回当前缓冲区中的事件数。
func (b *BoundedEventChannel) Len() int {
	return len(b.ch)
}

// Cap 返回通道容量。
func (b *BoundedEventChannel) Cap() int {
	return b.maxSize
}

// 编译期检查：BoundedEventChannel 实现 EventStream 接口。
var _ EventStream = (*BoundedEventChannel)(nil)
