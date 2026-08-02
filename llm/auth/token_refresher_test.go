package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TR-001 (AC-2): 令牌距过期不足 5 分钟时触发刷新。
func TestTokenRefresher_RefreshesWithinBuffer(t *testing.T) {
	// 令牌 2 分钟后过期，在 5 分钟缓冲区内
	expiringSoon := &Token{
		AccessToken: "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(2 * time.Minute),
		TokenType: "Bearer",
	}
	refreshed := &Token{
		AccessToken: "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt: time.Now().Add(time.Hour),
		TokenType: "Bearer",
	}

	calls := int32(0)
	refreshFunc := func(_ context.Context) (*Token, error) {
		atomic.AddInt32(&calls, 1)
		return refreshed, nil
	}

	r := NewTokenRefresher(expiringSoon, refreshFunc)
	got, err := r.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v", err)
	}
	if got != refreshed {
		t.Errorf("Token() = %+v, want refreshed token", got)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("refreshFunc calls = %d, want 1", calls)
	}
}

// TR-002: 令牌距过期超过 buffer 时不刷新。
func TestTokenRefresher_NoRefreshOutsideBuffer(t *testing.T) {
	valid := &Token{
		AccessToken: "valid-access",
		RefreshToken: "valid-refresh",
		ExpiresAt: time.Now().Add(time.Hour), // 远超 5 分钟
		TokenType: "Bearer",
	}

	calls := int32(0)
	refreshFunc := func(_ context.Context) (*Token, error) {
		atomic.AddInt32(&calls, 1)
		return &Token{AccessToken: "should-not-happen"}, nil
	}

	r := NewTokenRefresher(valid, refreshFunc)
	got, err := r.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v", err)
	}
	if got != valid {
		t.Errorf("Token() = %+v, want original valid token", got)
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Errorf("refreshFunc calls = %d, want 0", calls)
	}
}

// TR-003: 刷新错误向上传播。
func TestTokenRefresher_RefreshErrorPropagated(t *testing.T) {
	expiringSoon := &Token{
		AccessToken: "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(2 * time.Minute),
		TokenType: "Bearer",
	}
	wantErr := errors.New("refresh failed")

	refreshFunc := func(_ context.Context) (*Token, error) {
		return nil, wantErr
	}

	r := NewTokenRefresher(expiringSoon, refreshFunc)
	_, err := r.Token(context.Background())
	if !errors.Is(err, wantErr) {
		t.Errorf("Token() err = %v, want %v", err, wantErr)
	}
}

// TR-004: 刷新成功后将新令牌持久化到 CredentialStore。
func TestTokenRefresher_PersistsToCredentialStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir)
	if err != nil {
		t.Fatalf("NewFileCredentialStore() err = %v", err)
	}

	expiringSoon := &Token{
		AccessToken: "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(2 * time.Minute),
		TokenType: "Bearer",
	}
	refreshed := &Token{
		AccessToken: "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt: time.Now().Add(time.Hour),
		TokenType: "Bearer",
	}

	refreshFunc := func(_ context.Context) (*Token, error) {
		return refreshed, nil
	}

	r := NewTokenRefresher(expiringSoon, refreshFunc).
		WithCredentialStore(store, "persist-key")

	got, err := r.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v", err)
	}
	if got != refreshed {
		t.Errorf("Token() = %+v, want refreshed", got)
	}

	// 验证令牌已持久化
	loaded, err := store.Load(context.Background(), "persist-key")
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if loaded.AccessToken != refreshed.AccessToken {
		t.Errorf("loaded AccessToken = %q, want %q", loaded.AccessToken, refreshed.AccessToken)
	}
}

// TR-005: 自定义 buffer 生效。
func TestTokenRefresher_CustomBuffer(t *testing.T) {
	// 令牌 10 分钟后过期，默认 buffer 5 分钟不刷新，
	// 但自定义 buffer 为 15 分钟时应刷新。
	token := &Token{
		AccessToken: "access",
		RefreshToken: "refresh",
		ExpiresAt: time.Now().Add(10 * time.Minute),
		TokenType: "Bearer",
	}
	refreshed := &Token{
		AccessToken: "refreshed",
		RefreshToken: "refreshed-r",
		ExpiresAt: time.Now().Add(time.Hour),
		TokenType: "Bearer",
	}

	calls := int32(0)
	refreshFunc := func(_ context.Context) (*Token, error) {
		atomic.AddInt32(&calls, 1)
		return refreshed, nil
	}

	// 默认 buffer (5min) → 不刷新
	r1 := NewTokenRefresher(token, refreshFunc)
	got, err := r1.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v", err)
	}
	if got != token {
		t.Errorf("with default buffer, expected original token")
	}

	// 自定义 buffer 15min → 触发刷新
	r2 := NewTokenRefresher(token, refreshFunc).WithRefreshBuffer(15 * time.Minute)
	got, err = r2.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v", err)
	}
	if got != refreshed {
		t.Errorf("with 15min buffer, expected refreshed token")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("refreshFunc calls = %d, want 1", calls)
	}
}

// TR-006: 初始令牌为 nil 时触发刷新。
func TestTokenRefresher_NilTokenTriggersRefresh(t *testing.T) {
	refreshed := &Token{
		AccessToken: "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt: time.Now().Add(time.Hour),
		TokenType: "Bearer",
	}

	calls := int32(0)
	refreshFunc := func(_ context.Context) (*Token, error) {
		atomic.AddInt32(&calls, 1)
		return refreshed, nil
	}

	r := NewTokenRefresher(nil, refreshFunc)
	got, err := r.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() err = %v", err)
	}
	if got != refreshed {
		t.Errorf("Token() = %+v, want refreshed", got)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("refreshFunc calls = %d, want 1", calls)
	}
}

// TR-007: TokenRefresher 实现 TokenSource 接口。
func TestTokenRefresher_ImplementsTokenSource(t *testing.T) {
	var _ TokenSource = (*TokenRefresher)(nil)
}

// TR-008: 并发调用安全（-race 验证）。
func TestTokenRefresher_ConcurrentSafe(t *testing.T) {
	expiringSoon := &Token{
		AccessToken: "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt: time.Now().Add(2 * time.Minute),
		TokenType: "Bearer",
	}

	var refreshCount int32
	refreshFunc := func(_ context.Context) (*Token, error) {
		atomic.AddInt32(&refreshCount, 1)
		time.Sleep(20 * time.Millisecond)
		return &Token{
			AccessToken: "new-access",
			RefreshToken: "new-refresh",
			ExpiresAt: time.Now().Add(time.Hour),
			TokenType: "Bearer",
		}, nil
	}

	r := NewTokenRefresher(expiringSoon, refreshFunc)

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_, _ = r.Token(context.Background())
		}()
	}
	wg.Wait()
}
