package production

import "fmt"

// ProductionBundle 聚合六大生产化组件为单一注入点。
//
// 所有字段均为可选（nil = 禁用该能力），
// DefaultLoopAgent 在 nil ProductionBundle 时保持默认行为不变。
type ProductionBundle struct {
	LoopDetector LoopDetector
	CircuitBreaker CircuitBreaker
	TelemetryCollector TelemetryCollector
	SecurityGuard SecurityGuard
	AuditLogger AuditLogger
	IdempotencyKey IdempotencyKey
}

// NewProductionBundle creates a ProductionBundle with the given options.
// Nil options are ignored; the corresponding field remains nil (disabled).
func NewProductionBundle(opts ...ProductionOption) *ProductionBundle {
	b := &ProductionBundle{}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// ProductionOption configures a ProductionBundle.
type ProductionOption func(*ProductionBundle)

// WithLoopDetector sets the LoopDetector.
func WithLoopDetector(ld LoopDetector) ProductionOption {
	return func(b *ProductionBundle) { b.LoopDetector = ld }
}

// WithCircuitBreaker sets the CircuitBreaker.
func WithCircuitBreaker(cb CircuitBreaker) ProductionOption {
	return func(b *ProductionBundle) { b.CircuitBreaker = cb }
}

// WithTelemetryCollector sets the TelemetryCollector.
func WithTelemetryCollector(tc TelemetryCollector) ProductionOption {
	return func(b *ProductionBundle) { b.TelemetryCollector = tc }
}

// WithSecurityGuard sets the SecurityGuard.
func WithSecurityGuard(sg SecurityGuard) ProductionOption {
	return func(b *ProductionBundle) { b.SecurityGuard = sg }
}

// WithAuditLogger sets the AuditLogger.
func WithAuditLogger(al AuditLogger) ProductionOption {
	return func(b *ProductionBundle) { b.AuditLogger = al }
}

// WithIdempotencyKey sets the IdempotencyKey.
func WithIdempotencyKey(ik IdempotencyKey) ProductionOption {
	return func(b *ProductionBundle) { b.IdempotencyKey = ik }
}

// String returns a human-readable summary of the bundle's configuration.
func (b *ProductionBundle) String() string {
	if b == nil {
		return "ProductionBundle(nil)"
	}
	return fmt.Sprintf("ProductionBundle{LoopDetector:%v, CircuitBreaker:%v, Telemetry:%v, Security:%v, Audit:%v, Idempotency:%v}",
		b.LoopDetector != nil, b.CircuitBreaker != nil, b.TelemetryCollector != nil,
		b.SecurityGuard != nil, b.AuditLogger != nil, b.IdempotencyKey != nil)
}
