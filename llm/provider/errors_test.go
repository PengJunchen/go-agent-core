package provider

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// AC-4: ProviderError Error() — SDK-compatible error format
// ---------------------------------------------------------------------------

// PE-001: ProviderError.Error() includes provider name, status code, and message.
func TestProviderError_ErrorFormat(t *testing.T) {
	pe := &ProviderError{
		ProviderName: "openai",
		StatusCode: 429,
		Message: "rate limit exceeded",
		RetryAfter: 5 * time.Second,
		IsRetryable: true,
	}
	msg := pe.Error()
	if msg == "" {
		t.Fatal("Error() returned empty string")
	}
	// Must contain provider name and status code
	if !contains(msg, "openai") {
		t.Errorf("Error() = %q, want containing %q", msg, "openai")
	}
	if !contains(msg, "429") {
		t.Errorf("Error() = %q, want containing %q", msg, "429")
	}
	if !contains(msg, "rate limit exceeded") {
		t.Errorf("Error() = %q, want containing %q", msg, "rate limit exceeded")
	}
}

// PE-002: ProviderError.Error() includes RetryAfter when non-zero.
func TestProviderError_ErrorWithRetryAfter(t *testing.T) {
	pe := &ProviderError{
		ProviderName: "anthropic",
		StatusCode: 503,
		Message: "service unavailable",
		RetryAfter: 30 * time.Second,
		IsRetryable: true,
	}
	msg := pe.Error()
	if !contains(msg, "30") {
		t.Errorf("Error() = %q, want containing retry-after duration info", msg)
	}
}

// PE-003: ProviderError.Error() works without RetryAfter.
func TestProviderError_ErrorWithoutRetryAfter(t *testing.T) {
	pe := &ProviderError{
		ProviderName: "gemini",
		StatusCode: 400,
		Message: "bad request",
	}
	msg := pe.Error()
	if !contains(msg, "gemini") || !contains(msg, "400") {
		t.Errorf("Error() = %q, want containing provider and status code", msg)
	}
}

// PE-004: ProviderError implements error interface.
func TestProviderError_ImplementsError(t *testing.T) {
	var err error = &ProviderError{
		ProviderName: "openai",
		StatusCode: 500,
		Message: "internal error",
	}
	if err.Error() == "" {
		t.Error("ProviderError must implement error interface")
	}
}

// PE-005: ProviderError with Body retains raw response bytes.
func TestProviderError_BodyField(t *testing.T) {
	body := []byte(`{"error": "overloaded"}`)
	pe := &ProviderError{
		ProviderName: "anthropic",
		StatusCode: 529,
		Message: "overloaded",
		Body: body,
	}
	if string(pe.Body) != string(body) {
		t.Errorf("Body = %q, want %q", pe.Body, body)
	}
}

// ---------------------------------------------------------------------------
// AC-1: IsRetryableAssistantError — regex-based classification
// ---------------------------------------------------------------------------

// RE-001: HTTP 429 is retryable.
func TestIsRetryable_HTTP429(t *testing.T) {
	err := &ProviderError{StatusCode: 429, Message: "rate limit exceeded", IsRetryable: true}
	if !IsRetryableAssistantError(err) {
		t.Error("429 error should be retryable")
	}
}

// RE-002: HTTP 500 is retryable.
func TestIsRetryable_HTTP500(t *testing.T) {
	err := &ProviderError{StatusCode: 500, Message: "internal server error", IsRetryable: true}
	if !IsRetryableAssistantError(err) {
		t.Error("500 error should be retryable")
	}
}

// RE-003: HTTP 503 is retryable.
func TestIsRetryable_HTTP503(t *testing.T) {
	err := &ProviderError{StatusCode: 503, Message: "service unavailable", IsRetryable: true}
	if !IsRetryableAssistantError(err) {
		t.Error("503 error should be retryable")
	}
}

// RE-004: "overloaded" in message is retryable.
func TestIsRetryable_OverloadedMessage(t *testing.T) {
	err := errors.New("API is overloaded, please try again")
	if !IsRetryableAssistantError(err) {
		t.Error("overloaded message should be retryable")
	}
}

// RE-005: "overload" in message is retryable.
func TestIsRetryable_OverloadMessage(t *testing.T) {
	err := errors.New("temporary overload condition")
	if !IsRetryableAssistantError(err) {
		t.Error("overload message should be retryable")
	}
}

// RE-006: Network connection errors are retryable.
func TestIsRetryable_NetworkError(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	if !IsRetryableAssistantError(err) {
		t.Error("network error should be retryable")
	}
}

// RE-007: Generic "connection refused" string is retryable.
func TestIsRetryable_ConnectionRefusedString(t *testing.T) {
	err := errors.New("connection refused")
	if !IsRetryableAssistantError(err) {
		t.Error("connection refused should be retryable")
	}
}

// RE-008: HTTP 400 is not retryable.
func TestIsRetryable_HTTP400(t *testing.T) {
	err := &ProviderError{StatusCode: 400, Message: "bad request"}
	if IsRetryableAssistantError(err) {
		t.Error("400 error should not be retryable")
	}
}

// RE-009: HTTP 401 is not retryable.
func TestIsRetryable_HTTP401(t *testing.T) {
	err := &ProviderError{StatusCode: 401, Message: "unauthorized"}
	if IsRetryableAssistantError(err) {
		t.Error("401 error should not be retryable")
	}
}

// RE-010: HTTP 403 is not retryable.
func TestIsRetryable_HTTP403(t *testing.T) {
	err := &ProviderError{StatusCode: 403, Message: "forbidden"}
	if IsRetryableAssistantError(err) {
		t.Error("403 error should not be retryable")
	}
}

// RE-011: nil error is not retryable.
func TestIsRetryable_NilError(t *testing.T) {
	if IsRetryableAssistantError(nil) {
		t.Error("nil error should not be retryable")
	}
}

// RE-012: ProviderError with IsRetryable=true but non-standard status code.
func TestIsRetryable_ProviderErrorFlag(t *testing.T) {
	err := &ProviderError{StatusCode: 529, Message: "overloaded", IsRetryable: true}
	if !IsRetryableAssistantError(err) {
		t.Error("ProviderError with IsRetryable=true should be retryable")
	}
}

// RE-013: ProviderError with IsRetryable=false is not retryable even with 429-like message.
func TestIsRetryable_ProviderErrorFlagFalse(t *testing.T) {
	err := &ProviderError{StatusCode: 400, Message: "bad request", IsRetryable: false}
	if IsRetryableAssistantError(err) {
		t.Error("ProviderError with IsRetryable=false and non-retryable status should not be retryable")
	}
}

// ---------------------------------------------------------------------------
// ClassifyError
// ---------------------------------------------------------------------------

// CE-001: ClassifyError extracts ProviderError from a ProviderError.
func TestClassifyError_ProviderError(t *testing.T) {
	original := &ProviderError{
		ProviderName: "openai",
		StatusCode: 429,
		Message: "rate limit exceeded",
		RetryAfter: 5 * time.Second,
		IsRetryable: true,
	}
	classified := ClassifyError(original)
	if classified == nil {
		t.Fatal("ClassifyError returned nil for ProviderError")
	}
	if classified.ProviderName != "openai" {
		t.Errorf("ProviderName = %q, want %q", classified.ProviderName, "openai")
	}
	if classified.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", classified.StatusCode)
	}
}

// CE-002: ClassifyError wraps generic error with IsRetryable based on content.
func TestClassifyError_GenericErrorOverloaded(t *testing.T) {
	err := errors.New("API is overloaded")
	classified := ClassifyError(err)
	if classified == nil {
		t.Fatal("ClassifyError returned nil for generic error")
	}
	if !classified.IsRetryable {
		t.Error("overloaded error should be classified as retryable")
	}
}

// CE-003: ClassifyError on non-retryable generic error.
func TestClassifyError_GenericErrorNonRetryable(t *testing.T) {
	err := errors.New("invalid API key")
	classified := ClassifyError(err)
	if classified == nil {
		t.Fatal("ClassifyError should not return nil")
	}
	if classified.IsRetryable {
		t.Error("invalid API key should not be retryable")
	}
}

// CE-004: ClassifyError on nil returns nil.
func TestClassifyError_Nil(t *testing.T) {
	if ClassifyError(nil) != nil {
		t.Error("ClassifyError(nil) should return nil")
	}
}

// CE-005: ClassifyError preserves Body from ProviderError.
func TestClassifyError_PreservesBody(t *testing.T) {
	body := []byte(`{"error":{"type":"rate_limit_error"}}`)
	original := &ProviderError{
		ProviderName: "openai",
		StatusCode: 429,
		Message: "rate limit exceeded",
		Body: body,
		IsRetryable: true,
	}
	classified := ClassifyError(original)
	if string(classified.Body) != string(body) {
		t.Errorf("Body not preserved: got %q, want %q", classified.Body, body)
	}
}

// CE-006: ClassifyError detects network errors.
func TestClassifyError_NetworkError(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	classified := ClassifyError(err)
	if classified == nil {
		t.Fatal("ClassifyError returned nil for network error")
	}
	if !classified.IsRetryable {
		t.Error("network error should be classified as retryable")
	}
}

// CE-007: ClassifyError on fmt.Errorf wrapped ProviderError unwraps correctly.
func TestClassifyError_WrappedProviderError(t *testing.T) {
	original := &ProviderError{
		ProviderName: "openai",
		StatusCode: 503,
		Message: "service unavailable",
		IsRetryable: true,
	}
	wrapped := fmt.Errorf("call failed: %w", original)
	classified := ClassifyError(wrapped)
	if classified == nil {
		t.Fatal("ClassifyError returned nil for wrapped ProviderError")
	}
	if classified.StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", classified.StatusCode)
	}
}

// helper
func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
