package loop

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
)

// ─── ChannelEmitter Tests ────────────────────────────────────────

// ED-001: ApprovalHook 使用 EventEmitter（而非直接通道）发射事件。
// 验证 ApprovalHook 的构造函数接受 EventEmitter 接口，
// 且事件通过 emitter.Emit 发出。
func TestEventEmitter_ApprovalHookUsesEmitter(t *testing.T) {
	handler := &mockApprovalHandler{decision: ApprovalApprove}
	hitl := NewHITLManager(handler, 0)

	// 使用自定义 emitter 捕获事件，证明 ApprovalHook 走的是 EventEmitter 接口
	captured := make(chan event.AgentEvent, 1)
	emitter := &captureEmitter{ch: captured}

	hook := NewApprovalHook(hitl, emitter, "sub-x", "sess-x", "turn-x")

	call := &toolhook.ToolCall{
		ID: "tc-emit",
		Name: "emit_tool",
		Arguments: map[string]any{},
		SessionID: "sess-x",
		TurnID: "turn-x",
	}

	if _, err := hook.Before(context.Background(), call); err != nil {
		t.Fatalf("Before: %v", err)
	}

	select {
	case evt := <-captured:
		if evt.Type != event.EventApprovalRequest {
			t.Errorf("event type = %v, want EventApprovalRequest", evt.Type)
		}
		if evt.SubmissionID != "sub-x" {
			t.Errorf("submissionID = %q, want %q", evt.SubmissionID, "sub-x")
		}
	case <-time.After(time.Second):
		t.Error("no event captured via EventEmitter")
	}
}

// captureEmitter 是测试用 EventEmitter，将事件转发到 channel 供断言。
type captureEmitter struct {
	ch chan<- event.AgentEvent
}

func (c *captureEmitter) Emit(evt event.AgentEvent) {
	select {
	case c.ch <- evt:
	default:
	}
}

// TestChannelEmitter_DeliversEvent 验证 ChannelEmitter 将事件写入通道。
func TestChannelEmitter_DeliversEvent(t *testing.T) {
	ch := make(chan event.AgentEvent, 1)
	emitter := NewChannelEmitter(ch)

	evt := event.AgentEvent{Type: event.EventCompleted, SubmissionID: "s1"}
	emitter.Emit(evt)

	select {
	case got := <-ch:
		if got.Type != event.EventCompleted {
			t.Errorf("type = %v, want EventCompleted", got.Type)
		}
		if got.SubmissionID != "s1" {
			t.Errorf("submissionID = %q, want %q", got.SubmissionID, "s1")
		}
	default:
		t.Error("event not delivered")
	}
}

// TestChannelEmitter_DropsWhenFull 验证通道满时 ChannelEmitter 非阻塞地丢弃事件。
func TestChannelEmitter_DropsWhenFull(t *testing.T) {
	ch := make(chan event.AgentEvent, 1)
	emitter := NewChannelEmitter(ch)

	// 填满通道
	emitter.Emit(event.AgentEvent{Type: event.EventTurnStart})

	// 再发一个，应被丢弃而非阻塞
	done := make(chan struct{})
	go func() {
		emitter.Emit(event.AgentEvent{Type: event.EventTurnEnd})
		close(done)
	}()

	select {
	case <-done:
		// good: non-blocking
	case <-time.After(time.Second):
		t.Fatal("Emit blocked when channel full; should drop instead")
	}

	// 通道里应该只有第一个事件
	if len(ch) != 1 {
		t.Errorf("channel len = %d, want 1 (second event should be dropped)", len(ch))
	}
}

// TestChannelEmitter_NilChannelSafe 验证 nil 通道时 Emit 不 panic。
func TestChannelEmitter_NilChannelSafe(t *testing.T) {
	emitter := NewChannelEmitter(nil)
	// 不应 panic
	emitter.Emit(event.AgentEvent{Type: event.EventCompleted})
}

// TestChannelEmitter_ImplementsInterface 编译期契约：ChannelEmitter 实现 EventEmitter。
func TestChannelEmitter_ImplementsInterface(t *testing.T) {
	var _ EventEmitter = (*ChannelEmitter)(nil)
}

// ─── MultiEmitter Tests ──────────────────────────────────────────

// ED-002: 多个 emitter 可以通过 MultiEmitter 消费同一事件。
func TestMultiEmitter_FansOutToAll(t *testing.T) {
	var mu sync.Mutex
	var gotA, gotB []event.AgentEvent

	emA := &recordEmitter{mu: &mu, events: &gotA}
	emB := &recordEmitter{mu: &mu, events: &gotB}

	multi := NewMultiEmitter(emA, emB)

	evt1 := event.AgentEvent{Type: event.EventTurnStart, SubmissionID: "s1"}
	evt2 := event.AgentEvent{Type: event.EventTurnEnd, SubmissionID: "s1"}

	multi.Emit(evt1)
	multi.Emit(evt2)

	mu.Lock()
	defer mu.Unlock()

	if len(gotA) != 2 {
		t.Errorf("emitter A received %d events, want 2", len(gotA))
	}
	if len(gotB) != 2 {
		t.Errorf("emitter B received %d events, want 2", len(gotB))
	}
	if len(gotA) > 0 && gotA[0].Type != event.EventTurnStart {
		t.Errorf("emitter A[0] type = %v, want EventTurnStart", gotA[0].Type)
	}
	if len(gotB) > 1 && gotB[1].Type != event.EventTurnEnd {
		t.Errorf("emitter B[1] type = %v, want EventTurnEnd", gotB[1].Type)
	}
}

// TestMultiEmitter_EmptyIsNoOp 验证无子发射器时 Emit 为 no-op。
func TestMultiEmitter_EmptyIsNoOp(t *testing.T) {
	multi := NewMultiEmitter()
	// 不应 panic
	multi.Emit(event.AgentEvent{Type: event.EventCompleted})
}

// TestMultiEmitter_ImplementsInterface 编译期契约：MultiEmitter 实现 EventEmitter。
func TestMultiEmitter_ImplementsInterface(t *testing.T) {
	var _ EventEmitter = (*MultiEmitter)(nil)
}

// TestMultiEmitter_WithChannelEmitters 验证 MultiEmitter 组合 ChannelEmitter 的端到端行为。
func TestMultiEmitter_WithChannelEmitters(t *testing.T) {
	chA := make(chan event.AgentEvent, 2)
	chB := make(chan event.AgentEvent, 2)

	multi := NewMultiEmitter(NewChannelEmitter(chA), NewChannelEmitter(chB))

	multi.Emit(event.AgentEvent{Type: event.EventCompleted, SubmissionID: "s1"})

	// 两个通道都应收到
	for name, ch := range map[string]chan event.AgentEvent{"A": chA, "B": chB} {
		select {
		case evt := <-ch:
			if evt.Type != event.EventCompleted {
				t.Errorf("channel %s type = %v, want EventCompleted", name, evt.Type)
			}
		case <-time.After(time.Second):
			t.Errorf("channel %s did not receive event", name)
		}
	}
}

// recordEmitter 是测试用 EventEmitter，记录所有收到的事件。
type recordEmitter struct {
	mu *sync.Mutex
	events *[]event.AgentEvent
}

func (r *recordEmitter) Emit(evt event.AgentEvent) {
	r.mu.Lock()
	*r.events = append(*r.events, evt)
	r.mu.Unlock()
}
