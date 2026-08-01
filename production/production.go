// Package production 定义生产化组件接口。
//
// 提供六大横切能力的接口抽象，遵循接口先行原则（D3），
// 所有接口零具体实现依赖，用户可通过依赖注入替换任何组件。
//
// 六大能力：
// - CircuitBreaker：熔断器，防止级联故障
// - LoopDetector：循环检测，防止无限工具调用循环
// - IdempotencyKey：幂等键，确保操作可安全重试
// - SecurityGuard：安全守卫，校验操作是否符合安全策略
// - AuditLogger：审计日志，记录合规相关事件
// - TelemetryCollector：遥测采集，收集指标和链路追踪
package production

import (
	"context"
	"time"
)

// ─── Circuit Breaker ───────────────────────────────────────────────

// CircuitBreaker protects against cascading failures by tracking error rates
// and preventing requests when the failure threshold is exceeded.
type CircuitBreaker interface {
	// Execute runs the given function through the circuit breaker.
	// Returns an error if the circuit is open or if fn returns an error.
	Execute(ctx context.Context, fn func(ctx context.Context) error) error
	// State returns the current circuit state.
	State() CircuitState
	// Reset manually resets the circuit to closed state.
	Reset()
}

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// CircuitClosed indicates normal operation — requests flow through.
	CircuitClosed CircuitState = iota
	// CircuitOpen indicates failing — requests are rejected.
	CircuitOpen
	// CircuitHalfOpen indicates testing — limited requests allowed to probe recovery.
	CircuitHalfOpen
)

// CircuitBreakerConfig configures circuit breaker behavior.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of failures before opening (default: 5).
	FailureThreshold int
	// SuccessThreshold is the number of successes before closing from half-open (default: 3).
	SuccessThreshold int
	// Timeout is the time before trying half-open (default: 30s).
	Timeout time.Duration
	// HalfOpenMaxReqs is the max requests allowed in half-open state (default: 1).
	HalfOpenMaxReqs int
}

// DefaultCircuitBreakerConfig returns a CircuitBreakerConfig with sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout: 30 * time.Second,
		HalfOpenMaxReqs: 1,
	}
}

// ─── Loop Detection ────────────────────────────────────────────────

// LoopDetector detects and prevents infinite tool call loops by analyzing
// call patterns across consecutive turns.
type LoopDetector interface {
	// Record records a tool call for loop analysis.
	Record(ctx context.Context, call ToolCallRecord) error
	// IsLoop returns true if a loop pattern is detected.
	IsLoop(ctx context.Context) bool
	// Reset clears the detection history.
	Reset(ctx context.Context) error
	// ConsecutiveCount returns how many consecutive identical calls were made.
	ConsecutiveCount(ctx context.Context, toolName string) int
}

// ToolCallRecord describes a single tool call for loop analysis.
type ToolCallRecord struct {
	ToolName string
	Arguments map[string]any
	Timestamp time.Time
}

// LoopDetectorConfig configures loop detection behavior.
type LoopDetectorConfig struct {
	// ConsecutiveThreshold is the number of same tool calls that signals a loop (default: 3).
	ConsecutiveThreshold int
	// WindowSize is the sliding window size for analysis (default: 10).
	WindowSize int
	// ArgumentComparison enables argument comparison in loop detection (default: true).
	ArgumentComparison bool
}

// DefaultLoopDetectorConfig returns a LoopDetectorConfig with sensible defaults.
func DefaultLoopDetectorConfig() LoopDetectorConfig {
	return LoopDetectorConfig{
		ConsecutiveThreshold: 3,
		WindowSize: 10,
		ArgumentComparison: true,
	}
}

// ─── Idempotency ───────────────────────────────────────────────────

// IdempotencyKey ensures operations can be safely retried by tracking
// previously completed operations and their results.
type IdempotencyKey interface {
	// Check checks if this operation was already executed.
	// Returns the record and true if found, nil and false otherwise.
	Check(ctx context.Context, key string) (*IdempotencyRecord, bool, error)
	// Record records a completed operation with its result.
	Record(ctx context.Context, key string, result any) error
	// Delete removes an idempotency record (for cleanup).
	Delete(ctx context.Context, key string) error
}

// IdempotencyRecord stores the result of a previously completed operation.
type IdempotencyRecord struct {
	Key string
	Result any
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ─── Security ──────────────────────────────────────────────────────

// SecurityGuard validates operations against security policies.
// It can block, allow, require approval, or sanitize operations.
type SecurityGuard interface {
	// ValidateToolCall validates a tool call against security policies.
	ValidateToolCall(ctx context.Context, call SecurityCallInfo) (*SecurityDecision, error)
	// ValidateInput validates user input for safety.
	ValidateInput(ctx context.Context, input string) error
}

// SecurityCallInfo describes a tool call for security validation.
type SecurityCallInfo struct {
	ToolName string
	Arguments map[string]any
	SessionID string
	UserID string
}

// SecurityDecision represents the outcome of a security validation.
type SecurityDecision struct {
	Allowed bool
	Reason string
	Action SecurityAction
}

// SecurityAction enumerates the possible security actions.
type SecurityAction int

const (
	// SecurityAllow permits the operation.
	SecurityAllow SecurityAction = iota
	// SecurityBlock rejects the operation.
	SecurityBlock
	// SecurityRequireApproval flags the operation for human approval.
	SecurityRequireApproval
	// SecuritySanitize allows the operation after sanitizing inputs.
	SecuritySanitize
)

// ─── Audit ─────────────────────────────────────────────────────────

// AuditLogger records security-relevant events for compliance and review.
type AuditLogger interface {
	// LogToolCall records a tool call audit event.
	LogToolCall(ctx context.Context, event AuditToolCallEvent) error
	// LogApproval records an approval decision.
	LogApproval(ctx context.Context, event AuditApprovalEvent) error
	// LogDataAccess records data access events.
	LogDataAccess(ctx context.Context, event AuditDataAccessEvent) error
	// Query queries audit events for compliance review.
	Query(ctx context.Context, filter AuditFilter) ([]AuditEvent, error)
}

// AuditToolCallEvent records a tool call for audit purposes.
type AuditToolCallEvent struct {
	Timestamp time.Time
	SessionID string
	ToolName string
	Arguments map[string]any
	Result string
	Approved bool
	DecisionBy string // "auto" or user ID
}

// AuditApprovalEvent records an approval decision for audit purposes.
type AuditApprovalEvent struct {
	Timestamp time.Time
	SessionID string
	ToolCallID string
	Approved bool
	Reason string
	ApprovedBy string
}

// AuditDataAccessEvent records a data access for audit purposes.
type AuditDataAccessEvent struct {
	Timestamp time.Time
	SessionID string
	DataType string
	Action string // "read", "write", "delete"
	Resource string
}

// AuditFilter constrains audit event queries.
type AuditFilter struct {
	StartTime *time.Time
	EndTime *time.Time
	SessionID string
	EventType string
	Limit int
}

// AuditEvent is a generic audit event returned by queries.
type AuditEvent struct {
	ID string
	Timestamp time.Time
	Type string
	Data any
}

// ─── Telemetry ─────────────────────────────────────────────────────

// TelemetryCollector collects metrics and traces for observability.
type TelemetryCollector interface {
	// RecordMetric records a metric data point.
	RecordMetric(ctx context.Context, metric MetricPoint) error
	// StartSpan starts a new trace span and returns a context with the span attached.
	StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)
	// RecordError records an error event with optional context.
	RecordError(ctx context.Context, err error, opts ...ErrorOption)
}

// MetricPoint represents a single metric measurement.
type MetricPoint struct {
	Name string
	Value float64
	Tags map[string]string
	Timestamp time.Time
}

// Span represents an in-progress trace span.
type Span interface {
	// End completes the span.
	End()
	// SetTag attaches a key-value tag to the span.
	SetTag(key string, value any)
	// RecordError records an error within the span.
	RecordError(err error)
	// Context returns the context associated with this span.
	Context() context.Context
}

// SpanOption configures span creation.
type SpanOption func(*SpanConfig)

// ErrorOption configures error recording.
type ErrorOption func(*ErrorConfig)

// SpanConfig holds configuration for span creation.
type SpanConfig struct {
	Tags map[string]any
	Parent Span
}

// ErrorConfig holds configuration for error recording.
type ErrorConfig struct {
	Tags map[string]any
	Span Span
}
