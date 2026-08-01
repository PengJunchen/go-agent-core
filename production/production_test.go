package production

import (
	"context"
	"testing"
	"time"
)

// ─── CircuitBreaker interface compliance ───────────────────────────

func TestCircuitBreaker_Interface(t *testing.T) {
	var _ CircuitBreaker = (*noopCircuitBreaker)(nil)
}

func TestCircuitBreaker_Execute(t *testing.T) {
	cb := noopCircuitBreaker{}
	err := cb.Execute(context.Background(), func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("Execute err = %v, want nil", err)
	}
}

func TestCircuitBreaker_State(t *testing.T) {
	cb := noopCircuitBreaker{}
	if st := cb.State(); st != CircuitClosed {
		t.Errorf("State = %d, want %d", st, CircuitClosed)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := noopCircuitBreaker{}
	cb.Reset() // should not panic
}

func TestCircuitState_Values(t *testing.T) {
	if CircuitClosed != 0 {
		t.Errorf("CircuitClosed = %d, want 0", CircuitClosed)
	}
	if CircuitOpen != 1 {
		t.Errorf("CircuitOpen = %d, want 1", CircuitOpen)
	}
	if CircuitHalfOpen != 2 {
		t.Errorf("CircuitHalfOpen = %d, want 2", CircuitHalfOpen)
	}
}

func TestCircuitBreakerConfig_Defaults(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	if cfg.FailureThreshold != 5 {
		t.Errorf("FailureThreshold = %d, want 5", cfg.FailureThreshold)
	}
	if cfg.SuccessThreshold != 3 {
		t.Errorf("SuccessThreshold = %d, want 3", cfg.SuccessThreshold)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if cfg.HalfOpenMaxReqs != 1 {
		t.Errorf("HalfOpenMaxReqs = %d, want 1", cfg.HalfOpenMaxReqs)
	}
}

func TestCircuitBreakerConfig_Construct(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 10,
		SuccessThreshold: 5,
		Timeout: 60 * time.Second,
		HalfOpenMaxReqs: 2,
	}
	if cfg.FailureThreshold != 10 {
		t.Errorf("FailureThreshold = %d, want 10", cfg.FailureThreshold)
	}
	if cfg.SuccessThreshold != 5 {
		t.Errorf("SuccessThreshold = %d, want 5", cfg.SuccessThreshold)
	}
}

// ─── LoopDetector interface compliance ─────────────────────────────

func TestLoopDetector_Interface(t *testing.T) {
	var _ LoopDetector = (*noopLoopDetector)(nil)
}

func TestLoopDetector_Record(t *testing.T) {
	ld := noopLoopDetector{}
	err := ld.Record(context.Background(), ToolCallRecord{
		ToolName: "search",
		Arguments: map[string]any{"q": "test"},
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Errorf("Record err = %v, want nil", err)
	}
}

func TestLoopDetector_IsLoop(t *testing.T) {
	ld := noopLoopDetector{}
	if ld.IsLoop(context.Background()) {
		t.Error("IsLoop = true, want false for noop")
	}
}

func TestLoopDetector_Reset(t *testing.T) {
	ld := noopLoopDetector{}
	err := ld.Reset(context.Background())
	if err != nil {
		t.Errorf("Reset err = %v, want nil", err)
	}
}

func TestLoopDetector_ConsecutiveCount(t *testing.T) {
	ld := noopLoopDetector{}
	if n := ld.ConsecutiveCount(context.Background(), "search"); n != 0 {
		t.Errorf("ConsecutiveCount = %d, want 0 for noop", n)
	}
}

func TestToolCallRecord_Construct(t *testing.T) {
	now := time.Now()
	rec := ToolCallRecord{
		ToolName: "read_file",
		Arguments: map[string]any{"path": "/tmp/test"},
		Timestamp: now,
	}
	if rec.ToolName != "read_file" {
		t.Errorf("ToolName = %q, want read_file", rec.ToolName)
	}
	if rec.Timestamp != now {
		t.Errorf("Timestamp mismatch")
	}
}

func TestLoopDetectorConfig_Defaults(t *testing.T) {
	cfg := DefaultLoopDetectorConfig()
	if cfg.ConsecutiveThreshold != 3 {
		t.Errorf("ConsecutiveThreshold = %d, want 3", cfg.ConsecutiveThreshold)
	}
	if cfg.WindowSize != 10 {
		t.Errorf("WindowSize = %d, want 10", cfg.WindowSize)
	}
	if !cfg.ArgumentComparison {
		t.Error("ArgumentComparison = false, want true")
	}
}

func TestLoopDetectorConfig_Construct(t *testing.T) {
	cfg := LoopDetectorConfig{
		ConsecutiveThreshold: 5,
		WindowSize: 20,
		ArgumentComparison: false,
	}
	if cfg.ConsecutiveThreshold != 5 {
		t.Errorf("ConsecutiveThreshold = %d, want 5", cfg.ConsecutiveThreshold)
	}
	if cfg.ArgumentComparison {
		t.Error("ArgumentComparison = true, want false")
	}
}

// ─── IdempotencyKey interface compliance ───────────────────────────

func TestIdempotencyKey_Interface(t *testing.T) {
	var _ IdempotencyKey = (*noopIdempotencyKey)(nil)
}

func TestIdempotencyKey_Check(t *testing.T) {
	ik := noopIdempotencyKey{}
	rec, found, err := ik.Check(context.Background(), "key-1")
	if err != nil {
		t.Errorf("Check err = %v, want nil", err)
	}
	if found {
		t.Error("Check found = true, want false for noop")
	}
	if rec != nil {
		t.Error("Check rec should be nil for noop")
	}
}

func TestIdempotencyKey_Record(t *testing.T) {
	ik := noopIdempotencyKey{}
	err := ik.Record(context.Background(), "key-1", "result")
	if err != nil {
		t.Errorf("Record err = %v, want nil", err)
	}
}

func TestIdempotencyKey_Delete(t *testing.T) {
	ik := noopIdempotencyKey{}
	err := ik.Delete(context.Background(), "key-1")
	if err != nil {
		t.Errorf("Delete err = %v, want nil", err)
	}
}

func TestIdempotencyRecord_Construct(t *testing.T) {
	now := time.Now()
	rec := &IdempotencyRecord{
		Key: "op-123",
		Result: "done",
		CreatedAt: now,
		ExpiresAt: now.Add(5 * time.Minute),
	}
	if rec.Key != "op-123" {
		t.Errorf("Key = %q, want op-123", rec.Key)
	}
	if rec.ExpiresAt.Before(rec.CreatedAt) {
		t.Error("ExpiresAt should be after CreatedAt")
	}
}

// ─── SecurityGuard interface compliance ────────────────────────────

func TestSecurityGuard_Interface(t *testing.T) {
	var _ SecurityGuard = (*noopSecurityGuard)(nil)
}

func TestSecurityGuard_ValidateToolCall(t *testing.T) {
	sg := noopSecurityGuard{}
	dec, err := sg.ValidateToolCall(context.Background(), SecurityCallInfo{
		ToolName: "read_file",
		Arguments: map[string]any{"path": "/etc/passwd"},
		SessionID: "s-1",
		UserID: "u-1",
	})
	if err != nil {
		t.Errorf("ValidateToolCall err = %v, want nil", err)
	}
	if !dec.Allowed {
		t.Error("Allowed = false, want true for noop")
	}
}

func TestSecurityGuard_ValidateInput(t *testing.T) {
	sg := noopSecurityGuard{}
	err := sg.ValidateInput(context.Background(), "hello")
	if err != nil {
		t.Errorf("ValidateInput err = %v, want nil", err)
	}
}

func TestSecurityAction_Values(t *testing.T) {
	if SecurityAllow != 0 {
		t.Errorf("SecurityAllow = %d, want 0", SecurityAllow)
	}
	if SecurityBlock != 1 {
		t.Errorf("SecurityBlock = %d, want 1", SecurityBlock)
	}
	if SecurityRequireApproval != 2 {
		t.Errorf("SecurityRequireApproval = %d, want 2", SecurityRequireApproval)
	}
	if SecuritySanitize != 3 {
		t.Errorf("SecuritySanitize = %d, want 3", SecuritySanitize)
	}
}

func TestSecurityCallInfo_Construct(t *testing.T) {
	info := SecurityCallInfo{
		ToolName: "exec",
		Arguments: map[string]any{"cmd": "rm -rf /"},
		SessionID: "s-1",
		UserID: "u-1",
	}
	if info.ToolName != "exec" {
		t.Errorf("ToolName = %q, want exec", info.ToolName)
	}
}

func TestSecurityDecision_Construct(t *testing.T) {
	dec := &SecurityDecision{
		Allowed: false,
		Reason: "dangerous command",
		Action: SecurityBlock,
	}
	if dec.Allowed {
		t.Error("Allowed = true, want false")
	}
	if dec.Action != SecurityBlock {
		t.Errorf("Action = %d, want %d", dec.Action, SecurityBlock)
	}
}

// ─── AuditLogger interface compliance ──────────────────────────────

func TestAuditLogger_Interface(t *testing.T) {
	var _ AuditLogger = (*noopAuditLogger)(nil)
}

func TestAuditLogger_LogToolCall(t *testing.T) {
	al := noopAuditLogger{}
	err := al.LogToolCall(context.Background(), AuditToolCallEvent{
		Timestamp: time.Now(),
		SessionID: "s-1",
		ToolName: "read_file",
		Arguments: map[string]any{"path": "/tmp"},
		Result: "ok",
		Approved: true,
		DecisionBy: "auto",
	})
	if err != nil {
		t.Errorf("LogToolCall err = %v, want nil", err)
	}
}

func TestAuditLogger_LogApproval(t *testing.T) {
	al := noopAuditLogger{}
	err := al.LogApproval(context.Background(), AuditApprovalEvent{
		Timestamp: time.Now(),
		SessionID: "s-1",
		ToolCallID: "tc-1",
		Approved: true,
		Reason: "safe operation",
		ApprovedBy: "user-1",
	})
	if err != nil {
		t.Errorf("LogApproval err = %v, want nil", err)
	}
}

func TestAuditLogger_LogDataAccess(t *testing.T) {
	al := noopAuditLogger{}
	err := al.LogDataAccess(context.Background(), AuditDataAccessEvent{
		Timestamp: time.Now(),
		SessionID: "s-1",
		DataType: "user_profile",
		Action: "read",
		Resource: "/api/users/123",
	})
	if err != nil {
		t.Errorf("LogDataAccess err = %v, want nil", err)
	}
}

func TestAuditLogger_Query(t *testing.T) {
	al := noopAuditLogger{}
	events, err := al.Query(context.Background(), AuditFilter{
		EventType: "tool_call",
		Limit: 10,
	})
	if err != nil {
		t.Errorf("Query err = %v, want nil", err)
	}
	if len(events) != 0 {
		t.Errorf("Query returned %d events, want 0 for noop", len(events))
	}
}

func TestAuditFilter_Construct(t *testing.T) {
	now := time.Now()
	f := AuditFilter{
		StartTime: &now,
		EndTime: nil,
		SessionID: "s-1",
		EventType: "tool_call",
		Limit: 50,
	}
	if f.Limit != 50 {
		t.Errorf("Limit = %d, want 50", f.Limit)
	}
	if f.StartTime == nil {
		t.Error("StartTime should not be nil")
	}
}

func TestAuditEvent_Construct(t *testing.T) {
	ev := AuditEvent{
		ID: "evt-1",
		Timestamp: time.Now(),
		Type: "tool_call",
		Data: map[string]any{"key": "value"},
	}
	if ev.ID != "evt-1" {
		t.Errorf("ID = %q, want evt-1", ev.ID)
	}
}

// ─── TelemetryCollector interface compliance ───────────────────────

func TestTelemetryCollector_Interface(t *testing.T) {
	var _ TelemetryCollector = (*noopTelemetryCollector)(nil)
}

func TestTelemetryCollector_RecordMetric(t *testing.T) {
	tc := noopTelemetryCollector{}
	err := tc.RecordMetric(context.Background(), MetricPoint{
		Name: "llm.latency",
		Value: 1.23,
		Tags: map[string]string{"model": "gpt-4o"},
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Errorf("RecordMetric err = %v, want nil", err)
	}
}

func TestTelemetryCollector_StartSpan(t *testing.T) {
	tc := noopTelemetryCollector{}
	ctx, span := tc.StartSpan(context.Background(), "tool.exec")
	if span == nil {
		t.Error("StartSpan returned nil span")
	}
	if ctx == nil {
		t.Error("StartSpan returned nil context")
	}
}

func TestTelemetryCollector_RecordError(t *testing.T) {
	tc := noopTelemetryCollector{}
	tc.RecordError(context.Background(), errTest) // should not panic
}

func TestSpan_Interface(t *testing.T) {
	var _ Span = (*noopSpan)(nil)
}

func TestMetricPoint_Construct(t *testing.T) {
	now := time.Now()
	mp := MetricPoint{
		Name: "tool.duration",
		Value: 0.5,
		Tags: map[string]string{"tool": "search"},
		Timestamp: now,
	}
	if mp.Name != "tool.duration" {
		t.Errorf("Name = %q, want tool.duration", mp.Name)
	}
	if mp.Value != 0.5 {
		t.Errorf("Value = %f, want 0.5", mp.Value)
	}
}

func TestSpanConfig_Construct(t *testing.T) {
	cfg := SpanConfig{
		Tags: map[string]any{"key": "value"},
		Parent: nil,
	}
	if cfg.Tags["key"] != "value" {
		t.Errorf("Tags[key] = %v, want value", cfg.Tags["key"])
	}
}

func TestErrorConfig_Construct(t *testing.T) {
	cfg := ErrorConfig{
		Tags: map[string]any{"severity": "high"},
		Span: nil,
	}
	if cfg.Tags["severity"] != "high" {
		t.Errorf("Tags[severity] = %v, want high", cfg.Tags["severity"])
	}
}

func TestSpanOption_FuncType(t *testing.T) {
	// Verify SpanOption can be constructed and applied
	opt := SpanOption(func(cfg *SpanConfig) {
		cfg.Tags = map[string]any{"from_option": true}
	})
	cfg := &SpanConfig{}
	opt(cfg)
	if !cfg.Tags["from_option"].(bool) {
		t.Error("SpanOption was not applied correctly")
	}
}

func TestErrorOption_FuncType(t *testing.T) {
	// Verify ErrorOption can be constructed and applied
	opt := ErrorOption(func(cfg *ErrorConfig) {
		cfg.Tags = map[string]any{"from_option": true}
	})
	cfg := &ErrorConfig{}
	opt(cfg)
	if !cfg.Tags["from_option"].(bool) {
		t.Error("ErrorOption was not applied correctly")
	}
}

// ─── noop implementations for interface compliance ─────────────────

var errTest = context.DeadlineExceeded

type noopCircuitBreaker struct{}

func (noopCircuitBreaker) Execute(_ context.Context, fn func(context.Context) error) error {
	return fn(context.Background())
}
func (noopCircuitBreaker) State() CircuitState { return CircuitClosed }
func (noopCircuitBreaker) Reset() {}

type noopLoopDetector struct{}

func (noopLoopDetector) Record(_ context.Context, _ ToolCallRecord) error { return nil }
func (noopLoopDetector) IsLoop(_ context.Context) bool { return false }
func (noopLoopDetector) Reset(_ context.Context) error { return nil }
func (noopLoopDetector) ConsecutiveCount(_ context.Context, _ string) int { return 0 }

type noopIdempotencyKey struct{}

func (noopIdempotencyKey) Check(_ context.Context, _ string) (*IdempotencyRecord, bool, error) {
	return nil, false, nil
}
func (noopIdempotencyKey) Record(_ context.Context, _ string, _ any) error { return nil }
func (noopIdempotencyKey) Delete(_ context.Context, _ string) error { return nil }

type noopSecurityGuard struct{}

func (noopSecurityGuard) ValidateToolCall(_ context.Context, _ SecurityCallInfo) (*SecurityDecision, error) {
	return &SecurityDecision{Allowed: true, Action: SecurityAllow}, nil
}
func (noopSecurityGuard) ValidateInput(_ context.Context, _ string) error { return nil }

type noopAuditLogger struct{}

func (noopAuditLogger) LogToolCall(_ context.Context, _ AuditToolCallEvent) error { return nil }
func (noopAuditLogger) LogApproval(_ context.Context, _ AuditApprovalEvent) error { return nil }
func (noopAuditLogger) LogDataAccess(_ context.Context, _ AuditDataAccessEvent) error { return nil }
func (noopAuditLogger) Query(_ context.Context, _ AuditFilter) ([]AuditEvent, error) {
	return nil, nil
}

type noopTelemetryCollector struct{}

func (noopTelemetryCollector) RecordMetric(_ context.Context, _ MetricPoint) error { return nil }
func (noopTelemetryCollector) StartSpan(ctx context.Context, _ string, _ ...SpanOption) (context.Context, Span) {
	return ctx, noopSpan{ctx: ctx}
}
func (noopTelemetryCollector) RecordError(_ context.Context, _ error, _ ...ErrorOption) {}

type noopSpan struct {
	ctx context.Context
}

func (noopSpan) End() {}
func (noopSpan) SetTag(_ string, _ any) {}
func (noopSpan) RecordError(_ error) {}
func (s noopSpan) Context() context.Context { return s.ctx }
