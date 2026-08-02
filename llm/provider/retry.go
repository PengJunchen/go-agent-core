package provider

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"strconv"
	"time"
)

// RetryConfig 配置重试策略。
type RetryConfig struct {
	MaxRetries int // 最大重试次数（不含首次调用）
	InitialBackoff time.Duration // 首次重试的退避时间
	MaxBackoff time.Duration // 退避时间上限
	BackoffMultiplier float64 // 退避乘数（指数增长因子）
	Jitter bool // 是否添加随机抖动
}

// DefaultRetryConfig 返回合理的默认重试配置。
// MaxRetries=3, InitialBackoff=1s, MaxBackoff=30s, BackoffMultiplier=2.0, Jitter=true
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff: 30 * time.Second,
		BackoffMultiplier: 2.0,
		Jitter: true,
	}
}

// RetryableFunc 是可被重试的函数签名。
type RetryableFunc func(ctx context.Context) error

// OnRetryCallback 在每次重试前被调用，接收 attempt（从 1 开始）、
// 触发重试的错误和即将等待的退避时间。
type OnRetryCallback func(attempt int, err error, nextBackoff time.Duration)

// RetryWithConfig 按照给定配置执行双层重试：
// - 传输层：网络错误（connection refused、timeout 等）
// - 业务层：API 错误（429/500/503、overloaded 等）
//
// 重试使用指数退避（可选抖动），尊重 ProviderError.RetryAfter，
// 支持上下文取消。
func RetryWithConfig(ctx context.Context, config RetryConfig, fn RetryableFunc, onRetry ...OnRetryCallback) error {
	// 检查上下文是否已取消
	if err := ctx.Err(); err != nil {
		return err
	}

	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		// 检查上下文
		if err := ctx.Err(); err != nil {
			return err
		}

		// 执行函数
		err := fn(ctx)
		if err == nil {
			return nil
		}

		lastErr = err

		// 判断是否可重试
		if !IsRetryableAssistantError(err) {
			return err
		}

		// 已达最大重试次数，不再重试
		if attempt >= config.MaxRetries {
			return err
		}

		// 计算退避时间
		backoff := computeBackoff(config, attempt)
		// 如果错误包含 RetryAfter，取较大值
		if pe := extractProviderError(err); pe != nil && pe.RetryAfter > 0 {
			if pe.RetryAfter > backoff {
				backoff = pe.RetryAfter
			}
		}

		// 调用回调（在等待之前）
		for _, cb := range onRetry {
			if cb != nil {
				cb(attempt+1, err, backoff)
			}
		}

		// 等待退避时间，尊重上下文取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return lastErr
}

// computeBackoff 计算第 attempt 次重试的退避时间（attempt 从 0 开始）。
func computeBackoff(config RetryConfig, attempt int) time.Duration {
	backoff := float64(config.InitialBackoff) * math.Pow(config.BackoffMultiplier, float64(attempt))
	if backoff > float64(config.MaxBackoff) {
		backoff = float64(config.MaxBackoff)
	}
	if config.Jitter && backoff > 0 {
		// 添加 [0, backoff) 的随机抖动
		jitter := rand.Float64() * backoff
		// 抖动后取 [backoff/2, backoff*1.5) 范围
		// 简单实现：取 backoff 的 50%-150%
		backoff = backoff*0.5 + jitter
		if backoff > float64(config.MaxBackoff) {
			backoff = float64(config.MaxBackoff)
		}
	}
	return time.Duration(backoff)
}

// extractProviderError 尝试从 error 中提取 *ProviderError。
func extractProviderError(err error) *ProviderError {
	if err == nil {
		return nil
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe
	}
	return nil
}

// ParseRetryAfter 解析 HTTP Retry-After 响应头。
// 支持两种格式：
// - delta-seconds：如 "120"
// - HTTP-date：如 "Wed, 21 Oct 2026 07:28:00 GMT"（RFC1123）
//
// 无效或过去的日期返回 0。
func ParseRetryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}

	// 尝试解析为 delta-seconds
	if seconds, err := strconv.Atoi(header); err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}

	// 尝试解析为 HTTP-date（RFC1123）
	parseFormats := []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
	}
	var parsedTime time.Time
	for _, layout := range parseFormats {
		if t, err := time.Parse(layout, header); err == nil {
			parsedTime = t
			break
		}
	}
	if parsedTime.IsZero() {
		return 0
	}

	// 计算距离现在的剩余时间
	duration := time.Until(parsedTime)
	if duration < 0 {
		return 0
	}
	return duration
}
