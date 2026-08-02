package provider

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"
)

// ProviderError 表示 LLM Provider 返回的结构化错误。
// 包含足够信息供上层重试策略、日志和审计使用。
type ProviderError struct {
	ProviderName string // Provider 名称（如 "openai", "anthropic"）
	StatusCode int // HTTP 状态码
	Message string // 人类可读的错误消息
	RetryAfter time.Duration // 服务端建议的等待时间（0 表示未知）
	IsRetryable bool // 是否可安全重试
	Body []byte // 原始响应体（用于调试/审计）
}

// Error 实现 error 接口，输出 SDK 兼容的错误格式。
// 格式：provider:<ProviderName>: status <StatusCode>: <Message>
// 当 RetryAfter > 0 时追加 retry-after 信息。
func (e *ProviderError) Error() string {
	s := fmt.Sprintf("provider:%s: status %d: %s", e.ProviderName, e.StatusCode, e.Message)
	if e.RetryAfter > 0 {
		s += fmt.Sprintf(" (retry-after: %s)", e.RetryAfter)
	}
	return s
}

// retryablePatterns 列出消息中可重试的关键字正则。
var retryablePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\boverloaded\b`),
	regexp.MustCompile(`(?i)\boverload\b`),
	regexp.MustCompile(`(?i)\bconnection\s+refused\b`),
	regexp.MustCompile(`(?i)\bconnection\s+reset\b`),
	regexp.MustCompile(`(?i)\btimeout\b`),
	regexp.MustCompile(`(?i)\btemporarily\s+unavailable\b`),
	regexp.MustCompile(`(?i)\brate\s+limit\b`),
}

// retryableStatusCodes 是可重试的 HTTP 状态码集合。
var retryableStatusCodes = map[int]bool{
	429: true,
	500: true,
	502: true,
	503: true,
	504: true,
	529: true,
}

// IsRetryableAssistantError 判断错误是否可安全重试。
// 检查顺序：ProviderError.IsRetryable 标志 → HTTP 状态码 → 消息正则匹配 → 网络错误。
func IsRetryableAssistantError(err error) bool {
	if err == nil {
		return false
	}

	// 1. ProviderError 直接判断
	var pe *ProviderError
	if errors.As(err, &pe) {
		if pe.IsRetryable {
			return true
		}
		if retryableStatusCodes[pe.StatusCode] {
			return true
		}
		return matchRetryablePatterns(pe.Message)
	}

	// 2. 网络错误（net.OpError 等）
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// 3. 消息正则匹配
	return matchRetryablePatterns(err.Error())
}

// matchRetryablePatterns 检查消息是否匹配可重试关键字。
func matchRetryablePatterns(msg string) bool {
	for _, p := range retryablePatterns {
		if p.MatchString(msg) {
			return true
		}
	}
	return false
}

// ClassifyError 检查 error 并返回结构化的 ProviderError。
// 如果 err 本身就是 *ProviderError（包括被 fmt.Errorf %w 包装的），直接提取。
// 否则根据错误内容构造新的 ProviderError。
func ClassifyError(err error) *ProviderError {
	if err == nil {
		return nil
	}

	// 尝试解包 ProviderError
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe
	}

	// 构造新的 ProviderError
	classified := &ProviderError{
		ProviderName: "unknown",
		StatusCode: 0,
		Message: err.Error(),
		IsRetryable: IsRetryableAssistantError(err),
	}

	// 检查是否为网络错误
	var netErr net.Error
	if errors.As(err, &netErr) {
		classified.StatusCode = 0 // 网络层错误无 HTTP 状态码
		classified.ProviderName = "network"
	}

	return classified
}
