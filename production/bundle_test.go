package production

import (
	"strings"
	"testing"
)

func TestProductionBundle_NewEmpty(t *testing.T) {
	b := NewProductionBundle()
	if b == nil {
		t.Fatal("NewProductionBundle returned nil")
	}
	if b.LoopDetector != nil {
		t.Error("LoopDetector should be nil for empty bundle")
	}
	if b.CircuitBreaker != nil {
		t.Error("CircuitBreaker should be nil for empty bundle")
	}
	if b.TelemetryCollector != nil {
		t.Error("TelemetryCollector should be nil for empty bundle")
	}
	if b.SecurityGuard != nil {
		t.Error("SecurityGuard should be nil for empty bundle")
	}
	if b.AuditLogger != nil {
		t.Error("AuditLogger should be nil for empty bundle")
	}
	if b.IdempotencyKey != nil {
		t.Error("IdempotencyKey should be nil for empty bundle")
	}
}

func TestProductionBundle_WithLoopDetector(t *testing.T) {
	ld := noopLoopDetector{}
	b := NewProductionBundle(WithLoopDetector(ld))
	if b.LoopDetector == nil {
		t.Error("LoopDetector should not be nil")
	}
}

func TestProductionBundle_WithCircuitBreaker(t *testing.T) {
	cb := noopCircuitBreaker{}
	b := NewProductionBundle(WithCircuitBreaker(cb))
	if b.CircuitBreaker == nil {
		t.Error("CircuitBreaker should not be nil")
	}
}

func TestProductionBundle_WithTelemetryCollector(t *testing.T) {
	tc := noopTelemetryCollector{}
	b := NewProductionBundle(WithTelemetryCollector(tc))
	if b.TelemetryCollector == nil {
		t.Error("TelemetryCollector should not be nil")
	}
}

func TestProductionBundle_WithSecurityGuard(t *testing.T) {
	sg := noopSecurityGuard{}
	b := NewProductionBundle(WithSecurityGuard(sg))
	if b.SecurityGuard == nil {
		t.Error("SecurityGuard should not be nil")
	}
}

func TestProductionBundle_WithAuditLogger(t *testing.T) {
	al := noopAuditLogger{}
	b := NewProductionBundle(WithAuditLogger(al))
	if b.AuditLogger == nil {
		t.Error("AuditLogger should not be nil")
	}
}

func TestProductionBundle_WithIdempotencyKey(t *testing.T) {
	ik := noopIdempotencyKey{}
	b := NewProductionBundle(WithIdempotencyKey(ik))
	if b.IdempotencyKey == nil {
		t.Error("IdempotencyKey should not be nil")
	}
}

func TestProductionBundle_MultipleOptions(t *testing.T) {
	b := NewProductionBundle(
		WithLoopDetector(noopLoopDetector{}),
		WithCircuitBreaker(noopCircuitBreaker{}),
		WithAuditLogger(noopAuditLogger{}),
	)
	if b.LoopDetector == nil || b.CircuitBreaker == nil || b.AuditLogger == nil {
		t.Error("all set options should be non-nil")
	}
	if b.TelemetryCollector != nil || b.SecurityGuard != nil || b.IdempotencyKey != nil {
		t.Error("unset options should be nil")
	}
}

func TestProductionBundle_String(t *testing.T) {
	b := NewProductionBundle(WithLoopDetector(noopLoopDetector{}))
	s := b.String()
	if s == "" {
		t.Error("String should not be empty")
	}
	if !strings.Contains(s, "LoopDetector:true") {
		t.Errorf("String should contain LoopDetector:true, got %q", s)
	}
}

func TestProductionBundle_StringNil(t *testing.T) {
	var b *ProductionBundle
	s := b.String()
	if s != "ProductionBundle(nil)" {
		t.Errorf("String on nil = %q, want ProductionBundle(nil)", s)
	}
}
