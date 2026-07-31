// Package loop 定义 LoopAgent 核心调度接口及其默认实现。
//
// harness_test.go 包含 DefaultAgentHarness 的单元测试，覆盖：
// - 接口合规性
// - 新会话自动创建
// - 已有会话上下文恢复
// - 委托给 LoopAgent.Query
// - 会话上下文恢复
// - 会话不存在时的错误处理
// - 资源释放
// - SessionManager 的 io.Closer 释放
package loop

import (
	stdcontext "context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
	"github.com/pengjunchen/go-agent-core/memory/session"
)

// ─── Mock SessionManager ────────────────────────────────────────

// mockSessionManager 是用于测试的 SessionManager 实现。
type mockSessionManager struct {
	mu sync.Mutex
	sessions map[string]*session.Session
	closed bool
	createFn func(ctx stdcontext.Context, opts *session.SessionOptions) (*session.Session, error)
}

func newMockSessionManager() *mockSessionManager {
	return &mockSessionManager{
		sessions: make(map[string]*session.Session),
	}
}

func (m *mockSessionManager) CreateSession(ctx stdcontext.Context, opts *session.SessionOptions) (*session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.createFn != nil {
		return m.createFn(ctx, opts)
	}

	sess := &session.Session{
		ID: fmt.Sprintf("session-%d", time.Now().UnixNano()),
		Status: session.SessionActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if opts != nil && opts.ContextID != "" {
		sess.ContextID = opts.ContextID
	}
	m.sessions[sess.ID] = sess
	return sess, nil
}

func (m *mockSessionManager) GetSession(_ stdcontext.Context, sessionID string) (*session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	return sess, nil
}

func (m *mockSessionManager) UpdateSession(_ stdcontext.Context, sessionID string, update *session.SessionUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}
	if update.Status != nil {
		sess.Status = *update.Status
		sess.UpdatedAt = time.Now()
	}
	return nil
}

func (m *mockSessionManager) DeleteSession(_ stdcontext.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, sessionID)
	return nil
}

func (m *mockSessionManager) ListSessions(_ stdcontext.Context, _ *session.ListOptions) ([]*session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []*session.Session
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result, nil
}

// Close 实现 io.Closer 接口。
func (m *mockSessionManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

// ─── Mock LoopAgent for Harness ─────────────────────────────────

// harnessMockAgent 是用于 Harness 测试的 LoopAgent 实现。
type harnessMockAgent struct {
	mu sync.Mutex
	status event.AgentStatus
	closed bool
	queryCh chan event.AgentEvent
	queryErr error
	// 记录最近一次 Query 的参数
	lastInput AgentInput
}

func newHarnessMockAgent() *harnessMockAgent {
	return &harnessMockAgent{
		status: event.StatusIdle,
	}
}

func (a *harnessMockAgent) Query(_ stdcontext.Context, input AgentInput) (<-chan event.AgentEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.queryErr != nil {
		return nil, a.queryErr
	}

	a.lastInput = input
	a.status = event.StatusRunning

	ch := make(chan event.AgentEvent, 64)
	if a.queryCh != nil {
		// 使用外部提供的 channel
		go func() {
			defer close(ch)
			for evt := range a.queryCh {
				ch <- evt
			}
		}()
	} else {
		// 默认行为：发送完成事件
		go func() {
			defer close(ch)
			ch <- event.AgentEvent{Type: event.EventTurnStart, SubmissionID: "sub-test", TurnID: "turn-test", SessionID: input.SessionID}
			ch <- event.AgentEvent{Type: event.EventTextDelta, SubmissionID: "sub-test", TurnID: "turn-test", SessionID: input.SessionID, Payload: "test response"}
			ch <- event.AgentEvent{Type: event.EventTurnEnd, SubmissionID: "sub-test", TurnID: "turn-test", SessionID: input.SessionID}
			ch <- event.AgentEvent{Type: event.EventCompleted, SubmissionID: "sub-test", TurnID: "turn-test", SessionID: input.SessionID}
		}()
	}

	// 模拟完成后状态变更
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.mu.Lock()
		a.status = event.StatusCompleted
		a.mu.Unlock()
	}()

	return ch, nil
}

func (a *harnessMockAgent) Interrupt(_ stdcontext.Context) error { return nil }
func (a *harnessMockAgent) Steer(_ stdcontext.Context, _ string) error { return nil }
func (a *harnessMockAgent) FollowUp(_ stdcontext.Context, _ string) error { return nil }

func (a *harnessMockAgent) Status() event.AgentStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

func (a *harnessMockAgent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	a.status = event.StatusIdle
	return nil
}

// ─── 测试用例 ──────────────────────────────────────────────────────

// TestAgentHarness_Interface 验证 DefaultAgentHarness 实现了 AgentHarness 接口。
func TestAgentHarness_Interface(t *testing.T) {
	var _ AgentHarness = (*DefaultAgentHarness)(nil)
}

// TestAgentHarness_QueryNewSession 测试查询时空 sessionID 自动创建新会话。
func TestAgentHarness_QueryNewSession(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	eventCh, err := harness.Query(stdcontext.Background(), "hello", "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	// 收集事件
	events := collectHarnessEvents(eventCh, 5*time.Second)
	if !harnessHasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", harnessEventTypes(events))
	}

	// 验证新会话已创建
	if len(sm.sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sm.sessions))
	}

	// 验证 LoopAgent.Query 被调用了，且使用了新创建的 sessionID
	agent.mu.Lock()
	lastInput := agent.lastInput
	agent.mu.Unlock()

	if lastInput.SessionID == "" {
		t.Error("expected non-empty sessionID to be passed to LoopAgent.Query")
	}
	if lastInput.Prompt != "hello" {
		t.Errorf("prompt = %q, want %q", lastInput.Prompt, "hello")
	}
}

// TestAgentHarness_QueryExistingSession 测试查询时提供已有 sessionID 恢复上下文。
func TestAgentHarness_QueryExistingSession(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()

	// 预创建一个会话
	existingSess, _ := sm.CreateSession(stdcontext.Background(), &session.SessionOptions{})

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	eventCh, err := harness.Query(stdcontext.Background(), "hello", existingSess.ID)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectHarnessEvents(eventCh, 5*time.Second)
	if !harnessHasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", harnessEventTypes(events))
	}

	// 验证 LoopAgent.Query 使用了传入的 sessionID
	agent.mu.Lock()
	lastInput := agent.lastInput
	agent.mu.Unlock()

	if lastInput.SessionID != existingSess.ID {
		t.Errorf("sessionID = %q, want %q", lastInput.SessionID, existingSess.ID)
	}
}

// TestAgentHarness_QueryDelegateToAgent 验证 Harness 委托给 LoopAgent.Query。
func TestAgentHarness_QueryDelegateToAgent(t *testing.T) {
	agent := newHarnessMockAgent()
	agent.queryErr = errors.New("delegated error")
	sm := newMockSessionManager()

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	_, err = harness.Query(stdcontext.Background(), "hello", "")
	if err == nil {
		t.Error("expected error from delegate")
	}
	if !errors.Is(err, agent.queryErr) {
		t.Errorf("error = %v, want delegated error", err)
	}
}

// TestAgentHarness_RestoreSession 测试恢复会话上下文。
func TestAgentHarness_RestoreSession(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()
	cm := ctxpkg.NewHeuristicContextManager()

	// 预创建一个会话
	existingSess, _ := sm.CreateSession(stdcontext.Background(), &session.SessionOptions{})

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		WithContextManager(cm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	err = harness.Restore(stdcontext.Background(), existingSess.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
}

// TestAgentHarness_RestoreSessionNotFound 测试恢复不存在的会话时返回错误。
func TestAgentHarness_RestoreSessionNotFound(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()
	cm := ctxpkg.NewHeuristicContextManager()

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		WithContextManager(cm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	err = harness.Restore(stdcontext.Background(), "nonexistent-session")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

// TestAgentHarness_RestoreEmptySessionID 测试恢复时空 sessionID 返回错误。
func TestAgentHarness_RestoreEmptySessionID(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	err = harness.Restore(stdcontext.Background(), "")
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

// TestAgentHarness_Close 测试资源释放。
func TestAgentHarness_Close(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := harness.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证 LoopAgent 被关闭
	agent.mu.Lock()
	closed := agent.closed
	agent.mu.Unlock()
	if !closed {
		t.Error("agent was not closed")
	}
}

// TestAgentHarness_CloseWithSessionManager 测试 SessionManager 实现 io.Closer 时被关闭。
func TestAgentHarness_CloseWithSessionManager(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := harness.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证 SessionManager 被关闭
	if !sm.closed {
		t.Error("SessionManager was not closed")
	}
}

// TestAgentHarness_CloseWithLogger 测试 ExecLogger 实现 io.Closer 时被关闭。
func TestAgentHarness_CloseWithLogger(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()
	logger := &mockClosableLogger{}

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		WithLogger(logger).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if err := harness.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证 Logger 被关闭
	if !logger.closed {
		t.Error("Logger was not closed")
	}
}

// TestAgentHarness_HarnessBuilderNoAgent 测试 HarnessBuilder 未设置 Agent 时返回错误。
func TestAgentHarness_HarnessBuilderNoAgent(t *testing.T) {
	builder := &HarnessBuilder{}
	_, err := builder.Build()
	if !errors.Is(err, ErrNoAgent) {
		t.Errorf("error = %v, want ErrNoAgent", err)
	}
}

// TestAgentHarness_QuerySessionCreateError 测试创建会话失败时的错误处理。
func TestAgentHarness_QuerySessionCreateError(t *testing.T) {
	agent := newHarnessMockAgent()
	sm := newMockSessionManager()
	sm.createFn = func(_ stdcontext.Context, _ *session.SessionOptions) (*session.Session, error) {
		return nil, errors.New("session creation failed")
	}

	harness, err := NewHarnessBuilder(agent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	_, err = harness.Query(stdcontext.Background(), "hello", "")
	if err == nil {
		t.Error("expected error when session creation fails")
	}
}

// TestAgentHarness_IntegrationWithRealAgent 测试 Harness 与真实 DefaultLoopAgent 的集成。
func TestAgentHarness_IntegrationWithRealAgent(t *testing.T) {
	responses := [][]stream.StreamEvent{
		{
			{Type: stream.StreamTextDelta, Content: "Hello from real agent"},
			{Type: stream.StreamDone},
		},
	}

	p := newMockProvider(responses)
	cm := ctxpkg.NewHeuristicContextManager()
	tr := registry.NewDefaultToolRegistry()

	realAgent, err := NewDefaultLoopAgent(&LoopAgentConfig{
		Provider: p,
		ContextManager: cm,
		ToolRegistry: tr,
		MaxTurns: DefaultMaxTurns,
	})
	if err != nil {
		t.Fatalf("NewDefaultLoopAgent: %v", err)
	}

	sm := newMockSessionManager()

	harness, err := NewHarnessBuilder(realAgent).
		WithSessionManager(sm).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	eventCh, err := harness.Query(stdcontext.Background(), "hello", "")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	events := collectHarnessEvents(eventCh, 5*time.Second)
	if !harnessHasEventType(events, event.EventCompleted) {
		t.Errorf("missing EventCompleted, got %v", harnessEventTypes(events))
	}

	// 验证新会话已创建
	if len(sm.sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sm.sessions))
	}

	// 清理
	_ = harness.Close()
}

// ─── 辅助类型 ──────────────────────────────────────────────────────

// mockClosableLogger 是一个实现了 ExecLogger 和 io.Closer 的 mock。
type mockClosableLogger struct {
	closed bool
}

func (m *mockClosableLogger) Log(_ stdcontext.Context, _ *log.ExecLogEntry) error { return nil }
func (m *mockClosableLogger) LogTurn(_ stdcontext.Context, _ *log.TurnRecord) error {
	return nil
}
func (m *mockClosableLogger) LogItem(_ stdcontext.Context, _ *log.ItemRecord) error {
	return nil
}
func (m *mockClosableLogger) LogEvent(_ stdcontext.Context, _ *log.EventRecord) error {
	return nil
}
func (m *mockClosableLogger) LogSession(_ stdcontext.Context, _ *log.SessionRecord) error {
	return nil
}
func (m *mockClosableLogger) Flush(_ stdcontext.Context) error { return nil }
func (m *mockClosableLogger) Close() error {
	m.closed = true
	return nil
}

// ─── 辅助函数 ──────────────────────────────────────────────────────

// collectHarnessEvents 从事件通道收集所有事件，直到通道关闭或超时。
func collectHarnessEvents(ch <-chan event.AgentEvent, timeout time.Duration) []event.AgentEvent {
	var events []event.AgentEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-timer.C:
			return events
		}
	}
}

// harnessEventTypes 提取事件类型列表。
func harnessEventTypes(events []event.AgentEvent) []event.EventType {
	types := make([]event.EventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

// harnessHasEventType 检查事件列表中是否包含指定类型。
func harnessHasEventType(events []event.AgentEvent, t event.EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// ─── 确保不会破坏原有 default_test.go 中的 mock 类型 ─────────────

// 确保 mockProvider 仍然可用于 default_test.go 中的测试。
var _ provider.ModelProvider = (*mockProvider)(nil)

// 确保 mockClosableLogger 实现了 log.ExecLogger。
var _ log.ExecLogger = (*mockClosableLogger)(nil)

// 确保 mockSessionManager 实现了 session.SessionManager。
var _ session.SessionManager = (*mockSessionManager)(nil)

// 确保 mockSessionManager 实现了 io.Closer。
var _ interface{ Close() error } = (*mockSessionManager)(nil)
