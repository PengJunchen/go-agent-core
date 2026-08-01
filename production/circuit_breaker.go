package production

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when Execute is called while the circuit is open.
var ErrCircuitOpen = &CircuitOpenError{}

// CircuitOpenError is a typed error returned when the circuit breaker is open.
type CircuitOpenError struct{}

func (e *CircuitOpenError) Error() string { return "circuit breaker is open" }

// DefaultCircuitBreaker implements CircuitBreaker with a three-state machine.
type DefaultCircuitBreaker struct {
	mu sync.Mutex
	config CircuitBreakerConfig
	state CircuitState
	failureCount int
	successCount int
	halfOpenReqs int
	lastFailureTime time.Time
}

// NewDefaultCircuitBreaker creates a DefaultCircuitBreaker with the given config.
// If cfg has zero-valued fields, uses defaults from DefaultCircuitBreakerConfig.
func NewDefaultCircuitBreaker(cfg CircuitBreakerConfig) *DefaultCircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.HalfOpenMaxReqs <= 0 {
		cfg.HalfOpenMaxReqs = 1
	}
	return &DefaultCircuitBreaker{
		config: cfg,
		state: CircuitClosed,
	}
}

// Execute runs fn through the circuit breaker. Returns ErrCircuitOpen if the
// circuit is open, or the error from fn otherwise.
func (cb *DefaultCircuitBreaker) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	cb.mu.Lock()

	// Check if we should transition from Open to HalfOpen
	if cb.state == CircuitOpen {
		if time.Since(cb.lastFailureTime) >= cb.config.Timeout {
			cb.state = CircuitHalfOpen
			cb.successCount = 0
			cb.halfOpenReqs = 0
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
	}

	// In HalfOpen, limit concurrent requests
	if cb.state == CircuitHalfOpen {
		if cb.halfOpenReqs >= cb.config.HalfOpenMaxReqs {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
		cb.halfOpenReqs++
	}

	cb.mu.Unlock()

	// Execute the function outside the lock
	err := fn(ctx)

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.onFailure()
		return err
	}

	cb.onSuccess()
	return nil
}

// State returns the current circuit state, checking for automatic Open→HalfOpen transition.
func (cb *DefaultCircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Check for automatic Open→HalfOpen transition
	if cb.state == CircuitOpen && time.Since(cb.lastFailureTime) >= cb.config.Timeout {
		cb.state = CircuitHalfOpen
		cb.successCount = 0
		cb.halfOpenReqs = 0
	}

	return cb.state
}

// Reset transitions the circuit to Closed and clears all counters.
func (cb *DefaultCircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failureCount = 0
	cb.successCount = 0
	cb.halfOpenReqs = 0
}

func (cb *DefaultCircuitBreaker) onFailure() {
	switch cb.state {
	case CircuitClosed:
		cb.failureCount++
		if cb.failureCount >= cb.config.FailureThreshold {
			cb.state = CircuitOpen
			cb.lastFailureTime = time.Now()
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.lastFailureTime = time.Now()
		cb.halfOpenReqs = 0
	}
}

func (cb *DefaultCircuitBreaker) onSuccess() {
	switch cb.state {
	case CircuitHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.config.SuccessThreshold {
			cb.state = CircuitClosed
			cb.failureCount = 0
			cb.successCount = 0
			cb.halfOpenReqs = 0
		}
	case CircuitClosed:
		cb.failureCount = 0
	}
}

// String implements fmt.Stringer for CircuitState.
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}
