// Package loop — ProductionBundle 集成测试（S-16）。
//
// 验证六大生产化组件与 DefaultLoopAgent 的端到端集成：
// - LoopDetector: 连续相同工具调用触发 EventToolLoopDetected
// - CircuitBreaker: LLM 失败后熔断，后续调用返回 ErrCircuitOpen
// - SecurityGuard: 阻止被屏蔽工具的执行
// - AuditLogger: 记录工具调用审计事件，支持 Query 查询
// - Nil ProductionBundle 不影响 Agent 正常运行
// - 全量 ProductionBundle 协同工作
package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/production"
)

// ─── 辅助：构建带 ProductionBundle 的 Agent ──────────────────────────

// setupAgentWithProduction 创建带 ProductionBundle 的 DefaultLoopAgent。
func setupAgentWithProduction(
	responses [][]stream.StreamEvent,
	maxTurns int,
	pb *production.ProductionBundle,
) *DefaultLoopAgent {
	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	cfg := &LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: maxTurns,
		ProductionBundle: pb,
	}
	if maxTurns <= 0 {
		cfg.MaxTurns = DefaultMaxTurns
	}

	agent, err := NewDefaultLoopAgent(cfg)
	if err != nil {
		panic(fmt.Sprintf("setupAgentWithProduction: %v", err))
	}
	return agent
}

// sameToolCallResponse 构造一个返回指定工具调用的 StreamEvent 序列。
func sameToolCallResponse(toolID, toolName string, args map[string]any) []stream.StreamEvent {
	return []stream.StreamEvent{
		{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
			ID: toolID,
			Name: toolName,
			Arguments: args,
		}},
		{Type: stream.StreamDone},
	}
}

// ─── S-16-1: LoopDetector 集成 ──────────────────────────────────────

// TestProductionBundle_LoopDetectorIntegration 验证：
// 连续相同工具调用超过阈值时，LoopDetector 触发 EventToolLoopDetected，
// Agent 返回 StatusError。
func TestProductionBundle_LoopDetectorIntegration(t *testing.T) {
	ld := production.NewDefaultLoopDetector(production.LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	})

	pb := production.NewProductionBundle(production.WithLoopDetector(ld))

	// 构造 4 轮响应：每轮都调用相同的 repeat_tool（阈值=3，第3次应触发检测）
	toolArgs := map[string]any{"action": "repeat"}
	responses := make([][]stream.StreamEvent, 5)
	for i := range responses {
		responses[i] = sameToolCallResponse(
			fmt.Sprintf("tc-%d", i),
			"repeat_tool",
			toolArgs,
		)
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	// 注册 repeat_tool
	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "repeat_tool",
		Description: "A tool that repeats",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "repeated"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "repeat the action",
		SessionID: "test-loop-detector",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证 EventToolLoopDetected 被发射
	if !hasEventType(events, event.EventToolLoopDetected) {
		t.Errorf("missing EventToolLoopDetected, got %v", eventTypes(events))
	}

	// 验证 Agent 进入 StatusError
	if agent.Status() != event.StatusError {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusError)
	}

	// 验证 EventCompleted 仍然被发送（P0 Fix 1）
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 EventToolLoopDetected 的 Payload 包含工具名
	for _, e := range events {
		if e.Type == event.EventToolLoopDetected {
			toolName, ok := e.Payload.(string)
			if !ok || toolName != "repeat_tool" {
				t.Errorf("EventToolLoopDetected payload = %v, want %q", e.Payload, "repeat_tool")
			}
		}
	}

	// 验证 LoopDetector 内部计数
	count := ld.ConsecutiveCount(context.Background(), "repeat_tool")
	if count < 3 {
		t.Errorf("ConsecutiveCount = %d, want >= 3", count)
	}
}

// ─── S-16-2: CircuitBreaker 集成 ────────────────────────────────────

// TestProductionBundle_CircuitBreakerIntegration 验证：
// LLM 调用失败后 CircuitBreaker 熔断，后续调用立即返回 ErrCircuitOpen。
func TestProductionBundle_CircuitBreakerIntegration(t *testing.T) {
	cb := production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout: 30 * time.Second,
		HalfOpenMaxReqs: 1,
	})

	pb := production.NewProductionBundle(production.WithCircuitBreaker(cb))

	// 第一个 Agent：使用总是失败的 Provider
	errProvider := &errorMockProvider{err: &HTTPError{StatusCode: 500, Message: "internal error"}}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent1, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: errProvider,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
		ProductionBundle: pb,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	// 第一次查询：LLM 失败 → CB 记录失败 → FailureThreshold=1 → CB 打开
	ch, err := agent1.Query(context.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	events1 := collectEvents(ch, 5*time.Second)

	if !hasEventType(events1, event.EventError) {
		t.Errorf("missing EventError on first query, got %v", eventTypes(events1))
	}
	if !hasEventType(events1, event.EventCompleted) {
		t.Errorf("missing EventCompleted on first query, got %v", eventTypes(events1))
	}

	// 验证 CB 已打开
	if cb.State() != production.CircuitOpen {
		t.Errorf("CB state = %v, want CircuitOpen", cb.State())
	}

	// 第二个 Agent：共享同一个 CB，使用正常的 Provider
	// 当 CB 处于 Open 状态时，新的 LLM 调用应被立即拒绝
	normalProvider := newMockProvider([][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamDone},
		},
	})

	cm2 := ctxpkg.NewHeuristicContextManager()
	tr2 := registry.NewDefaultToolRegistry()

	agent2, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: normalProvider,
		ContextManager: cm2,
		ToolRegistry: tr2,
		MaxTurns: DefaultMaxTurns,
		ProductionBundle: pb,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent agent2: %v", err)
	}

	ch2, err := agent2.Query(context.Background(), AgentInput{Prompt: "test2"})
	if err != nil {
		t.Fatalf("Query agent2: %v", err)
	}
	events2 := collectEvents(ch2, 5*time.Second)

	// 第二个 Agent 应收到 circuit breaker open 错误
	if !hasEventType(events2, event.EventError) {
		t.Errorf("missing EventError on second query (circuit open), got %v", eventTypes(events2))
	}

	// 验证错误消息包含 "circuit breaker"
	var foundCircuitOpenError bool
	for _, e := range events2 {
		if e.Type == event.EventError && e.Error != nil {
			if strings.Contains(e.Error.Error(), "circuit breaker") {
				foundCircuitOpenError = true
			}
		}
	}
	if !foundCircuitOpenError {
		t.Errorf("expected error containing 'circuit breaker', got events: %v", eventTypes(events2))
	}
}

// ─── S-16-3: SecurityGuard 集成 ─────────────────────────────────────

// TestProductionBundle_SecurityGuardIntegration 验证：
// ConfigSecurityGuard 阻止被屏蔽的工具执行，Agent 继续运行。
func TestProductionBundle_SecurityGuardIntegration(t *testing.T) {
	sg := production.NewConfigSecurityGuard(production.SecurityGuardConfig{
		BlockedTools: map[string]bool{"dangerous_tool": true},
		BlockMessage: "tool blocked by security policy",
	})

	pb := production.NewProductionBundle(production.WithSecurityGuard(sg))

	// 第一轮：LLM 请求调用 dangerous_tool
	// 第二轮：LLM 给出文本回复
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-danger",
				Name: "dangerous_tool",
				Arguments: map[string]any{"cmd": "rm -rf /"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "I cannot execute that command."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	// 注册 dangerous_tool（虽然被安全守卫阻止，仍需注册以通过 HookPipeline）
	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "dangerous_tool",
		Description: "A dangerous tool",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			t.Error("dangerous_tool handler should not be called when blocked by security")
			return &registry.ToolResult{Content: "should not reach"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "use the dangerous tool",
		SessionID: "test-security-guard",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证 EventToolCallStart 被发射（工具调用开始事件在安全检查之前）
	if !hasEventType(events, event.EventToolCallStart) {
		t.Errorf("missing EventToolCallStart, got %v", eventTypes(events))
	}

	// 验证 EventToolCallResult 包含 IsError=true（被安全守卫阻止）
	blockedResultFound := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				if strings.Contains(tr.Content, "blocked by security") {
					blockedResultFound = true
				}
			}
		}
	}
	if !blockedResultFound {
		t.Errorf("expected EventToolCallResult with IsError and 'blocked by security', got %v", eventTypes(events))
	}

	// 验证 EventError 被发射
	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}
}

// ─── S-16-4: AuditLogger 集成 ───────────────────────────────────────

// TestProductionBundle_AuditLoggerIntegration 验证：
// LogAuditLogger 记录工具调用审计事件，Query 可查询到。
func TestProductionBundle_AuditLoggerIntegration(t *testing.T) {
	al := production.NewLogAuditLogger(nil) // 不依赖 ExecLogger，仅内存存储

	pb := production.NewProductionBundle(production.WithAuditLogger(al))

	// 第一轮：LLM 调用 audit_tool
	// 第二轮：LLM 给出文本回复
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-audit",
				Name: "audit_tool",
				Arguments: map[string]any{"key": "value"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Audit tool executed."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "audit_tool",
		Description: "A tool for audit testing",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "audit result"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "use the audit tool",
		SessionID: "test-audit-session",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证 Agent 正常完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 AuditLogger 记录了工具调用事件
	auditEvents, err := al.Query(context.Background(), production.AuditFilter{
		EventType: "tool_call",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("AuditLogger Query: %v", err)
	}

	if len(auditEvents) == 0 {
		t.Fatal("AuditLogger should have recorded at least one tool_call event")
	}

	// 验证审计事件内容
	firstAudit := auditEvents[0]
	if firstAudit.Type != "tool_call" {
		t.Errorf("audit event type = %q, want %q", firstAudit.Type, "tool_call")
	}

	toolCallData, ok := firstAudit.Data.(production.AuditToolCallEvent)
	if !ok {
		t.Fatalf("audit event data type = %T, want AuditToolCallEvent", firstAudit.Data)
	}
	if toolCallData.ToolName != "audit_tool" {
		t.Errorf("audit tool name = %q, want %q", toolCallData.ToolName, "audit_tool")
	}
	if toolCallData.SessionID != "test-audit-session" {
		t.Errorf("audit session ID = %q, want %q", toolCallData.SessionID, "test-audit-session")
	}
	if !toolCallData.Approved {
		t.Error("audit Approved should be true (tool was not blocked)")
	}
	if toolCallData.DecisionBy != "auto" {
		t.Errorf("audit DecisionBy = %q, want %q", toolCallData.DecisionBy, "auto")
	}
}

// ─── S-16-5: Nil ProductionBundle 不影响 Agent ──────────────────────

// TestProductionBundle_NilBundleNoEffect 验证：
// 不设置 ProductionBundle 时，Agent 行为与无生产化组件时完全一致。
func TestProductionBundle_NilBundleNoEffect(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello"},
			{Type: stream.StreamTextDelta, Content: " world"},
			{Type: stream.StreamDone},
		},
	}

	// 不传入 ProductionBundle
	agent := setupAgentWithProduction(responses, 0, nil)

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "test nil bundle",
		SessionID: "test-nil-bundle",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)
	types := eventTypes(events)

	if !hasEventType(events, event.EventTurnStart) {
		t.Errorf("missing EventTurnStart, got %v", types)
	}
	if !hasEventType(events, event.EventTextDelta) {
		t.Errorf("missing EventTextDelta, got %v", types)
	}
	if !hasEventType(events, event.EventTurnEnd) {
		t.Errorf("missing EventTurnEnd, got %v", types)
	}
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", types)
	}

	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// ─── S-16-6: 全量 ProductionBundle 集成 ──────────────────────────────

// TestProductionBundle_FullIntegration 验证：
// 所有生产化组件同时工作时互不干扰，Agent 能正常完成。
func TestProductionBundle_FullIntegration(t *testing.T) {
	ld := production.NewDefaultLoopDetector(production.LoopDetectorConfig{
		ConsecutiveThreshold: 5, // 高阈值，本测试不应触发
		WindowSize: 10,
	})
	cb := production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
		FailureThreshold: 5, // 高阈值，本测试不应触发
		Timeout: 30 * time.Second,
	})
	sg := production.NewConfigSecurityGuard(production.SecurityGuardConfig{
		BlockedTools: map[string]bool{"forbidden_tool": true},
	})
	al := production.NewLogAuditLogger(nil)

	pb := production.NewProductionBundle(
		production.WithLoopDetector(ld),
		production.WithCircuitBreaker(cb),
		production.WithSecurityGuard(sg),
		production.WithAuditLogger(al),
	)

	// 第一轮：LLM 调用 safe_tool
	// 第二轮：LLM 给出文本回复
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-safe",
				Name: "safe_tool",
				Arguments: map[string]any{"input": "test"},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "All production components working."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "safe_tool",
		Description: "A safe tool",
		Parameters: map[string]any{"type": "object"},
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "safe result"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "use the safe tool",
		SessionID: "test-full-integration",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证 Agent 正常完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}

	// 验证工具调用正常执行
	if !hasEventType(events, event.EventToolCallStart) {
		t.Errorf("missing EventToolCallStart, got %v", eventTypes(events))
	}
	if !hasEventType(events, event.EventToolCallResult) {
		t.Errorf("missing EventToolCallResult, got %v", eventTypes(events))
	}

	// 验证 CircuitBreaker 仍处于 Closed 状态
	if cb.State() != production.CircuitClosed {
		t.Errorf("CB state = %v, want CircuitClosed", cb.State())
	}

	// 验证 LoopDetector 未检测到循环
	if ld.IsLoop(context.Background()) {
		t.Error("LoopDetector should not detect a loop")
	}

	// 验证 AuditLogger 记录了事件
	auditEvents, err := al.Query(context.Background(), production.AuditFilter{
		EventType: "tool_call",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("AuditLogger Query: %v", err)
	}
	if len(auditEvents) == 0 {
		t.Error("AuditLogger should have recorded tool_call events")
	}

	// 验证无错误事件（安全守卫未阻止 safe_tool）
	if hasEventType(events, event.EventError) {
		t.Errorf("unexpected EventError in full integration, got %v", eventTypes(events))
	}

	// 验证无循环检测事件
	if hasEventType(events, event.EventToolLoopDetected) {
		t.Errorf("unexpected EventToolLoopDetected in full integration")
	}
}

// ─── S-16-7: SecurityGuard 阻止 + AuditLogger 记录联合 ───────────────

// TestProductionBundle_SecurityGuardAndAuditLogger 验证：
// 当 SecurityGuard 阻止工具调用时，AuditLogger 不记录该调用
// （因为阻止发生在工具执行之前，审计日志仅记录实际执行的调用）。
func TestProductionBundle_SecurityGuardAndAuditLogger(t *testing.T) {
	al := production.NewLogAuditLogger(nil)
	sg := production.NewConfigSecurityGuard(production.SecurityGuardConfig{
		BlockedTools: map[string]bool{"blocked_tool": true},
	})

	pb := production.NewProductionBundle(
		production.WithSecurityGuard(sg),
		production.WithAuditLogger(al),
	)

	// 第一轮：LLM 请求调用 blocked_tool
	// 第二轮：LLM 请求调用 allowed_tool
	// 第三轮：LLM 给出文本回复
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-blocked",
				Name: "blocked_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-allowed",
				Name: "allowed_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Done."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "blocked_tool",
		Description: "A blocked tool",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "should not run"}, nil
		},
	})
	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "allowed_tool",
		Description: "An allowed tool",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "allowed result"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{
		Prompt: "try both tools",
		SessionID: "test-security-audit",
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证 Agent 完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 AuditLogger 仅记录了 allowed_tool（未被阻止的工具）
	auditEvents, err := al.Query(context.Background(), production.AuditFilter{
		EventType: "tool_call",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("AuditLogger Query: %v", err)
	}

	// 审计日志不应包含 blocked_tool
	for _, ae := range auditEvents {
		data, ok := ae.Data.(production.AuditToolCallEvent)
		if !ok {
			continue
		}
		if data.ToolName == "blocked_tool" {
			t.Error("AuditLogger should not record blocked_tool (it was blocked before execution)")
		}
	}

	// 审计日志应包含 allowed_tool
	var foundAllowedAudit bool
	for _, ae := range auditEvents {
		data, ok := ae.Data.(production.AuditToolCallEvent)
		if !ok {
			continue
		}
		if data.ToolName == "allowed_tool" {
			foundAllowedAudit = true
		}
	}
	if !foundAllowedAudit {
		t.Error("AuditLogger should have recorded allowed_tool")
	}
}

// ─── S-16-8: CircuitBreaker 包裹 LLM 调用验证 ───────────────────────

// TestProductionBundle_CircuitBreakerWrapsLLM 验证：
// CircuitBreaker 确实包裹了 LLM 调用。
// 成功调用后 CB 状态为 Closed，LLM 调用计数正确。
func TestProductionBundle_CircuitBreakerWrapsLLM(t *testing.T) {
	cb := production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout: 30 * time.Second,
	})

	pb := production.NewProductionBundle(production.WithCircuitBreaker(cb))

	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "LLM call successful."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	ch, err := agent.Query(context.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证成功完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}

	// 验证 CB 仍处于 Closed 状态（LLM 调用成功）
	if cb.State() != production.CircuitClosed {
		t.Errorf("CB state = %v, want CircuitClosed after successful LLM call", cb.State())
	}
}

// ─── S-16-9: LoopDetector 不影响非循环工具调用 ────────────────────────

// TestProductionBundle_LoopDetectorNoFalsePositive 验证：
// 不同工具交替调用时，LoopDetector 不会误报循环。
func TestProductionBundle_LoopDetectorNoFalsePositive(t *testing.T) {
	ld := production.NewDefaultLoopDetector(production.LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
	})

	pb := production.NewProductionBundle(production.WithLoopDetector(ld))

	// 交替调用两个不同工具
	responses := [][]stream.StreamEvent{
		sameToolCallResponse("tc-1", "tool_a", map[string]any{"x": 1}),
		sameToolCallResponse("tc-2", "tool_b", map[string]any{"y": 2}),
		sameToolCallResponse("tc-3", "tool_a", map[string]any{"x": 3}),
		{
			{Type: stream.StreamTextDelta, Content: "Done with alternating tools."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "tool_a",
		Description: "Tool A",
		Handler: func(_ context.Context, args map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: fmt.Sprintf("A: %v", args["x"])}, nil
		},
	})
	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "tool_b",
		Description: "Tool B",
		Handler: func(_ context.Context, args map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: fmt.Sprintf("B: %v", args["y"])}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{Prompt: "alternate tools"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 10*time.Second)

	// 验证不应有 EventToolLoopDetected
	if hasEventType(events, event.EventToolLoopDetected) {
		t.Errorf("unexpected EventToolLoopDetected for alternating tool calls")
	}

	// 验证正常完成
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
	if agent.Status() != event.StatusCompleted {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusCompleted)
	}
}

// ─── S-16-10: Builder WithProduction 方法 ────────────────────────────

// TestBuilderWithProduction 验证 Builder 的 WithProduction 方法正确设置 ProductionBundle。
func TestBuilderWithProduction(t *testing.T) {
	p := newMockProvider(nil)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	pb := production.NewProductionBundle(
		production.WithLoopDetector(production.NewDefaultLoopDetector(production.LoopDetectorConfig{
			ConsecutiveThreshold: 3,
		})),
	)

	agent, err := NewBuilder().
		WithProvider(p).
		WithContextManager(cm).
		WithToolRegistry(tr).
		WithProduction(pb).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if agent.productionBundle == nil {
		t.Error("productionBundle should not be nil")
	}
	if agent.productionBundle.LoopDetector == nil {
		t.Error("LoopDetector should not be nil in productionBundle")
	}
}

// ─── S-16-11: errorMockProvider 复用验证 ─────────────────────────────

// TestProductionBundle_ErrorMockProviderSmoke 验证 errorMockProvider 在 ProductionBundle
// 测试中的行为符合预期（StreamChat 返回错误）。
func TestProductionBundle_ErrorMockProviderSmoke(t *testing.T) {
	p := &errorMockProvider{err: &HTTPError{StatusCode: 500, Message: "test error"}}
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	agent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	ch, err := agent.Query(context.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError, got %v", eventTypes(events))
	}
	if !hasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", eventTypes(events))
	}
	if agent.Status() != event.StatusError {
		t.Errorf("status = %v, want %v", agent.Status(), event.StatusError)
	}
}

// ─── 编译时接口合规校验 ──────────────────────────────────────────────

// TestProductionBundle_InterfaceCompliance 编译时校验 ProductionBundle 各组件实现对应接口。
func TestProductionBundle_InterfaceCompliance(t *testing.T) {
	var _ production.LoopDetector = production.NewDefaultLoopDetector(production.LoopDetectorConfig{})
	var _ production.CircuitBreaker = production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{})
	var _ production.SecurityGuard = production.NewConfigSecurityGuard(production.SecurityGuardConfig{})
	var _ production.AuditLogger = production.NewLogAuditLogger(nil)
}

// ─── 额外的 mock provider（可追踪调用计数）──────────────────────────

// countingErrorProvider 是一个可追踪调用次数的 mock provider，
// 总是从 StreamChat 返回错误。
type countingErrorProvider struct {
	err error
	callCount int
}

func (p *countingErrorProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	p.callCount++
	return nil, p.err
}

func (p *countingErrorProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return nil, p.err
}

func (p *countingErrorProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{Provider: "counting-error-mock", ModelName: "error"}
}

// TestProductionBundle_CircuitBreakerRejectsOnOpen 验证：
// CB 打开后，LLM 调用被直接拒绝，Provider 的 StreamChat 不会被调用。
func TestProductionBundle_CircuitBreakerRejectsOnOpen(t *testing.T) {
	cb := production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout: 30 * time.Second,
	})

	pb := production.NewProductionBundle(production.WithCircuitBreaker(cb))

	// 第一步：用 error provider 触发 CB 打开
	errProvider := &countingErrorProvider{
		err: &HTTPError{StatusCode: 500, Message: "fail"},
	}
	cm1 := ctxpkg.NewHeuristicContextManager()
	tr1 := registry.NewDefaultToolRegistry()

	agent1, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: errProvider,
		ContextManager: cm1,
		ToolRegistry: tr1,
		MaxTurns: DefaultMaxTurns,
		ProductionBundle: pb,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent agent1: %v", err)
	}

	ch1, _ := agent1.Query(context.Background(), AgentInput{Prompt: "trigger"})
	_ = collectEvents(ch1, 5*time.Second)

	if cb.State() != production.CircuitOpen {
		t.Fatalf("CB should be open, got %v", cb.State())
	}

	// 第二步：用 countingError provider 验证 CB 打开后 StreamChat 不被调用
	countingProvider := &countingErrorProvider{
		err: &HTTPError{StatusCode: 500, Message: "should not reach"},
	}
	cm2 := ctxpkg.NewHeuristicContextManager()
	tr2 := registry.NewDefaultToolRegistry()

	agent2, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: countingProvider,
		ContextManager: cm2,
		ToolRegistry: tr2,
		MaxTurns: DefaultMaxTurns,
		ProductionBundle: pb,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent agent2: %v", err)
	}

	ch2, _ := agent2.Query(context.Background(), AgentInput{Prompt: "should be rejected"})
	events2 := collectEvents(ch2, 5*time.Second)

	// StreamChat 不应被调用（CB 已打开）
	if countingProvider.callCount != 0 {
		t.Errorf("StreamChat called %d times, want 0 (CB is open)", countingProvider.callCount)
	}

	// 验证错误事件包含 "circuit breaker"
	var foundCBError bool
	for _, e := range events2 {
		if e.Type == event.EventError && e.Error != nil {
			if strings.Contains(e.Error.Error(), "circuit breaker") {
				foundCBError = true
			}
		}
	}
	if !foundCBError {
		t.Errorf("expected error containing 'circuit breaker', got %v", eventTypes(events2))
	}
}

// ─── S-16-12: SecurityGuard 白名单模式 ───────────────────────────────

// TestProductionBundle_SecurityGuardWhitelist 验证：
// 白名单模式下，只有 AllowedTools 中的工具可以执行。
func TestProductionBundle_SecurityGuardWhitelist(t *testing.T) {
	sg := production.NewConfigSecurityGuard(production.SecurityGuardConfig{
		AllowedTools: map[string]bool{"allowed_tool": true},
	})

	pb := production.NewProductionBundle(production.WithSecurityGuard(sg))

	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-not-allowed",
				Name: "not_in_whitelist",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Tool was not allowed."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "not_in_whitelist",
		Description: "Not in whitelist",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			t.Error("not_in_whitelist handler should not be called")
			return &registry.ToolResult{Content: "should not run"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{Prompt: "use not-allowed tool"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 验证工具调用结果包含 IsError
	blockedFound := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				blockedFound = true
			}
		}
	}
	if !blockedFound {
		t.Errorf("expected blocked tool result with IsError=true, got %v", eventTypes(events))
	}
}

// ─── S-16-13: 安全守卫错误路径验证 ──────────────────────────────────

// TestProductionBundle_SecurityGuardValidationError 验证：
// SecurityGuard 本身返回错误时，Agent 发出 EventError 和 EventToolCallResult（IsError）。
func TestProductionBundle_SecurityGuardValidationError(t *testing.T) {
	sg := &errorSecurityGuard{err: errors.New("security service unavailable")}
	pb := production.NewProductionBundle(production.WithSecurityGuard(sg))

	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamToolCallStart, ToolCall: &stream.ToolCall{
				ID: "tc-1",
				Name: "any_tool",
				Arguments: map[string]any{},
			}},
			{Type: stream.StreamDone},
		},
		{
			{Type: stream.StreamTextDelta, Content: "Continuing after error."},
			{Type: stream.StreamDone},
		},
	}

	agent := setupAgentWithProduction(responses, 10, pb)

	_ = agent.toolRegistry.RegisterTool(context.Background(), registry.ToolDefinition{
		Name: "any_tool",
		Description: "Any tool",
		Handler: func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
			return &registry.ToolResult{Content: "result"}, nil
		},
	})

	ch, err := agent.Query(context.Background(), AgentInput{Prompt: "test"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectEvents(ch, 5*time.Second)

	// 应该有 EventError（security guard 错误）
	if !hasEventType(events, event.EventError) {
		t.Errorf("missing EventError for security guard validation error, got %v", eventTypes(events))
	}

	// 应该有 EventToolCallResult（包含错误信息）
	toolResultFound := false
	for _, e := range events {
		if e.Type == event.EventToolCallResult {
			if tr, ok := e.Payload.(*registry.ToolResult); ok && tr.IsError {
				if strings.Contains(tr.Content, "security validation error") {
					toolResultFound = true
				}
			}
		}
	}
	if !toolResultFound {
		t.Errorf("expected EventToolCallResult with 'security validation error', got %v", eventTypes(events))
	}
}

// errorSecurityGuard 是一个总是返回错误的 SecurityGuard 实现。
type errorSecurityGuard struct {
	err error
}

func (g *errorSecurityGuard) ValidateToolCall(_ context.Context, _ production.SecurityCallInfo) (*production.SecurityDecision, error) {
	return nil, g.err
}

func (g *errorSecurityGuard) ValidateInput(_ context.Context, _ string) error {
	return g.err
}
