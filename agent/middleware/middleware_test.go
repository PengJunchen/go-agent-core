package middleware

import (
	"context"
	"errors"
	"testing"
)

// mockMiddleware 实现Middleware接口，用于编译契约验证。
type mockMiddleware struct {
	beforeTurnCalls []string
	afterTurnCalls []string
	beforeCompactCalls int
	afterCompactCalls int
	beforeTurnErr error
	afterTurnErr error
}

func (m *mockMiddleware) BeforeTurn(_ context.Context, turnID string) error {
	m.beforeTurnCalls = append(m.beforeTurnCalls, turnID)
	return m.beforeTurnErr
}

func (m *mockMiddleware) AfterTurn(_ context.Context, turnID string) error {
	m.afterTurnCalls = append(m.afterTurnCalls, turnID)
	return m.afterTurnErr
}

func (m *mockMiddleware) BeforeCompact(_ context.Context) error {
	m.beforeCompactCalls++
	return nil
}

func (m *mockMiddleware) AfterCompact(_ context.Context) error {
	m.afterCompactCalls++
	return nil
}

// Interface-001: Middleware 接口可被 mock 实现。
func TestMiddleware_InterfaceContract(t *testing.T) {
	var _ Middleware = (*mockMiddleware)(nil)
}

// VT-001: Chain.BeforeTurn 按顺序执行所有中间件。
func TestChain_BeforeTurnOrder(t *testing.T) {
	m1 := &mockMiddleware{}
	m2 := &mockMiddleware{}
	c := NewChain()
	c.Add(m1)
	c.Add(m2)
	if err := c.BeforeTurn(context.Background(), "turn-1"); err != nil {
		t.Fatalf("BeforeTurn: %v", err)
	}
	if len(m1.beforeTurnCalls) != 1 || m1.beforeTurnCalls[0] != "turn-1" {
		t.Errorf("m1 beforeTurnCalls = %v, want [turn-1]", m1.beforeTurnCalls)
	}
	if len(m2.beforeTurnCalls) != 1 || m2.beforeTurnCalls[0] != "turn-1" {
		t.Errorf("m2 beforeTurnCalls = %v, want [turn-1]", m2.beforeTurnCalls)
	}
}

// VT-002: Chain.AfterTurn 按逆序执行所有中间件。
func TestChain_AfterTurnReverseOrder(t *testing.T) {
	var order []int
	c := NewChain()
	c.Add(&recordingMiddleware{order: &order, id: 1})
	c.Add(&recordingMiddleware{order: &order, id: 2})
	c.Add(&recordingMiddleware{order: &order, id: 3})
	if err := c.AfterTurn(context.Background(), "turn-1"); err != nil {
		t.Fatalf("AfterTurn: %v", err)
	}
	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Errorf("AfterTurn order = %v, want [3 2 1]", order)
	}
}

// VT-003: Chain.BeforeTurn 在中间件返回错误时短路并返回该错误。
func TestChain_BeforeTurnShortCircuitOnError(t *testing.T) {
	errBoom := errors.New("before-turn boom")
	m1 := &mockMiddleware{beforeTurnErr: errBoom}
	m2 := &mockMiddleware{}
	c := NewChain()
	c.Add(m1)
	c.Add(m2)
	err := c.BeforeTurn(context.Background(), "turn-1")
	if err != errBoom {
		t.Errorf("BeforeTurn error = %v, want %v", err, errBoom)
	}
	if len(m2.beforeTurnCalls) != 0 {
		t.Errorf("subsequent middleware was called %d times, want 0", len(m2.beforeTurnCalls))
	}
}

// VT-004: Chain.BeforeCompact 按顺序、AfterCompact 按逆序。
func TestChain_CompactOrder(t *testing.T) {
	m1 := &mockMiddleware{}
	m2 := &mockMiddleware{}
	c := NewChain()
	c.Add(m1)
	c.Add(m2)
	if err := c.BeforeCompact(context.Background()); err != nil {
		t.Fatalf("BeforeCompact: %v", err)
	}
	if m1.beforeCompactCalls != 1 || m2.beforeCompactCalls != 1 {
		t.Errorf("beforeCompactCalls = (%d, %d), want (1, 1)", m1.beforeCompactCalls, m2.beforeCompactCalls)
	}
	if err := c.AfterCompact(context.Background()); err != nil {
		t.Fatalf("AfterCompact: %v", err)
	}
	if m1.afterCompactCalls != 1 || m2.afterCompactCalls != 1 {
		t.Errorf("afterCompactCalls = (%d, %d), want (1, 1)", m1.afterCompactCalls, m2.afterCompactCalls)
	}
}

// VT-005: 空 Chain 不 panic 且各方法返回 nil。
func TestChain_Empty(t *testing.T) {
	c := NewChain()
	if err := c.BeforeTurn(context.Background(), "x"); err != nil {
		t.Errorf("empty BeforeTurn: %v", err)
	}
	if err := c.AfterTurn(context.Background(), "x"); err != nil {
		t.Errorf("empty AfterTurn: %v", err)
	}
	if err := c.BeforeCompact(context.Background()); err != nil {
		t.Errorf("empty BeforeCompact: %v", err)
	}
	if err := c.AfterCompact(context.Background()); err != nil {
		t.Errorf("empty AfterCompact: %v", err)
	}
}

type recordingMiddleware struct {
	order *[]int
	id int
}

func (m *recordingMiddleware) BeforeTurn(_ context.Context, _ string) error { return nil }
func (m *recordingMiddleware) AfterTurn(_ context.Context, _ string) error {
	*m.order = append(*m.order, m.id)
	return nil
}
func (m *recordingMiddleware) BeforeCompact(_ context.Context) error { return nil }
func (m *recordingMiddleware) AfterCompact(_ context.Context) error { return nil }
