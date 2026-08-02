package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// AT-001: StaticTokenSource returns fixed token.
func TestStaticTokenSource_ReturnsFixedToken(t *testing.T) {
	want := &Token{AccessToken: "static-key", TokenType: "Bearer"}
	src := NewStaticTokenSource(want)

	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v, want nil", err)
	}
	if got != want {
		t.Errorf("Token() = %p, want %p (same pointer)", got, want)
	}
	if got.AccessToken != "static-key" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "static-key")
	}
}

// AT-002: OAuthTokenSource refreshes expired token.
func TestOAuthTokenSource_RefreshesExpiredToken(t *testing.T) {
	expired := &Token{
		AccessToken: "old-access",
		RefreshToken: "refresh-me",
		ExpiresAt: time.Now().Add(-time.Hour), // already expired
		TokenType: "Bearer",
	}
	refreshed := &Token{
		AccessToken: "new-access",
		RefreshToken: "refresh-me-next",
		ExpiresAt: time.Now().Add(time.Hour),
		TokenType: "Bearer",
	}

	calls := 0
	refreshFunc := func(_ context.Context, refreshToken string) (*Token, error) {
		calls++
		if refreshToken != "refresh-me" {
			t.Errorf("refreshToken = %q, want %q", refreshToken, "refresh-me")
		}
		return refreshed, nil
	}

	src := NewOAuthTokenSource(expired, refreshFunc)
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v, want nil", err)
	}
	if got != refreshed {
		t.Errorf("Token() = %+v, want refreshed token", got)
	}
	if calls != 1 {
		t.Errorf("refreshFunc calls = %d, want 1", calls)
	}
}

// AT-003: OAuthTokenSource uses double-checked locking (concurrent calls).
func TestOAuthTokenSource_ConcurrentRefreshSingleCall(t *testing.T) {
	expired := &Token{
		AccessToken: "old-access",
		RefreshToken: "refresh-me",
		ExpiresAt: time.Now().Add(-time.Hour),
		TokenType: "Bearer",
	}

	var refreshCount int32
	refreshFunc := func(_ context.Context, _ string) (*Token, error) {
		atomic.AddInt32(&refreshCount, 1)
		// Simulate slow refresh to maximize chance of race
		time.Sleep(50 * time.Millisecond)
		return &Token{
			AccessToken: "new-access",
			RefreshToken: "refresh-me-next",
			ExpiresAt: time.Now().Add(time.Hour),
			TokenType: "Bearer",
		}, nil
	}

	src := NewOAuthTokenSource(expired, refreshFunc)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, _ = src.Token(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&refreshCount); got != 1 {
		t.Errorf("refreshFunc calls = %d, want 1 (double-checked locking should prevent duplicate refresh)", got)
	}
}

// AT-004: OAuthTokenSource doesn't refresh valid token.
func TestOAuthTokenSource_DoesNotRefreshValidToken(t *testing.T) {
	valid := &Token{
		AccessToken: "valid-access",
		RefreshToken: "refresh-me",
		ExpiresAt: time.Now().Add(time.Hour), // still valid
		TokenType: "Bearer",
	}

	calls := 0
	refreshFunc := func(_ context.Context, _ string) (*Token, error) {
		calls++
		return &Token{AccessToken: "should-not-happen"}, nil
	}

	src := NewOAuthTokenSource(valid, refreshFunc)
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v, want nil", err)
	}
	if got != valid {
		t.Errorf("Token() = %+v, want original valid token", got)
	}
	if calls != 0 {
		t.Errorf("refreshFunc calls = %d, want 0 (valid token should not trigger refresh)", calls)
	}
}

// AT-005: OAuthTokenSource propagates refresh error.
func TestOAuthTokenSource_RefreshErrorPropagated(t *testing.T) {
	expired := &Token{
		AccessToken: "old-access",
		RefreshToken: "refresh-me",
		ExpiresAt: time.Now().Add(-time.Hour),
		TokenType: "Bearer",
	}
	wantErr := errors.New("refresh failed")

	refreshFunc := func(_ context.Context, _ string) (*Token, error) {
		return nil, wantErr
	}

	src := NewOAuthTokenSource(expired, refreshFunc)
	_, err := src.Token(context.Background())
	if !errors.Is(err, wantErr) {
		t.Errorf("Token() err = %v, want %v", err, wantErr)
	}
}

// AT-006: FallbackTokenSource 主令牌源成功时返回主令牌。
func TestFallbackTokenSource_PrimarySucceeds(t *testing.T) {
	primaryTok := &Token{AccessToken: "primary-key", TokenType: "Bearer"}
	primary := NewStaticTokenSource(primaryTok)
	fallback := NewStaticTokenSource(&Token{AccessToken: "fallback-key"})

	src := NewFallbackTokenSource(primary, fallback)
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v, want nil", err)
	}
	if got.AccessToken != "primary-key" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "primary-key")
	}
}

// AT-007 (AC-3): 主令牌源失败时回退到备用令牌源。
func TestFallbackTokenSource_FallsBackOnError(t *testing.T) {
	primary := &errorTokenSource{err: errors.New("oauth refresh failed")}
	fallbackTok := &Token{AccessToken: "api-key-fallback", TokenType: "Bearer"}
	fallback := NewStaticTokenSource(fallbackTok)

	src := NewFallbackTokenSource(primary, fallback)
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v, want nil", err)
	}
	if got.AccessToken != "api-key-fallback" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "api-key-fallback")
	}
}

// AT-008: 主备令牌源都失败时返回备用令牌源的错误。
func TestFallbackTokenSource_BothFail(t *testing.T) {
	primaryErr := errors.New("primary failed")
	fallbackErr := errors.New("fallback failed")
	primary := &errorTokenSource{err: primaryErr}
	fallback := &errorTokenSource{err: fallbackErr}

	src := NewFallbackTokenSource(primary, fallback)
	_, err := src.Token(context.Background())
	if !errors.Is(err, fallbackErr) {
		t.Errorf("Token() err = %v, want %v", err, fallbackErr)
	}
}

// AT-009: FallbackTokenSource 实现 TokenSource 接口。
func TestFallbackTokenSource_ImplementsTokenSource(t *testing.T) {
	var _ TokenSource = (*FallbackTokenSource)(nil)
}

// errorTokenSource 总是返回错误的 TokenSource，用于测试。
type errorTokenSource struct {
	err error
}

func (s *errorTokenSource) Token(_ context.Context) (*Token, error) {
	return nil, s.err
}
