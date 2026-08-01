package production

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── DefaultCircuitBreaker tests ────────────────────────────────────

func TestDefaultCircuitBreaker_Interface(t *testing.T) {
	t.Parallel()
	// Compile-time check: DefaultCircuitBreaker must implement CircuitBreaker.
	var _ CircuitBreaker = (*DefaultCircuitBreaker)(nil)
}

func TestDefaultCircuitBreaker_InitialStateClosed(t *testing.T) {
	t.Parallel()
	cb := NewDefaultCircuitBreaker(DefaultCircuitBreakerConfig())
	if st := cb.State(); st != CircuitClosed {
		t.Errorf("State = %v, want CircuitClosed", st)
	}
}

func TestDefaultCircuitBreaker_ClosedPassThrough(t *testing.T) {
	t.Parallel()
	cb := NewDefaultCircuitBreaker(DefaultCircuitBreakerConfig())

	// Successful call
	err := cb.Execute(context.Background(), func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("Execute err = %v, want nil", err)
	}

	// Failing call — should propagate the error from fn
	fnErr := errors.New("fn error")
	err = cb.Execute(context.Background(), func(_ context.Context) error {
		return fnErr
	})
	if !errors.Is(err, fnErr) {
		t.Errorf("Execute err = %v, want %v", err, fnErr)
	}
}

func TestDefaultCircuitBreaker_TransitionToOpen(t *testing.T) {
	t.Parallel()
	cfg := CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	failErr := errors.New("fail")
	for i := 0; i < cfg.FailureThreshold; i++ {
		err := cb.Execute(context.Background(), func(_ context.Context) error {
			return failErr
		})
		if !errors.Is(err, failErr) {
			t.Errorf("Execute %d: err = %v, want %v", i, err, failErr)
		}
	}

	if st := cb.State(); st != CircuitOpen {
		t.Errorf("State after %d failures = %v, want CircuitOpen", cfg.FailureThreshold, st)
	}
}

func TestDefaultCircuitBreaker_OpenRejects(t *testing.T) {
	t.Parallel()
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})

	// fn should NOT be called when circuit is open
	called := false
	err := cb.Execute(context.Background(), func(_ context.Context) error {
		called = true
		return nil
	})
	if called {
		t.Error("fn was called while circuit is open, should be rejected")
	}
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Execute err = %v, want ErrCircuitOpen", err)
	}
}

func TestDefaultCircuitBreaker_TransitionToHalfOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})
	if cb.State() != CircuitOpen {
		t.Fatal("expected CircuitOpen")
	}

	// Wait for timeout
	time.Sleep(80 * time.Millisecond)

	// State should now be HalfOpen
	if st := cb.State(); st != CircuitHalfOpen {
		t.Errorf("State after timeout = %v, want CircuitHalfOpen", st)
	}
}

func TestDefaultCircuitBreaker_HalfOpenSuccessToClosed(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 3,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 3,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})

	// Wait for half-open
	time.Sleep(80 * time.Millisecond)

	// Send SuccessThreshold successes
	for i := 0; i < cfg.SuccessThreshold; i++ {
		err := cb.Execute(context.Background(), func(_ context.Context) error {
			return nil
		})
		if err != nil {
			t.Errorf("Execute %d in half-open: err = %v, want nil", i, err)
		}
	}

	if st := cb.State(); st != CircuitClosed {
		t.Errorf("State after %d successes = %v, want CircuitClosed", cfg.SuccessThreshold, st)
	}
}

func TestDefaultCircuitBreaker_HalfOpenFailureToOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 2,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})

	// Wait for half-open
	time.Sleep(80 * time.Millisecond)
	if cb.State() != CircuitHalfOpen {
		t.Fatal("expected CircuitHalfOpen")
	}

	// One failure in half-open should re-open
	err := cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail again")
	})
	if err == nil {
		t.Error("expected error from failing fn in half-open")
	}

	if st := cb.State(); st != CircuitOpen {
		t.Errorf("State after failure in half-open = %v, want CircuitOpen", st)
	}
}

func TestDefaultCircuitBreaker_HalfOpenMaxReqs(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 2,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})

	// Wait for half-open
	time.Sleep(80 * time.Millisecond)

	// First request should go through (uses the slot)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Long-running request to hold the slot
		_ = cb.Execute(context.Background(), func(_ context.Context) error {
			time.Sleep(200 * time.Millisecond)
			return nil
		})
	}()

	// Give the goroutine time to acquire the slot
	time.Sleep(20 * time.Millisecond)

	// Second request should be rejected (HalfOpenMaxReqs = 1, slot taken)
	err := cb.Execute(context.Background(), func(_ context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Second request in half-open: err = %v, want ErrCircuitOpen", err)
	}

	wg.Wait()
}

func TestDefaultCircuitBreaker_Reset(t *testing.T) {
	t.Parallel()
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})
	if cb.State() != CircuitOpen {
		t.Fatal("expected CircuitOpen")
	}

	// Reset
	cb.Reset()

	if st := cb.State(); st != CircuitClosed {
		t.Errorf("State after Reset = %v, want CircuitClosed", st)
	}

	// Should be able to execute again
	err := cb.Execute(context.Background(), func(_ context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("Execute after Reset: err = %v, want nil", err)
	}
}

func TestDefaultCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	t.Parallel()
	cfg := CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	failErr := errors.New("fail")

	// Two failures (below threshold)
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return failErr })
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return failErr })

	if cb.State() != CircuitClosed {
		t.Fatal("expected CircuitClosed after 2 failures with threshold 3")
	}

	// One success resets the failure count
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return nil })

	// Two more failures — should still be closed because count was reset
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return failErr })
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return failErr })

	if cb.State() != CircuitClosed {
		t.Errorf("State = %v, want CircuitClosed (failure count should have been reset by success)", cb.State())
	}

	// Third failure should now open the circuit
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return failErr })
	if cb.State() != CircuitOpen {
		t.Errorf("State after 3rd failure = %v, want CircuitOpen", cb.State())
	}
}

func TestDefaultCircuitBreaker_DefaultConfig(t *testing.T) {
	t.Parallel()
	cb := NewDefaultCircuitBreaker(CircuitBreakerConfig{})
	// Zero config should use defaults; verify by checking state and that
	// the circuit doesn't open after fewer than 5 failures.
	for i := 0; i < 4; i++ {
		_ = cb.Execute(context.Background(), func(_ context.Context) error {
			return errors.New("fail")
		})
	}
	if cb.State() != CircuitClosed {
		t.Errorf("State after 4 failures with default config = %v, want CircuitClosed", cb.State())
	}

	// 5th failure should open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})
	if cb.State() != CircuitOpen {
		t.Errorf("State after 5 failures with default config = %v, want CircuitOpen", cb.State())
	}
}

func TestDefaultCircuitBreaker_ErrCircuitOpenType(t *testing.T) {
	t.Parallel()
	// Verify ErrCircuitOpen can be checked with errors.Is
	if !errors.Is(ErrCircuitOpen, ErrCircuitOpen) {
		t.Error("errors.Is(ErrCircuitOpen, ErrCircuitOpen) = false, want true")
	}

	// Verify it's a named error (not just a string)
	var target *CircuitOpenError
	cfg := CircuitBreakerConfig{
		FailureThreshold: 1,
		SuccessThreshold: 1,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 1,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Trigger open
	_ = cb.Execute(context.Background(), func(_ context.Context) error {
		return errors.New("fail")
	})

	err := cb.Execute(context.Background(), func(_ context.Context) error {
		return nil
	})
	if !errors.As(err, &target) {
		t.Errorf("errors.As(err, &CircuitOpenError) = false, err = %v", err)
	}
}

func TestDefaultCircuitBreaker_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	cfg := CircuitBreakerConfig{
		FailureThreshold: 50,
		SuccessThreshold: 10,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 5,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	var wg sync.WaitGroup
	var successCount atomic.Int64
	var failCount atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := cb.Execute(context.Background(), func(_ context.Context) error {
				return nil
			})
			if err == nil {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}()
	}
	wg.Wait()

	// No assertions on exact counts — just ensuring no data races or panics
	_ = successCount.Load()
	_ = failCount.Load()
}

func TestDefaultCircuitBreaker_FullCycle(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 2,
		Timeout: 50 * time.Millisecond,
		HalfOpenMaxReqs: 2,
	}
	cb := NewDefaultCircuitBreaker(cfg)

	// Phase 1: Closed → Open
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return errors.New("fail1") })
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return errors.New("fail2") })
	if cb.State() != CircuitOpen {
		t.Fatalf("Phase 1: State = %v, want CircuitOpen", cb.State())
	}

	// Phase 2: Open → HalfOpen (wait for timeout)
	time.Sleep(80 * time.Millisecond)
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("Phase 2: State = %v, want CircuitHalfOpen", cb.State())
	}

	// Phase 3: HalfOpen → Closed (SuccessThreshold successes)
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return nil })
	_ = cb.Execute(context.Background(), func(_ context.Context) error { return nil })
	if cb.State() != CircuitClosed {
		t.Fatalf("Phase 3: State = %v, want CircuitClosed", cb.State())
	}

	// Phase 4: Verify circuit works normally again
	err := cb.Execute(context.Background(), func(_ context.Context) error { return nil })
	if err != nil {
		t.Errorf("Phase 4: Execute err = %v, want nil", err)
	}
}

func TestDefaultCircuitBreaker_CircuitStateString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state CircuitState
		want string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half-open"},
		{CircuitState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		got := tt.state.String()
		if got != tt.want {
			t.Errorf("CircuitState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
