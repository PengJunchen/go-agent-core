package subagent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/pengjunchen/go-agent-core/agent/loop"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── Registry 测试用 Mock ──────────────────────────────────────

// stubSubAgent 是用于 registry 测试的简单 SubAgent 实现。
type stubSubAgent struct {
	name string
	closed bool
	closeErr error
}

func (s *stubSubAgent) Name() string { return s.name }
func (s *stubSubAgent) Run(_ context.Context, _ string) error { return nil }
func (s *stubSubAgent) Send(_ context.Context, _ string) error { return nil }
func (s *stubSubAgent) Interrupt(_ context.Context) error { return nil }
func (s *stubSubAgent) Wait(_ context.Context) (*SubAgentResult, error) { return nil, nil }
func (s *stubSubAgent) Events() <-chan SubAgentEvent { return nil }
func (s *stubSubAgent) Close() error {
	s.closed = true
	return s.closeErr
}

// ─── Registry 测试 ─────────────────────────────────────────────

// TestRegistry_RegisterAndList 测试基本注册和列表操作。
func TestRegistry_RegisterAndList(t *testing.T) {
	r := NewSubAgentRegistry()

	a1 := &stubSubAgent{name: "agent-1"}
	a2 := &stubSubAgent{name: "agent-2"}

	if err := r.Register(a1); err != nil {
		t.Fatalf("Register a1: %v", err)
	}
	if err := r.Register(a2); err != nil {
		t.Fatalf("Register a2: %v", err)
	}

	// List 应包含两个名称
	names := r.List()
	if len(names) != 2 {
		t.Fatalf("List() length = %d, want 2", len(names))
	}

	// 按名称获取
	got, ok := r.Get("agent-1")
	if !ok {
		t.Error("Get(agent-1) not found")
	}
	if got.Name() != "agent-1" {
		t.Errorf("Get(agent-1).Name() = %q, want %q", got.Name(), "agent-1")
	}

	// 不存在的名称
	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("Get(nonexistent) should return false")
	}
}

// TestRegistry_DuplicateRegistration 测试重复注册返回错误。
func TestRegistry_DuplicateRegistration(t *testing.T) {
	r := NewSubAgentRegistry()

	a1 := &stubSubAgent{name: "dup"}
	if err := r.Register(a1); err != nil {
		t.Fatalf("Register: %v", err)
	}

	err := r.Register(&stubSubAgent{name: "dup"})
	if err == nil {
		t.Error("expected error for duplicate registration")
	}
}

// TestRegistry_ConcurrentAccess 测试并发访问线程安全。
func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewSubAgentRegistry()

	var wg sync.WaitGroup
	const goroutines = 100

	// 并发注册
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = r.Register(&stubSubAgent{name: fmt.Sprintf("agent-%d", idx)})
		}(i)
	}

	// 并发读取
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, _ = r.Get(fmt.Sprintf("agent-%d", idx))
		}(i)
	}

	// 并发列表
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.List()
		}()
	}

	wg.Wait()

	names := r.List()
	if len(names) != goroutines {
		t.Errorf("List() length = %d, want %d", len(names), goroutines)
	}
}

// TestRegistry_CloseAll 测试关闭所有 SubAgent。
func TestRegistry_CloseAll(t *testing.T) {
	r := NewSubAgentRegistry()

	a1 := &stubSubAgent{name: "a1"}
	a2 := &stubSubAgent{name: "a2"}

	_ = r.Register(a1)
	_ = r.Register(a2)

	err := r.CloseAll()
	if err != nil {
		t.Fatalf("CloseAll: %v", err)
	}

	if !a1.closed {
		t.Error("a1 not closed")
	}
	if !a2.closed {
		t.Error("a2 not closed")
	}

	// 注册表应被清空
	names := r.List()
	if len(names) != 0 {
		t.Errorf("List() after CloseAll = %v, want empty", names)
	}
}

// TestRegistry_CloseAllWithErrors 测试 CloseAll 收集第一个错误。
func TestRegistry_CloseAllWithErrors(t *testing.T) {
	r := NewSubAgentRegistry()

	a1 := &stubSubAgent{name: "a1", closeErr: errors.New("close error")}
	a2 := &stubSubAgent{name: "a2"}

	_ = r.Register(a1)
	_ = r.Register(a2)

	err := r.CloseAll()
	if err == nil {
		t.Error("expected error from CloseAll")
	}

	// 仍然应该关闭所有 agent 并清空注册表
	if !a2.closed {
		t.Error("a2 not closed even though a1 had error")
	}
	names := r.List()
	if len(names) != 0 {
		t.Errorf("List() after CloseAll = %v, want empty", names)
	}
}

// ─── 集成测试：Registry + DefaultSubAgent ───────────────────────

// registryMockProvider 是用于 registry 集成测试的 mock provider。
type registryMockProvider struct{}

func (m *registryMockProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 64)
	go func() {
		defer close(ch)
		ch <- stream.StreamEvent{Type: stream.StreamTextDelta, Content: "hello"}
		ch <- stream.StreamEvent{Type: stream.StreamDone}
	}()
	return ch, nil
}

func (m *registryMockProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{{Type: message.ContentText, Text: "hello"}},
	}, nil
}

func (m *registryMockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "reg-mock", ModelName: "reg-model"}
}

// TestRegistry_IntegrationWithDefaultSubAgent 测试注册真实的 DefaultSubAgent。
func TestRegistry_IntegrationWithDefaultSubAgent(t *testing.T) {
	r := NewSubAgentRegistry()

	p := &registryMockProvider{}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &loop.LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: loop.DefaultMaxTurns,
	}

	agent, err := loop.NewDefaultLoopAgent(cfg)
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	sub := NewDefaultSubAgent("integration-agent", agent)

	err = r.Register(sub)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	// 获取并运行
	got, ok := r.Get("integration-agent")
	if !ok {
		t.Fatal("Get(integration-agent) not found")
	}

	err = got.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	result, err := got.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if result.Name != "integration-agent" {
		t.Errorf("result.Name = %q, want %q", result.Name, "integration-agent")
	}
	if result.Output != "hello" {
		t.Errorf("result.Output = %q, want %q", result.Output, "hello")
	}

	// 关闭所有
	err = r.CloseAll()
	if err != nil {
		t.Fatalf("CloseAll: %v", err)
	}

	// 验证注册表已清空
	if len(r.List()) != 0 {
		t.Error("List() should be empty after CloseAll")
	}
}

// TestRegistry_CloseAllIdempotent 测试 CloseAll 幂等性。
func TestRegistry_CloseAllIdempotent(t *testing.T) {
	r := NewSubAgentRegistry()

	_ = r.Register(&stubSubAgent{name: "a1"})

	err := r.CloseAll()
	if err != nil {
		t.Fatalf("first CloseAll: %v", err)
	}

	// 第二次调用不应 panic
	err = r.CloseAll()
	if err != nil {
		t.Fatalf("second CloseAll: %v", err)
	}
}

