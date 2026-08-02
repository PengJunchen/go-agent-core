package provider

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// mockProvider is a minimal ModelProvider for lazy loading tests.
type mockProvider struct {
	info *ModelInfo
}

func (m *mockProvider) StreamChat(_ context.Context, _ []message.Message, _ *ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, nil
}
func (m *mockProvider) Generate(_ context.Context, _ []message.Message, _ *ChatOptions) (*message.Message, error) {
	return &message.Message{Role: message.RoleAssistant}, nil
}
func (m *mockProvider) ModelInfo() *ModelInfo { return m.info }

// LT-001: Factory not called until first use.
func TestLazyProvider_FactoryNotCalledUntilUse(t *testing.T) {
	var calls int32
	factory := func() (ModelProvider, error) {
		atomic.AddInt32(&calls, 1)
		return &mockProvider{info: &ModelInfo{Provider: "mock", ModelName: "m1"}}, nil
	}

	lp := NewLazyProvider(factory)
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("factory calls before use = %d, want 0", got)
	}

	// First use triggers factory
	_ = lp.ModelInfo()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("factory calls after first use = %d, want 1", got)
	}
}

// LT-002: Factory called exactly once (sync.Once).
func TestLazyProvider_FactoryCalledExactlyOnce(t *testing.T) {
	var calls int32
	factory := func() (ModelProvider, error) {
		atomic.AddInt32(&calls, 1)
		return &mockProvider{info: &ModelInfo{Provider: "mock", ModelName: "m1"}}, nil
	}

	lp := NewLazyProvider(factory)

	// Multiple uses
	_ = lp.ModelInfo()
	_, _ = lp.Generate(context.Background(), nil, nil)
	_, _ = lp.StreamChat(context.Background(), nil, nil)
	_ = lp.ModelInfo()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("factory calls after multiple uses = %d, want 1", got)
	}
}

// LT-003: Factory error propagated.
func TestLazyProvider_FactoryErrorPropagated(t *testing.T) {
	wantErr := errors.New("factory boom")
	factory := func() (ModelProvider, error) {
		return nil, wantErr
	}

	lp := NewLazyProvider(factory)

	_, err := lp.Generate(context.Background(), nil, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("Generate() err = %v, want %v", err, wantErr)
	}

	_, err = lp.StreamChat(context.Background(), nil, nil)
	if !errors.Is(err, wantErr) {
		t.Errorf("StreamChat() err = %v, want %v", err, wantErr)
	}

	info := lp.ModelInfo()
	if info == nil {
		t.Fatal("ModelInfo() = nil, want non-nil placeholder")
	}
	if info.Provider != "lazy" || info.ModelName != "uninitialized" {
		t.Errorf("ModelInfo() = {Provider: %q, ModelName: %q}, want {lazy, uninitialized}", info.Provider, info.ModelName)
	}
}

// LT-004: Concurrent calls only invoke factory once.
func TestLazyProvider_ConcurrentCallsInvokeFactoryOnce(t *testing.T) {
	var calls int32
	factory := func() (ModelProvider, error) {
		atomic.AddInt32(&calls, 1)
		// Simulate slow init to maximize chance of race
		// (no sleep needed — sync.Once guarantees correctness, but
		// a brief pause makes violations detectable)
		return &mockProvider{info: &ModelInfo{Provider: "mock", ModelName: "m1"}}, nil
	}

	lp := NewLazyProvider(factory)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			_ = lp.ModelInfo()
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("factory calls under concurrency = %d, want 1", got)
	}
}

// LT-005: LazyProvider implements ModelProvider interface.
func TestLazyProvider_ImplementsModelProvider(t *testing.T) {
	var _ ModelProvider = (*LazyProvider)(nil)
}

// LT-006: LazyProvider delegates to underlying provider after init.
func TestLazyProvider_DelegatesAfterInit(t *testing.T) {
	wantInfo := &ModelInfo{Provider: "mock", ModelName: "delegated"}
	lp := NewLazyProvider(func() (ModelProvider, error) {
		return &mockProvider{info: wantInfo}, nil
	})

	got := lp.ModelInfo()
	if got != wantInfo {
		t.Errorf("ModelInfo() = %+v, want %+v", got, wantInfo)
	}

	msg, err := lp.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Generate() err = %v, want nil", err)
	}
	if msg == nil {
		t.Error("Generate() = nil, want non-nil message")
	}
}
