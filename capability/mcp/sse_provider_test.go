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

// Transport-003: SSEMCPProvider 实现 Transport 接口。
func TestSSEMCPProvider_InterfaceContract(t *testing.T) {
	var _ Transport = (*SSEMCPProvider)(nil)
}

// sseTestServer 是一个模拟 MCP SSE 传输的测试 server：
// - GET /sse 建立 SSE 流，先发 endpoint 事件（指向 /message），随后把 POST
// 收到的请求响应作为 message 事件写回流。
// - POST /message 接收 JSON-RPC 请求，交由 broker 写回响应。
type sseTestServer struct {
	t *testing.T
	tools []Tool

	mu sync.Mutex
	w http.ResponseWriter
	flusher http.Flusher
	ready chan struct{}
}

func newSSETestServer(t *testing.T, tools []Tool) *sseTestServer {
	return &sseTestServer{t: t, tools: tools, ready: make(chan struct{})}
}

func (s *sseTestServer) resetReady() {
	s.mu.Lock()
	s.ready = make(chan struct{})
	s.mu.Unlock()
}

func (s *sseTestServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", s.serveSSE)
	mux.HandleFunc("/message", s.serveMessage)
	return mux
}

func (s *sseTestServer) serveSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	f, ok := w.(http.Flusher)
	if !ok {
		s.t.Fatal("ResponseWriter does not support flushing")
	}
	s.mu.Lock()
	s.w = w
	s.flusher = f
	select {
	case <-s.ready:
		// already closed, need reset
	default:
		close(s.ready)
	}
	s.mu.Unlock()

	// 发送 endpoint 事件。
	_, _ = io.WriteString(w, "event: endpoint\ndata: /message\n\n")
	f.Flush()

	// 保持连接直到客户端断开。
	<-r.Context().Done()
}

// sseTestRequest 是 SSE 测试 server 的 JSON-RPC 请求结构。
type sseTestRequest struct {
	ID int64 `json:"id"`
	Method string `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (s *sseTestServer) serveMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req sseTestRequest
	_ = json.Unmarshal(body, &req)

	resp := s.buildResponse(req)
	// 把响应写入 SSE 流。
	s.mu.Lock()
	if s.w != nil && s.flusher != nil {
		_, _ = io.WriteString(s.w, "event: message\ndata: ")
		_, _ = s.w.Write(resp) // mock server：响应写入 SSE 流
		_, _ = io.WriteString(s.w, "\n\n")
		s.flusher.Flush()
	}
	s.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}

func (s *sseTestServer) buildResponse(req sseTestRequest) []byte {
	var payload map[string]any
	switch req.Method {
	case "initialize":
		payload = map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "mock-sse", "version": "1.0"},
			},
		}
	case "tools/list":
		payload = map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"tools": s.tools},
		}
	case "tools/call":
		payload = map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "called " + extractToolName(req.Params)}},
				"isError": false,
			},
		}
	default:
		payload = map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"error": map[string]any{"code": -32601, "message": "method not found"},
		}
	}
	data, _ := json.Marshal(payload)
	return data
}

// ─── 功能测试 ────────────────────────────────────────────────────

// SSE-001: ListTools 通过 SSE 流发现工具。
func TestSSEMCPProvider_ListTools(t *testing.T) {
	srv := newSSETestServer(t, testTools())
	httpSrv := httptest.NewServer(srv.handler())
	defer httpSrv.Close()

	p := &SSEMCPProvider{URL: httpSrv.URL + "/sse", httpClient: httpSrv.Client()}
	defer func() { _ = p.Close() }()

	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	assertTransportTools(t, tools, []string{"search", "fetch"})
}

// SSE-002: Call 通过 SSE 流调用工具。
func TestSSEMCPProvider_Call(t *testing.T) {
	srv := newSSETestServer(t, testTools())
	httpSrv := httptest.NewServer(srv.handler())
	defer httpSrv.Close()

	p := &SSEMCPProvider{URL: httpSrv.URL + "/sse", httpClient: httpSrv.Client()}
	defer func() { _ = p.Close() }()

	res, err := p.Call(context.Background(), "search", json.RawMessage(`{"q":"x"}`))
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

// SSE-003: 连接复用——多次 ListTools 共用同一 SSE 流。
func TestSSEMCPProvider_ReuseConnection(t *testing.T) {
	srv := newSSETestServer(t, testTools())
	httpSrv := httptest.NewServer(srv.handler())
	defer httpSrv.Close()

	p := &SSEMCPProvider{URL: httpSrv.URL + "/sse", httpClient: httpSrv.Client()}
	defer func() { _ = p.Close() }()

	for i := 0; i < 3; i++ {
		tools, err := p.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools #%d: %v", i, err)
		}
		assertTransportTools(t, tools, []string{"search", "fetch"})
	}
}

// SSE-004: 不可达 URL 时报错。
func TestSSEMCPProvider_Unreachable(t *testing.T) {
	p := &SSEMCPProvider{
		URL: "http://127.0.0.1:1/sse",
		httpClient: &http.Client{Timeout: 200 * time.Millisecond},
	}
	_, err := p.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected connection error")
	}
}

// SSE-005: Close 后可重新连接。
func TestSSEMCPProvider_CloseAndReconnect(t *testing.T) {
	srv := newSSETestServer(t, testTools())
	httpSrv := httptest.NewServer(srv.handler())
	defer httpSrv.Close()

	p := &SSEMCPProvider{URL: httpSrv.URL + "/sse", httpClient: httpSrv.Client()}

	if _, err := p.ListTools(context.Background()); err != nil {
		t.Fatalf("first ListTools: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// 重连（同一 server 可再次接受新 SSE 流）。
	if _, err := p.ListTools(context.Background()); err != nil {
		t.Fatalf("second ListTools: %v", err)
	}
	_ = p.Close()
}
