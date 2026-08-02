package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// WF-001: web_fetch fetches content from a URL.
func TestWebFetch_BasicFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><body>Hello World</body></html>"))
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Hello World") {
		t.Errorf("expected 'Hello World' in output, got: %s", result.Content)
	}
}

// WF-002: web_fetch respects max_length truncation.
func TestWebFetch_Truncation(t *testing.T) {
	longContent := strings.Repeat("a", 10000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(longContent))
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
		"max_length": 100,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Output should be truncated (with marker).
	if !strings.Contains(result.Content, "[truncated]") {
		t.Error("expected truncation marker in output")
	}
}

// WF-003: web_fetch returns error for missing URL.
func TestWebFetch_MissingURL(t *testing.T) {
	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for missing URL")
	}
}

// WF-004: web_fetch rejects binary content.
func TestWebFetch_BinaryRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for binary content")
	}
}

// WF-005: web_fetch handles server errors.
func TestWebFetch_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for server 500")
	}
}

// WF-006: web_fetch respects context cancellation.
func TestWebFetch_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"url": "http://example.com",
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// WF-007: web_fetch is ParallelSafe.
func TestWebFetch_ParallelSafe(t *testing.T) {
	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	if !tool.ParallelSafe {
		t.Error("web_fetch should be ParallelSafe")
	}
}

// WF-008: web_fetch with non-string URL returns error.
func TestWebFetch_InvalidURLType(t *testing.T) {
	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": 123,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for non-string URL")
	}
}

// WF-009: web_fetch fetches JSON content.
func TestWebFetch_JSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key": "value"}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "value") {
		t.Errorf("expected 'value' in output, got: %s", result.Content)
	}
}

// WF-010: web_fetch fetches plain text content.
func TestWebFetch_PlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain text response"))
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "plain text response") {
		t.Errorf("expected 'plain text response' in output, got: %s", result.Content)
	}
}

// WF-011: web_fetch returns details with status code.
func TestWebFetch_Details(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	dir := t.TempDir()
	tool := NewWebFetchTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"url": server.URL,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.Details == nil {
		t.Fatal("expected Details to be set")
	}
	if result.Details["status_code"] != 200 {
		t.Errorf("expected status_code=200, got %v", result.Details["status_code"])
	}
}
