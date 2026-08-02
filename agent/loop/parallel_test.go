// Package loop — 并行工具执行测试。
//
// 本文件覆盖以下验收标准：
// - AC-1: 多工具调用通过 ParallelToolExecutor 并行执行
// - AC-2: 并行执行结果正确映射到对应的 tool call ID
// - AC-3: ToolExecutor 为 nil 时回退到串行执行路径
package loop

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
)

// ─── AC-1: 多工具调用并行执行 ──────────────────────────────────────

// TestParallelExecution_MultipleToolsExecuted 验证：
// 当 LLM 返回 2 个 ParallelSafe 工具调用时，两个工具都通过
// ParallelToolExecutor 被执行。
func TestParallelExecution_MultipleToolsExecuted(t *testing.T) {
	var handlerACalls int32
	var handlerBCalls int32

	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-parallel-1",
				Name: "read_file_a",
				Arguments: map[string]any{"path": "/a"},
			}},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-parallel-2",
				Name: "read_file_b",
				Arguments: map[string]any{"path": "/b"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Both files read."},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "read_file_a",
		Description: "Read file A",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			atomic.AddInt32(&handlerACalls, 1)
			return &registry.ToolResult{Content: "content of file A"}, nil
		},
	})
	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "read_file_b",
		Description: "Read file B",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			atomic.AddInt32(&handlerBCalls, 1)
			return &registry.ToolResult{Content: "content of file B"}, nil
		},
	})

	executor := registry.NewParallelToolExecutor(registry.ExecutionParallel, 0)

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		ToolExecutor: executor,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "read both files",
		SessionID: "test-parallel-exec",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证两个工具 handler 都被调用
	if atomic.LoadInt32(&handlerACalls) != 1 {
		t.Errorf("handler A calls = %d, want 1", atomic.LoadInt32(&handlerACalls))
	}
	if atomic.LoadInt32(&handlerBCalls) != 1 {
		t.Errorf("handler B calls = %d, want 1", atomic.LoadInt32(&handlerBCalls))
	}

	// 验证有 2 个 EventToolCallResult
	resultCount := countEventType(events, event.EventToolCallResult)
	if resultCount != 2 {
		t.Errorf("EventToolCallResult count = %d, want 2", resultCount)
	}

	// 验证 Agent 正常完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// ─── AC-2: 结果正确映射到 tool call ID ─────────────────────────────

// TestParallelExecution_ResultsMatchByToolCallID 验证：
// 并行执行后，每个工具结果都正确映射到对应的 tool call ID。
func TestParallelExecution_ResultsMatchByToolCallID(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-id-alpha",
				Name: "read_alpha",
				Arguments: map[string]any{"path": "/alpha"},
			}},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-id-beta",
				Name: "read_beta",
				Arguments: map[string]any{"path": "/beta"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Done reading files."},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "read_alpha",
		Description: "Read alpha file",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "alpha content"}, nil
		},
	})
	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "read_beta",
		Description: "Read beta file",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "beta content"}, nil
		},
	})

	executor := registry.NewParallelToolExecutor(registry.ExecutionParallel, 0)

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		ToolExecutor: executor,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "read files by id",
		SessionID: "test-parallel-id-match",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证 ContextManager 中工具结果按 tool call ID 正确记录
	items, err := cm.GetMessages(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	// 收集所有 tool role 的消息，按 ToolCallID 建立映射
	toolResultsByCallID := make(map[string]string) // ToolCallID -> Content
	for _, item := range items {
		if item.Role == "tool" && item.ToolCallID != "" {
			toolResultsByCallID[item.ToolCallID] = item.Content
		}
	}

	// 验证 alpha 结果
	if content, ok := toolResultsByCallID["tc-id-alpha"]; !ok {
		t.Error("missing tool result for tc-id-alpha")
	} else if content != "alpha content" {
		t.Errorf("tc-id-alpha content = %q, want %q", content, "alpha content")
	}

	// 验证 beta 结果
	if content, ok := toolResultsByCallID["tc-id-beta"]; !ok {
		t.Error("missing tool result for tc-id-beta")
	} else if content != "beta content" {
		t.Errorf("tc-id-beta content = %q, want %q", content, "beta content")
	}

	// 验证有 2 个 EventToolCallResult
	resultCount := countEventType(events, event.EventToolCallResult)
	if resultCount != 2 {
		t.Errorf("EventToolCallResult count = %d, want 2", resultCount)
	}

	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// ─── AC-3: nil executor 回退到串行执行 ─────────────────────────────

// TestParallelExecution_NilExecutorFallsBackToSerial 验证：
// 当 ToolExecutor 为 nil 时，使用现有的串行执行路径，
// 工具调用仍然正常执行并返回正确结果。
func TestParallelExecution_NilExecutorFallsBackToSerial(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-serial-1",
				Name: "read_file",
				Arguments: map[string]any{"path": "/test"},
			}},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-serial-2",
				Name: "read_file",
				Arguments: map[string]any{"path": "/test2"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Serial execution done."},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	var handlerCalls int32
	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "read_file",
		Description: "Read a file",
		ParallelSafe: true,
		Handler: func(_ context.Context, args map[string]any) (*registry.ToolResult, error) {
			atomic.AddInt32(&handlerCalls, 1)
			path, _ := args["path"].(string)
			return &registry.ToolResult{Content: fmt.Sprintf("content of %s", path)}, nil
		},
	})

	// ToolExecutor 为 nil，应使用串行路径
	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "read files serially",
		SessionID: "test-nil-executor",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证两个工具都被调用
	if atomic.LoadInt32(&handlerCalls) != 2 {
		t.Errorf("handler calls = %d, want 2", atomic.LoadInt32(&handlerCalls))
	}

	// 验证有 2 个 EventToolCallResult
	resultCount := countEventType(events, event.EventToolCallResult)
	if resultCount != 2 {
		t.Errorf("EventToolCallResult count = %d, want 2", resultCount)
	}

	// 验证 Agent 正常完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// ─── 补充测试：并行执行确实并行 ─────────────────────────────────────

// TestParallelExecution_ToolsRunConcurrently 验证：
// ParallelSafe 工具确实被并发执行（通过时间差验证）。
func TestParallelExecution_ToolsRunConcurrently(t *testing.T) {
	var startTimes sync.Map

	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-concurrent-1",
				Name: "slow_read_a",
				Arguments: map[string]any{"path": "/a"},
			}},
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-concurrent-2",
				Name: "slow_read_b",
				Arguments: map[string]any{"path": "/b"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Done."},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "slow_read_a",
		Description: "Slow read A",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			startTimes.Store("a", time.Now())
			time.Sleep(100 * time.Millisecond)
			return &registry.ToolResult{Content: "A done"}, nil
		},
	})
	_ = tr.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "slow_read_b",
		Description: "Slow read B",
		ParallelSafe: true,
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			startTimes.Store("b", time.Now())
			time.Sleep(100 * time.Millisecond)
			return &registry.ToolResult{Content: "B done"}, nil
		},
	})

	executor := registry.NewParallelToolExecutor(registry.ExecutionParallel, 0)

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		ToolExecutor: executor,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	start := time.Now()
	ch, _ := agent.Query(context.Background(), AgentInput{
		Prompt: "read slowly",
		SessionID: "test-concurrent",
	})
	events := collectEvents(ch, 10*time.Second)
	elapsed := time.Since(start)

	// 验证 Agent 完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证两个工具的启动时间接近（差异小于 50ms 表示并行）
	timeA, okA := startTimes.Load("a")
	timeB, okB := startTimes.Load("b")
	if !okA || !okB {
		t.Fatal("tool start times not recorded")
	}
	diff := timeA.(time.Time).Sub(timeB.(time.Time))
	if diff < 0 {
		diff = -diff
	}
	if diff > 50*time.Millisecond {
		t.Errorf("tools did not run concurrently: start time diff = %v", diff)
	}

	// 如果串行执行，总耗时至少 200ms；并行执行应约 100ms
	if elapsed > 180*time.Millisecond {
		t.Errorf("execution took %v, expected parallel (< 180ms)", elapsed)
	}
}
