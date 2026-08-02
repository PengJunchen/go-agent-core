package auth

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// defaultRefreshBuffer 是触发刷新的默认提前量。
const defaultRefreshBuffer = 5 * time.Minute

// TokenRefresher 实现带提前刷新缓冲的 TokenSource。
// 当令牌距过期不足 buffer 时触发刷新，避免在过期边界处请求失败。
type TokenRefresher struct {
	mu sync.Mutex
	token *Token
	buffer time.Duration
	refreshFunc func(ctx context.Context) (*Token, error)
	credentialStore CredentialStore // 可选，用于持久化刷新后的令牌
	storeKey string // credentialStore 中的存储键
}

// NewTokenRefresher 构造一个 TokenRefresher，buffer 默认 5 分钟。
func NewTokenRefresher(initial *Token, refreshFunc func(ctx context.Context) (*Token, error)) *TokenRefresher {
	return &TokenRefresher{
		token: initial,
		buffer: defaultRefreshBuffer,
		refreshFunc: refreshFunc,
	}
}

// WithRefreshBuffer 设置刷新缓冲时间，返回自身以支持链式调用。
func (r *TokenRefresher) WithRefreshBuffer(d time.Duration) *TokenRefresher {
	r.buffer = d
	return r
}

// WithCredentialStore 配置凭证存储，刷新成功后将新令牌持久化。
func (r *TokenRefresher) WithCredentialStore(store CredentialStore, key string) *TokenRefresher {
	r.credentialStore = store
	r.storeKey = key
	return r
}

// Token 返回有效令牌。若令牌距过期不足 buffer 则触发刷新；
// 刷新成功后若配置了 CredentialStore 则持久化新令牌。
func (r *TokenRefresher) Token(ctx context.Context) (*Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 令牌未临近过期，直接返回
	if r.token != nil && time.Until(r.token.ExpiresAt) > r.buffer {
		return r.token, nil
	}

	// 令牌临近过期（或为空），执行刷新
	newToken, err := r.refreshFunc(ctx)
	if err != nil {
		return nil, err
	}
	r.token = newToken

	// 配置了凭证存储则持久化
	if r.credentialStore != nil && r.storeKey != "" {
		if saveErr := r.credentialStore.Save(ctx, r.storeKey, newToken); saveErr != nil {
			return newToken, fmt.Errorf("persist refreshed token: %w", saveErr)
		}
	}

	return newToken, nil
}
