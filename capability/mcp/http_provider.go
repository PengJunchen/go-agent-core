// streamable HTTP 传输 MCPProvider 实现。
//
// HTTPMCPProvider 通过向单个 URL POST JSON-RPC 请求来连接 MCP server，
// 支持两种响应模式（按 Accept 协商）：
// - application/json：单次 JSON-RPC 响应
// - text/event-stream：SSE 流，响应作为 message 事件返回
//
//

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPMCPProvider 通过 streamable HTTP 与 MCP server 通信。
type HTTPMCPProvider struct {
	URL string
	Headers map[string]string
	Timeout time.Duration

	// httpClient 允许测试注入；nil 时使用 http.DefaultClient。
	httpClient *http.Client

	mu sync.Mutex
	conn *httpConn // 复用同一 client / 配置
	base *baseTransport // 缓存 baseTransport，
}

// ensureBase 惰性建立 baseTransport（首次调用时初始化）。
func (p *HTTPMCPProvider) ensureBase() *baseTransport {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.base != nil {
		return p.base
	}
	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	p.conn = newHTTPConn(p.URL, p.Headers, client)
	p.base = newBaseTransport(p.Timeout, p.conn)
	return p.base
}

// ListTools 列出 MCP server 暴露的工具。
func (p *HTTPMCPProvider) ListTools(ctx context.Context) ([]Tool, error) {
	return p.ensureBase().ListTools(ctx)
}

// Call 调用 MCP server 上的远程工具。
func (p *HTTPMCPProvider) Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error) {
	return p.ensureBase().Call(ctx, toolName, args)
}

// Close 清理连接。HTTP 为无状态连接，Close 重置缓存的 baseTransport。
func (p *HTTPMCPProvider) Close() error {
	p.mu.Lock()
	bt := p.base
	p.base = nil
	p.conn = nil
	p.mu.Unlock()
	if bt == nil {
		return nil
	}
	return bt.Close()
}

// ─── httpConn — JSON-RPC over HTTP POST ──────────────────────────

type httpConn struct {
	url string
	headers map[string]string
	client *http.Client
}

func newHTTPConn(url string, headers map[string]string, client *http.Client) *httpConn {
	return &httpConn{url: url, headers: headers, client: client}
}

func (c *httpConn) call(ctx context.Context, req *rpcRequest) (*rpcResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // cleanup: 关闭响应体，忽略已关闭错误
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp http: server returned %s", resp.Status)
	}
	// 按响应 Content-Type 解析：SSE 流或单次 JSON。
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		return readSSEResponse(ctx, resp.Body, req.ID)
	}
	var r rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("mcp http: decode response: %w", err)
	}
	if r.ID != req.ID {
		return nil, fmt.Errorf("mcp http: response id mismatch: got %d, want %d", r.ID, req.ID)
	}
	return &r, nil
}

func (c *httpConn) notify(ctx context.Context, n *rpcNotification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain: 丢弃残留响应体，忽略拷贝错误
	_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
	return nil
}

func (c *httpConn) close() error { return nil }
