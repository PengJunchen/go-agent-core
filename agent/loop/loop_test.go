package loop

import (
	"context"
	"testing"

	"github.com/pengjunchen/go-agent-core/agent/event"
)

// mockLoopAgent 实现LoopAgent接口，用于编译契约验证。
type mockLoopAgent struct {
	status event.AgentStatus
	closed bool
}

func (m *mockLoopAgent) Query(ctx context.Context, input AgentInput) (<-chan event.AgentEvent, error) {
	ch := make(chan event.AgentEvent, 1)
	ch <- event.AgentEvent{Type: event.EventCompleted}
	close(ch)
	return ch, nil
}

func (m *mockLoopAgent) Interrupt(ctx context.Context) error { return nil }

func (m *mockLoopAgent) Steer(ctx context.Context, message string) error { return nil }

func (m *mockLoopAgent) FollowUp(ctx context.Context, content string) error { return nil }

func (m *mockLoopAgent) Status() event.AgentStatus { return m.status }

func (m *mockLoopAgent) Close() error {
	m.closed = true
	return nil
}

// Interface-001: LoopAgent 接口可被 mock 实现。
func TestLoopAgent_InterfaceContract(t *testing.T) {
	var _ LoopAgent = (*mockLoopAgent)(nil)
}

// VT-001: Query 返回事件通道。
func TestLoopAgent_Query(t *testing.T) {
	agent := &mockLoopAgent{status: event.StatusIdle}
	ch, err := agent.Query(context.Background(), AgentInput{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	evt, ok := <-ch
	if !ok {
		t.Fatal("event channel closed unexpectedly")
	}
	if evt.Type != event.EventCompleted {
		t.Errorf("event type = %v, want EventCompleted", evt.Type)
	}
}

// VT-002: Status 返回当前状态。
func TestLoopAgent_Status(t *testing.T) {
	agent := &mockLoopAgent{status: event.StatusRunning}
	if got := agent.Status(); got != event.StatusRunning {
		t.Errorf("Status() = %v, want %v", got, event.StatusRunning)
	}
}

// VT-003: Close 释放资源。
func TestLoopAgent_Close(t *testing.T) {
	agent := &mockLoopAgent{}
	if err := agent.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !agent.closed {
		t.Error("Close did not mark agent as closed")
	}
}
