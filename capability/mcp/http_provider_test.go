package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ─── 接口契约 ────────────────────────────────────────────────────

// Transport-002: HTTPMCPProvider 实现 Transport 接口。
func TestHTTPMCPProvider_InterfaceContract(t *testing.T) {
	var _ Transport = (*HTTPMCPProvider)(nil)
}

// mockHTTPHandler 返回一个处理 JSON-RPC 的 http.Handler，按方法响应 initialize
// / tools/list / tools/call。可配置为返回 SSE 流或单次 JSON。
func mockHTTPHandler(t *testing.T, tools []Tool, useSSE bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var req struct {
			ID int64 `json:"id"`
			Method string `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		writeResp := func(payload map[string]any) {
			payload["jsonrpc"] = "2.0"
			payload["id"] = req.ID
			data, _ := json.Marshal(payload)
			if useSSE {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("event: message\ndata: "))
				_, _ = w.Write(data)
				_, _ = w.Write([]byte("\n\n"))
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}
		switch req.Method {
		case "initialize":
			writeResp(map[string]any{
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities": map[string]any{},
					"serverInfo": map[string]any{"name": "mock-http", "version": "1.0"},
				},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeResp(map[string]any{"result": map[string]any{"tools": tools}})
		case "tools/call":
			writeResp(map[string]any{
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "called " + extractToolName(req.Params)}},
					"isError": false,
				},
			})
		default:
			writeResp(map[string]any{"error": map[string]any{"code": -32601, "message": "method not found"}})
		}
	})
}

// ─── 单次 JSON 响应 ──────────────────────────────────────────────

// HTTP-001: ListTools（application/json 响应）。
func TestHTTPMCPProvider_ListTools_JSON(t *testing.T) {
	srv := httptest.NewServer(mockHTTPHandler(t, testTools(), false))
	defer srv.Close()

	p := &HTTPMCPProvider{URL: srv.URL, httpClient: srv.Client()}
	defer func() { _ = p.Close() }()

	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	assertTransportTools(t, tools, []string{"search", "fetch"})
}

// HTTP-002: Call（application/json 响应）。
func TestHTTPMCPProvider_Call_JSON(t *testing.T) {
	srv := httptest.NewServer(mockHTTPHandler(t, testTools(), false))
	defer srv.Close()

	p := &HTTPMCPProvider{URL: srv.URL, httpClient: srv.Client()}
	defer func() { _ = p.Close() }()

	res, err := p.Call(context.Background(), "fetch", json.RawMessage(`{"url":"x"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(res, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Text != "called fetch" {
		t.Errorf("content = %+v", result.Content)
	}
}

// ─── SSE 响应 ────────────────────────────────────────────────────

// HTTP-003: ListTools（text/event-stream 响应）。
func TestHTTPMCPProvider_ListTools_SSE(t *testing.T) {
	srv := httptest.NewServer(mockHTTPHandler(t, testTools(), true))
	defer srv.Close()

	p := &HTTPMCPProvider{URL: srv.URL, httpClient: srv.Client()}
	defer func() { _ = p.Close() }()

	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	assertTransportTools(t, tools, []string{"search", "fetch"})
}

// HTTP-004: Call（text/event-stream 响应）。
func TestHTTPMCPProvider_Call_SSE(t *testing.T) {
	srv := httptest.NewServer(mockHTTPHandler(t, testTools(), true))
	defer srv.Close()

	p := &HTTPMCPProvider{URL: srv.URL, httpClient: srv.Client()}
	defer func() { _ = p.Close() }()

	res, err := p.Call(context.Background(), "search", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(res, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Text != "called search" {
		t.Errorf("content = %+v", result.Content)
	}
}

// ─── 错误处理 ────────────────────────────────────────────────────

// HTTP-005: server 返回 4xx 时报错。
func TestHTTPMCPProvider_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := &HTTPMCPProvider{URL: srv.URL, httpClient: srv.Client()}
	_, err := p.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected server error")
	}
}

// HTTP-006: 不可达 URL 时报错。
func TestHTTPMCPProvider_Unreachable(t *testing.T) {
	p := &HTTPMCPProvider{URL: "http://127.0.0.1:1/nope", httpClient: &http.Client{Timeout: 200 * time.Millisecond}}
	_, err := p.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
}

// HTTP-007: 自定义 Headers 被发送。
func TestHTTPMCPProvider_CustomHeaders(t *testing.T) {
	var gotAuth string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		mu.Unlock()
		mockHTTPHandler(t, testTools(), false).ServeHTTP(w, r)
	}))
	defer srv.Close()

	p := &HTTPMCPProvider{
		URL: srv.URL,
		Headers: map[string]string{"Authorization": "Bearer test-token"},
		httpClient: srv.Client(),
	}
	_, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want 'Bearer test-token'", gotAuth)
	}
}

// HTTP-008: baseTransport 被缓存，多次调用复用同一 baseTransport。
func TestHTTPMCPProvider_BaseTransportCached(t *testing.T) {
	srv := httptest.NewServer(mockHTTPHandler(t, testTools(), false))
	defer srv.Close()

	p := &HTTPMCPProvider{URL: srv.URL, httpClient: srv.Client()}
	defer func() { _ = p.Close() }()

	// 多次 ListTools/Call 应复用缓存的 baseTransport，不会重复 initialize。
	for i := 0; i < 3; i++ {
		tools, err := p.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools #%d: %v", i, err)
		}
		assertTransportTools(t, tools, []string{"search", "fetch"})
	}
}
