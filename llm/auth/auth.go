// Package auth 提供 LLM Provider 的认证抽象。
//
// TokenSource 统一了 API Key（静态令牌）与 OAuth2（带刷新的动态令牌）
// 两种认证方式。Provider 通过 TokenSource 获取访问令牌，无需关心刷新细节。
package auth

import (
	"context"
	"sync"
	"time"
)

// Token 表示一个 OAuth 访问令牌。
type Token struct {
	AccessToken string
	RefreshToken string
	ExpiresAt time.Time
	TokenType string // "Bearer" 等
}

// IsExpired 在令牌过期时返回 true。
func (t *Token) IsExpired() bool {
	return time.Now().After(t.ExpiresAt)
}

// TokenSource 提供访问令牌，按需刷新。
type TokenSource interface {
	Token(ctx context.Context) (*Token, error)
}

// StaticTokenSource 返回固定令牌（用于 API Key 认证）。
type StaticTokenSource struct {
	token *Token
}

// NewStaticTokenSource 构造一个静态令牌源。
func NewStaticTokenSource(t *Token) *StaticTokenSource {
	return &StaticTokenSource{token: t}
}

// Token 返回固定的静态令牌。
func (s *StaticTokenSource) Token(_ context.Context) (*Token, error) {
	return s.token, nil
}

// OAuthTokenSource 实现 OAuth2 令牌刷新，使用双重检查锁定。
type OAuthTokenSource struct {
	mu sync.RWMutex
	token *Token
	refreshFunc func(ctx context.Context, refreshToken string) (*Token, error)
}

// NewOAuthTokenSource 构造一个带刷新能力的 TokenSource。
func NewOAuthTokenSource(initial *Token, refreshFunc func(ctx context.Context, refreshToken string) (*Token, error)) *OAuthTokenSource {
	return &OAuthTokenSource{token: initial, refreshFunc: refreshFunc}
}

// Token 返回有效令牌，必要时执行刷新。
// 使用双重检查锁定以提高效率：读锁用于快速路径检查，写锁用于刷新。
func (s *OAuthTokenSource) Token(ctx context.Context) (*Token, error) {
	// Fast path: read lock to check token validity
	s.mu.RLock()
	if s.token != nil && !s.token.IsExpired() {
		tok := s.token
		s.mu.RUnlock()
		return tok, nil
	}
	s.mu.RUnlock()

	// Slow path: write lock for refresh
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock
	if s.token != nil && !s.token.IsExpired() {
		return s.token, nil
	}

	// Refresh
	newToken, err := s.refreshFunc(ctx, s.token.RefreshToken)
	if err != nil {
		return nil, err
	}
	s.token = newToken
	return newToken, nil
}

// FallbackTokenSource 在主令牌源失败时回退到备用令牌源。
// 典型用法：primary 为 OAuth TokenSource，fallback 为静态 API Key，
// 当 OAuth 刷新失败时自动降级为 API Key 认证。
type FallbackTokenSource struct {
	primary TokenSource
	fallback TokenSource
}

// NewFallbackTokenSource 构造一个带回退能力的 TokenSource。
func NewFallbackTokenSource(primary, fallback TokenSource) *FallbackTokenSource {
	return &FallbackTokenSource{primary: primary, fallback: fallback}
}

// Token 先尝试主令牌源；若主令牌源返回错误，则使用备用令牌源。
func (s *FallbackTokenSource) Token(ctx context.Context) (*Token, error) {
	tok, err := s.primary.Token(ctx)
	if err != nil {
		return s.fallback.Token(ctx)
	}
	return tok, nil
}
