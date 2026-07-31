package registry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// PE-001: 串行模式逐个执行工具调用。
func TestParallelToolExecutor_Sequential(t *testing.T) {
	reg := NewDefaultToolRegistry()
	var order []string
	var mu sync.Mutex

	for _, name := range []string{"a", "b", "c"} {
		name := name
		_ = reg.RegisterTool(context.Background(), ToolDefinition{
			Name: name,
			Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				mu.Lock()
				order = append(order, name)
				mu.Unlock()
				return &ToolResult{Content: name}, nil
			},
		})
	}

	exec := NewParallelToolExecutor(ExecutionSequential, 0)
	calls := []ToolCall{
		{Name: "a", ID: "1"},
		{Name: "b", ID: "2"},
		{Name: "c", ID: "3"},
	}

	results := exec.ExecuteTools(context.Background(), calls, reg)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result[%d]: unexpected error: %v", i, r.Error)
		}
		if r.Result.Content != calls[i].Name {
			t.Errorf("result[%d].Content = %q, want %q", i, r.Result.Content, calls[i].Name)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("execution order = %v, want [a b c]", order)
	}
}

// PE-002: 并行模式并发执行 ParallelSafe 工具。
func TestParallelToolExecutor_Parallel(t *testing.T) {
	reg := NewDefaultToolRegistry()
	var running atomic.Int32

	for _, name := range []string{"p1", "p2", "p3"} {
		name := name
		_ = reg.RegisterTool(context.Background(), ToolDefinition{
			Name: name,
			ParallelSafe: true,
			Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				cur := running.Add(1)
				if cur > 1 {
					// 至少有两个同时在运行，说明并发执行
				}
				time.Sleep(50 * time.Millisecond)
				running.Add(-1)
				return &ToolResult{Content: name}, nil
			},
		})
	}

	exec := NewParallelToolExecutor(ExecutionParallel, 0)
	calls := []ToolCall{
		{Name: "p1", ID: "1"},
		{Name: "p2", ID: "2"},
		{Name: "p3", ID: "3"},
	}

	start := time.Now()
	results := exec.ExecuteTools(context.Background(), calls, reg)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result[%d]: unexpected error: %v", i, r.Error)
		}
	}

	// 并行执行应该比串行快（3 * 50ms = 150ms vs ~50ms）
	if elapsed > 120*time.Millisecond {
		t.Errorf("parallel execution took %v, expected < 120ms", elapsed)
	}
}

// PE-003: 并行模式尊重最大并发数限制。
func TestParallelToolExecutor_ParallelWithMaxConcurrent(t *testing.T) {
	reg := NewDefaultToolRegistry()
	var running atomic.Int32
	var maxRunning atomic.Int32

	for i := range 5 {
		name := string(rune('a' + i))
		_ = reg.RegisterTool(context.Background(), ToolDefinition{
			Name: name,
			ParallelSafe: true,
			Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				cur := running.Add(1)
				for {
					old := maxRunning.Load()
					if cur <= old || maxRunning.CompareAndSwap(old, cur) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				running.Add(-1)
				return &ToolResult{Content: name}, nil
			},
		})
	}

	exec := NewParallelToolExecutor(ExecutionParallel, 2)
	calls := make([]ToolCall, 5)
	for i := range calls {
		calls[i] = ToolCall{Name: string(rune('a' + i)), ID: string(rune('1' + i))}
	}

	results := exec.ExecuteTools(context.Background(), calls, reg)
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	max := maxRunning.Load()
	if max > 2 {
		t.Errorf("max concurrent = %d, want <= 2", max)
	}
}

// PE-004: context 取消停止所有执行。
func TestParallelToolExecutor_ContextCancellation(t *testing.T) {
	reg := NewDefaultToolRegistry()
	var started atomic.Int32

	_ = reg.RegisterTool(context.Background(), ToolDefinition{
		Name: "slow",
		ParallelSafe: true,
		Handler: func(ctx context.Context, _ map[string]any) (*ToolResult, error) {
			started.Add(1)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(5 * time.Second):
				return &ToolResult{Content: "done"}, nil
			}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	exec := NewParallelToolExecutor(ExecutionParallel, 0)
	calls := []ToolCall{
		{Name: "slow", ID: "1"},
		{Name: "slow", ID: "2"},
	}

	results := exec.ExecuteTools(ctx, calls, reg)
	for _, r := range results {
		if r.Error == nil {
			t.Error("expected error from cancelled context")
		}
	}
}

// PE-005: 工具未找到返回错误结果。
func TestParallelToolExecutor_ToolNotFound(t *testing.T) {
	reg := NewDefaultToolRegistry()
	exec := NewParallelToolExecutor(ExecutionParallel, 0)
	calls := []ToolCall{
		{Name: "nonexistent", ID: "1"},
	}

	results := exec.ExecuteTools(context.Background(), calls, reg)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error == nil {
		t.Error("expected error for nonexistent tool")
	}
	if results[0].Result != nil {
		t.Error("expected nil Result for nonexistent tool")
	}
}

// PE-006: 部分成功、部分失败。
func TestParallelToolExecutor_MixedResults(t *testing.T) {
	reg := NewDefaultToolRegistry()

	_ = reg.RegisterTool(context.Background(), ToolDefinition{
		Name: "ok",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
			return &ToolResult{Content: "success"}, nil
		},
	})
	_ = reg.RegisterTool(context.Background(), ToolDefinition{
		Name: "fail",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
			return nil, errors.New("tool error")
		},
	})

	exec := NewParallelToolExecutor(ExecutionParallel, 0)
	calls := []ToolCall{
		{Name: "ok", ID: "1"},
		{Name: "fail", ID: "2"},
		{Name: "nonexistent", ID: "3"},
	}

	results := exec.ExecuteTools(context.Background(), calls, reg)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// 第一个成功
	if results[0].Error != nil {
		t.Errorf("result[0]: unexpected error: %v", results[0].Error)
	}
	if results[0].Result.Content != "success" {
		t.Errorf("result[0].Content = %q, want %q", results[0].Result.Content, "success")
	}

	// 第二个执行出错
	if results[1].Error == nil {
		t.Error("result[1]: expected error")
	}

	// 第三个工具不存在
	if results[2].Error == nil {
		t.Error("result[2]: expected error for nonexistent tool")
	}
}

// PE-007: 结果按输入顺序返回。
func TestParallelToolExecutor_PreservesOrder(t *testing.T) {
	reg := NewDefaultToolRegistry()

	for i := range 5 {
		name := string(rune('a' + i))
		_ = reg.RegisterTool(context.Background(), ToolDefinition{
			Name: name,
			ParallelSafe: true,
			Handler: func(_ context.Context, _ map[string]any) (*ToolResult, error) {
				// 随机延迟以确保不同完成顺序
				time.Sleep(time.Duration(i*10+1) * time.Millisecond)
				return &ToolResult{Content: name}, nil
			},
		})
	}

	exec := NewParallelToolExecutor(ExecutionParallel, 0)
	calls := []ToolCall{
		{Name: "a", ID: "1"},
		{Name: "b", ID: "2"},
		{Name: "c", ID: "3"},
		{Name: "d", ID: "4"},
		{Name: "e", ID: "5"},
	}

	results := exec.ExecuteTools(context.Background(), calls, reg)
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	expected := []string{"a", "b", "c", "d", "e"}
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result[%d]: unexpected error: %v", i, r.Error)
		}
		if r.Result.Content != expected[i] {
			t.Errorf("result[%d].Content = %q, want %q", i, r.Result.Content, expected[i])
		}
		if r.Call.ID != calls[i].ID {
			t.Errorf("result[%d].Call.ID = %q, want %q", i, r.Call.ID, calls[i].ID)
		}
	}
}
