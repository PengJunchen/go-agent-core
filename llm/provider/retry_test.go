package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DefaultRetryConfig
// ---------------------------------------------------------------------------

// RC-001: DefaultRetryConfig returns sensible values.
func TestDefaultRetryConfig_Values(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("InitialBackoff = %v, want 1s", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", cfg.MaxBackoff)
	}
	if cfg.BackoffMultiplier != 2.0 {
		t.Errorf("BackoffMultiplier = %v, want 2.0", cfg.BackoffMultiplier)
	}
	if !cfg.Jitter {
		t.Error("Jitter = false, want true")
	}
}

// ---------------------------------------------------------------------------
// AC-3: ParseRetryAfter
// ---------------------------------------------------------------------------

// PA-001: ParseRetryAfter parses delta-seconds format.
func TestParseRetryAfter_DeltaSeconds(t *testing.T) {
	d := ParseRetryAfter("120")
	if d != 120*time.Second {
		t.Errorf("ParseRetryAfter(%q) = %v, want %v", "120", d, 120*time.Second)
	}
}

// PA-002: ParseRetryAfter parses small delta-seconds.
func TestParseRetryAfter_SmallDelta(t *testing.T) {
	d := ParseRetryAfter("5")
	if d != 5*time.Second {
		t.Errorf("ParseRetryAfter(%q) = %v, want %v", "5", d, 5*time.Second)
	}
}

// PA-003: ParseRetryAfter parses HTTP-date format.
func TestParseRetryAfter_HTTPDate(t *testing.T) {
	// Use a fixed future date relative to now
	future := time.Now().Add(10 * time.Second).UTC().Format(time.RFC1123)
	d := ParseRetryAfter(future)
	// Should be approximately 10s (allow some tolerance for execution time)
	if d < 5*time.Second || d > 15*time.Second {
		t.Errorf("ParseRetryAfter(HTTP-date) = %v, want ~10s", d)
	}
}

// PA-004: ParseRetryAfter returns 0 for invalid input.
func TestParseRetryAfter_Invalid(t *testing.T) {
	d := ParseRetryAfter("not-a-date")
	if d != 0 {
		t.Errorf("ParseRetryAfter(%q) = %v, want 0", "not-a-date", d)
	}
}

// PA-005: ParseRetryAfter returns 0 for empty string.
func TestParseRetryAfter_Empty(t *testing.T) {
	d := ParseRetryAfter("")
	if d != 0 {
		t.Errorf("ParseRetryAfter(%q) = %v, want 0", "", d)
	}
}

// PA-006: ParseRetryAfter handles past HTTP-date as 0.
func TestParseRetryAfter_PastHTTPDate(t *testing.T) {
	past := time.Now().Add(-10 * time.Second).UTC().Format(time.RFC1123)
	d := ParseRetryAfter(past)
	if d != 0 {
		t.Errorf("ParseRetryAfter(past date) = %v, want 0", d)
	}
}

// ---------------------------------------------------------------------------
// AC-2: Double-layer retry — transport layer (network) + business layer (API)
// ---------------------------------------------------------------------------

// RT-001: Retry succeeds after transient network error (transport layer retry).
func TestRetryWithConfig_TransportLayerRetry(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("RetryWithConfig err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

// RT-002: Retry succeeds after transient API error (business layer retry — 429).
func TestRetryWithConfig_BusinessLayerRetry429(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			return &ProviderError{StatusCode: 429, Message: "rate limit", IsRetryable: true}
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("RetryWithConfig err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

// RT-003: Retry succeeds after transient API error (business layer retry — 503).
func TestRetryWithConfig_BusinessLayerRetry503(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return &ProviderError{StatusCode: 503, Message: "service unavailable", IsRetryable: true}
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("RetryWithConfig err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

// RT-004: Non-retryable error returns immediately without retry.
func TestRetryWithConfig_NonRetryableNoRetry(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}
	var calls int32
	wantErr := &ProviderError{StatusCode: 400, Message: "bad request", IsRetryable: false}
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return wantErr
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1 (no retries for non-retryable)", got)
	}
}

// RT-005: Exhausted retries returns last error.
func TestRetryWithConfig_ExhaustedRetries(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	wantErr := &ProviderError{StatusCode: 503, Message: "service unavailable", IsRetryable: true}
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return wantErr
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	// 1 initial + 2 retries = 3 total
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

// RT-006: Success on first try — no backoff, no callback.
func TestRetryWithConfig_SuccessFirstTry(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}
	var calls int32
	var callbackCalls int32
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	onRetry := func(attempt int, err error, nextBackoff time.Duration) {
		atomic.AddInt32(&callbackCalls, 1)
	}
	err := RetryWithConfig(context.Background(), cfg, fn, onRetry)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&callbackCalls); got != 0 {
		t.Errorf("callbackCalls = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// AC-3: Retry-After header parsing and waiting
// ---------------------------------------------------------------------------

// RT-007: RetryAfter duration from ProviderError is respected (waits at least that long).
func TestRetryWithConfig_RespectsRetryAfter(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 1,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 10 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	start := time.Now()
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &ProviderError{
				StatusCode: 429,
				Message: "rate limit",
				RetryAfter: 50 * time.Millisecond,
				IsRetryable: true,
			}
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	// Should wait at least 50ms for RetryAfter
	if elapsed < 50*time.Millisecond {
		t.Errorf("elapsed = %v, want >= 50ms (RetryAfter respected)", elapsed)
	}
}

// RT-008: When RetryAfter exceeds MaxBackoff, use RetryAfter if it's the server directive.
func TestRetryWithConfig_RetryAfterOverridesBackoff(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 1,
		InitialBackoff: 100 * time.Millisecond, // larger than RetryAfter
		MaxBackoff: 200 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	start := time.Now()
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &ProviderError{
				StatusCode: 429,
				Message: "rate limit",
				RetryAfter: 20 * time.Millisecond,
				IsRetryable: true,
			}
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	// RetryAfter (20ms) is smaller than computed backoff (100ms), so should
	// use the larger one but still be reasonable
	if elapsed < 20*time.Millisecond {
		t.Errorf("elapsed = %v, should wait at least RetryAfter (20ms)", elapsed)
	}
}

// ---------------------------------------------------------------------------
// AC-5: OnRetry callback
// ---------------------------------------------------------------------------

// RT-009: OnRetry callback is called with attempt number, error, and next backoff.
func TestRetryWithConfig_OnRetryCallback(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 5 * time.Millisecond,
		MaxBackoff: 20 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	type callbackInfo struct {
		attempt int
		errIsNotNil bool
		backoff time.Duration
	}
	var mu sync.Mutex
	var callbacks []callbackInfo

	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return &ProviderError{StatusCode: 503, Message: "unavailable", IsRetryable: true}
		}
		return nil
	}
	onRetry := func(attempt int, err error, nextBackoff time.Duration) {
		mu.Lock()
		callbacks = append(callbacks, callbackInfo{
			attempt: attempt,
			errIsNotNil: err != nil,
			backoff: nextBackoff,
		})
		mu.Unlock()
	}
	err := RetryWithConfig(context.Background(), cfg, fn, onRetry)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(callbacks) != 2 {
		t.Fatalf("callbacks = %d, want 2", len(callbacks))
	}
	// First callback: attempt=1
	if callbacks[0].attempt != 1 {
		t.Errorf("callbacks[0].attempt = %d, want 1", callbacks[0].attempt)
	}
	if !callbacks[0].errIsNotNil {
		t.Error("callbacks[0].err should not be nil")
	}
	if callbacks[0].backoff <= 0 {
		t.Errorf("callbacks[0].backoff = %v, want > 0", callbacks[0].backoff)
	}
	// Second callback: attempt=2, backoff should be larger (exponential)
	if callbacks[1].attempt != 2 {
		t.Errorf("callbacks[1].attempt = %d, want 2", callbacks[1].attempt)
	}
	if callbacks[1].backoff <= 0 {
		t.Errorf("callbacks[1].backoff = %v, want > 0", callbacks[1].backoff)
	}
	// Exponential: second backoff >= first backoff (without jitter)
	if callbacks[1].backoff < callbacks[0].backoff {
		t.Errorf("callbacks[1].backoff (%v) < callbacks[0].backoff (%v), expected exponential growth",
			callbacks[1].backoff, callbacks[0].backoff)
	}
}

// RT-010: Multiple OnRetry callbacks are all invoked.
func TestRetryWithConfig_MultipleCallbacks(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 1,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	var cb1, cb2 int32
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return &ProviderError{StatusCode: 429, Message: "rate limit", IsRetryable: true}
		}
		return nil
	}
	onRetry1 := func(int, error, time.Duration) { atomic.AddInt32(&cb1, 1) }
	onRetry2 := func(int, error, time.Duration) { atomic.AddInt32(&cb2, 1) }
	err := RetryWithConfig(context.Background(), cfg, fn, onRetry1, onRetry2)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&cb1); got != 1 {
		t.Errorf("cb1 = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&cb2); got != 1 {
		t.Errorf("cb2 = %d, want 1", got)
	}
}

// RT-011: OnRetry callback receives the original error.
func TestRetryWithConfig_OnRetryCallbackReceivesError(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 1,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	var receivedErr error
	var mu sync.Mutex
	wantErr := &ProviderError{StatusCode: 429, Message: "rate limit", IsRetryable: true}
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return wantErr
		}
		return nil
	}
	onRetry := func(attempt int, err error, nextBackoff time.Duration) {
		mu.Lock()
		receivedErr = err
		mu.Unlock()
	}
	err := RetryWithConfig(context.Background(), cfg, fn, onRetry)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !errors.Is(receivedErr, wantErr) {
		t.Errorf("receivedErr = %v, want %v", receivedErr, wantErr)
	}
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

// RT-012: Context cancellation stops retry loop.
func TestRetryWithConfig_ContextCancellation(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 10,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var calls int32
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return &ProviderError{StatusCode: 503, Message: "unavailable", IsRetryable: true}
	}
	err := RetryWithConfig(ctx, cfg, fn)
	if err == nil {
		t.Fatal("err = nil, want non-nil (context cancelled)")
	}
	// Should not have made many calls
	if got := atomic.LoadInt32(&calls); got > 3 {
		t.Errorf("calls = %d, should be small (context cancelled)", got)
	}
}

// RT-013: Context already cancelled returns immediately.
func TestRetryWithConfig_ContextAlreadyCancelled(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls int32
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	err := RetryWithConfig(ctx, cfg, fn)
	if err == nil {
		t.Fatal("err = nil, want context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("calls = %d, want 0 (context already cancelled)", got)
	}
}

// ---------------------------------------------------------------------------
// Exponential backoff
// ---------------------------------------------------------------------------

// RT-014: Exponential backoff grows between retries (no jitter).
func TestRetryWithConfig_ExponentialBackoffGrowth(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	var backoffs []time.Duration
	var mu sync.Mutex
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return &ProviderError{StatusCode: 503, Message: "unavailable", IsRetryable: true}
	}
	onRetry := func(attempt int, err error, nextBackoff time.Duration) {
		mu.Lock()
		backoffs = append(backoffs, nextBackoff)
		mu.Unlock()
	}
	_ = RetryWithConfig(context.Background(), cfg, fn, onRetry)
	mu.Lock()
	defer mu.Unlock()
	if len(backoffs) != 3 {
		t.Fatalf("backoffs = %d, want 3", len(backoffs))
	}
	// backoffs[0] should be ~10ms, backoffs[1] ~20ms, backoffs[2] ~40ms
	expected := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}
	for i, want := range expected {
		got := backoffs[i]
		if got < want-2*time.Millisecond || got > want+2*time.Millisecond {
			t.Errorf("backoffs[%d] = %v, want ~%v", i, got, want)
		}
	}
}

// RT-015: Backoff is capped at MaxBackoff.
func TestRetryWithConfig_BackoffCappedAtMax(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 4,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff: 80 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	var backoffs []time.Duration
	var mu sync.Mutex
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return &ProviderError{StatusCode: 503, Message: "unavailable", IsRetryable: true}
	}
	onRetry := func(attempt int, err error, nextBackoff time.Duration) {
		mu.Lock()
		backoffs = append(backoffs, nextBackoff)
		mu.Unlock()
	}
	_ = RetryWithConfig(context.Background(), cfg, fn, onRetry)
	mu.Lock()
	defer mu.Unlock()
	for i, b := range backoffs {
		if b > cfg.MaxBackoff {
			t.Errorf("backoffs[%d] = %v, exceeds MaxBackoff %v", i, b, cfg.MaxBackoff)
		}
	}
}

// ---------------------------------------------------------------------------
// Jitter
// ---------------------------------------------------------------------------

// RT-016: Jitter adds randomness but stays within bounds.
func TestRetryWithConfig_JitterWithinBounds(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff: 100 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: true,
	}
	var calls int32
	var backoffs []time.Duration
	var mu sync.Mutex
	fn := func(ctx context.Context) error {
		atomic.AddInt32(&calls, 1)
		return &ProviderError{StatusCode: 503, Message: "unavailable", IsRetryable: true}
	}
	onRetry := func(attempt int, err error, nextBackoff time.Duration) {
		mu.Lock()
		backoffs = append(backoffs, nextBackoff)
		mu.Unlock()
	}
	_ = RetryWithConfig(context.Background(), cfg, fn, onRetry)
	mu.Lock()
	defer mu.Unlock()
	for i, b := range backoffs {
		if b <= 0 {
			t.Errorf("backoffs[%d] = %v, must be > 0 with jitter", i, b)
		}
		if b > cfg.MaxBackoff {
			t.Errorf("backoffs[%d] = %v, exceeds MaxBackoff %v", i, b, cfg.MaxBackoff)
		}
	}
}

// ---------------------------------------------------------------------------
// AC-6: go test -race passes (concurrent safety)
// ---------------------------------------------------------------------------

// RT-017: Concurrent retries are safe (race detector).
func TestRetryWithConfig_ConcurrentSafety(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: true,
	}
	var wg sync.WaitGroup
	const goroutines = 20
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var calls int32
			fn := func(ctx context.Context) error {
				n := atomic.AddInt32(&calls, 1)
				if n == 1 {
					return &ProviderError{StatusCode: 429, Message: "rate limit", IsRetryable: true}
				}
				return nil
			}
			_ = RetryWithConfig(context.Background(), cfg, fn)
		}()
	}
	wg.Wait()
}

// RT-018: OnRetry callback is safe under concurrency (race detector).
func TestRetryWithConfig_ConcurrentCallbackSafety(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: true,
	}
	var mu sync.Mutex
	count := 0
	onRetry := func(int, error, time.Duration) {
		mu.Lock()
		count++
		mu.Unlock()
	}
	var wg sync.WaitGroup
	const goroutines = 10
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var calls int32
			fn := func(ctx context.Context) error {
				n := atomic.AddInt32(&calls, 1)
				if n < 3 {
					return &ProviderError{StatusCode: 503, Message: "unavailable", IsRetryable: true}
				}
				return nil
			}
			_ = RetryWithConfig(context.Background(), cfg, fn, onRetry)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if count == 0 {
		t.Error("count = 0, expected some callback invocations")
	}
}

// RT-019: Transport error (connection refused) triggers retry, then succeeds.
func TestRetryWithConfig_ConnectionRefusedThenSuccess(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			return fmt.Errorf("dial tcp: connection refused")
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

// RT-020: Overloaded message error triggers retry.
func TestRetryWithConfig_OverloadedMessageRetry(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries: 2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff: 5 * time.Millisecond,
		BackoffMultiplier: 2.0,
		Jitter: false,
	}
	var calls int32
	fn := func(ctx context.Context) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return errors.New("API is overloaded")
		}
		return nil
	}
	err := RetryWithConfig(context.Background(), cfg, fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}
