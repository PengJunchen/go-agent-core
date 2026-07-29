// Package stream 的 BoundedEventChannel 测试。
package stream

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBoundedEventChannel_SendRecv 验证基本发送接收。
func TestBoundedEventChannel_SendRecv(t *testing.T) {
	ch := NewBoundedEventChannel(10, DropOldest)
	defer func() { _ = ch.Close() }()

	if !ch.Send(StreamEvent{Type: StreamTextDelta, Content: "hello"}) {
		t.Fatal("Send should succeed")
	}
	e, ok := ch.Next()
	if !ok {
		t.Fatal("Next should return event")
	}
	if e.Content != "hello" {
		t.Errorf("expected hello, got %s", e.Content)
	}
}

// TestBoundedEventChannel_DropOldest 验证 DropOldest 策略。
func TestBoundedEventChannel_DropOldest(t *testing.T) {
	ch := NewBoundedEventChannel(2, DropOldest)
	defer func() { _ = ch.Close() }()

	ch.Send(StreamEvent{Type: StreamTextDelta})
	ch.Send(StreamEvent{Type: StreamThinkingDelta})
	// 通道满了，第三个应丢弃最旧的
	ch.Send(StreamEvent{Type: StreamToolCallStart})

	if ch.Dropped() == 0 {
		t.Error("expected at least 1 dropped event")
	}
}

// TestBoundedEventChannel_DropNewest 验证 DropNewest 策略。
func TestBoundedEventChannel_DropNewest(t *testing.T) {
	ch := NewBoundedEventChannel(2, DropNewest)
	defer func() { _ = ch.Close() }()

	ch.Send(StreamEvent{Type: StreamTextDelta})
	ch.Send(StreamEvent{Type: StreamThinkingDelta})
	// 满了，丢弃最新的
	if ch.Send(StreamEvent{Type: StreamToolCallStart}) {
		t.Error("Send should fail with DropNewest when full")
	}
	if ch.Dropped() == 0 {
		t.Error("expected at least 1 dropped event")
	}
}

// TestBoundedEventChannel_BlockUntilConsumed 验证阻塞策略。
func TestBoundedEventChannel_BlockUntilConsumed(t *testing.T) {
	ch := NewBoundedEventChannel(1, BlockUntilConsumed)

	ch.Send(StreamEvent{Type: StreamTextDelta})
	// 通道满了，下一次 Send 应阻塞

	var sent atomic.Bool
	go func() {
		ch.Send(StreamEvent{Type: StreamThinkingDelta})
		sent.Store(true)
	}()

	time.Sleep(50 * time.Millisecond)
	if sent.Load() {
		t.Error("Send should be blocked")
	}

	// 消费一个事件
	_, _ = ch.Next()
	time.Sleep(50 * time.Millisecond)
	if !sent.Load() {
		t.Error("Send should have completed after consumer took event")
	}
	_ = ch.Close()
}

// TestBoundedEventChannel_Close 验证关闭行为。
func TestBoundedEventChannel_Close(t *testing.T) {
	ch := NewBoundedEventChannel(10, DropOldest)

	if err := ch.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	// 双重关闭安全
	if err := ch.Close(); err != nil {
		t.Errorf("double Close returned error: %v", err)
	}
	// 关闭后 Send 失败
	if ch.Send(StreamEvent{Type: StreamError}) {
		t.Error("Send after close should fail")
	}
}

// TestBoundedEventChannel_DefaultSize 验证默认容量。
func TestBoundedEventChannel_DefaultSize(t *testing.T) {
	ch := NewBoundedEventChannel(0, DropOldest) // 0 → 256
	defer func() { _ = ch.Close() }()
	if ch.Cap() != 256 {
		t.Errorf("expected default cap 256, got %d", ch.Cap())
	}
}

// TestBoundedEventChannel_ConcurrentSend 验证多 goroutine 并发发送的线程安全。
func TestBoundedEventChannel_ConcurrentSend(t *testing.T) {
	ch := NewBoundedEventChannel(64, DropOldest)
	defer func() { _ = ch.Close() }()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				ch.Send(StreamEvent{Type: StreamTextDelta, Content: "x"})
			}
		}()
	}
	wg.Wait()

	// 消费前关闭通道，否则 Next 永远阻塞
	_ = ch.Close()

	// 消费所有事件，不应 panic 或死锁
	count := 0
	for {
		_, ok := ch.Next()
		if !ok {
			break
		}
		count++
		if count > 10000 {
			t.Fatal("too many events")
		}
	}
	// DropOldest 策略下，丢弃数 = 发送数 - 消费数
	if ch.Dropped()+int64(count) != 1000 {
		t.Errorf("dropped(%d) + consumed(%d) != sent(1000)", ch.Dropped(), count)
	}
}

// TestBoundedEventChannel_SetErr 验证 SetErr 关闭通道。
func TestBoundedEventChannel_SetErr(t *testing.T) {
	ch := NewBoundedEventChannel(10, DropOldest)
	err := fmt.Errorf("test error")
	ch.SetErr(err)
	if ch.Err() == nil {
		t.Error("expected error to be set")
	}
	// SetErr 应关闭通道
	if ch.Send(StreamEvent{Type: StreamTextDelta}) {
		t.Error("Send after SetErr should fail")
	}
}
