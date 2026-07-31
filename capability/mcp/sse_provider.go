// SSE 传输 MCPProvider 实现。
//
// SSEMCPProvider 通过常驻 SSE 流连接 MCP server：先 GET 指定 URL 建立 SSE 流，
// server 发送 endpoint 事件告知 POST 端点；客户端把 JSON-RPC 请求 POST 到该端点，
// 响应作为 message 事件回到 SSE 流。
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
	"net/url"
	"sync"
	"time"
)

// SSEMCPProvider 通过 SSE 流与 MCP server 通信。
type SSEMCPProvider struct {
	URL string
	Headers map[string]string
	Timeout time.Duration

	// httpClient 允许测试注入；nil 时使用 http.DefaultClient。
	httpClient *http.Client

	mu sync.Mutex
	conn *sseConn
	base *baseTransport // 缓存 baseTransport，
	connErr error
	connErrAt time.Time // 连接错误发生时间，用于过期重试
}

// connErrTTL 是连接错误的缓存过期时间。过期后 ensureConn 会重试拨号，
// 避免永久缓存瞬时错误（如网络抖动）导致后续调用永远失败。
const connErrTTL = 5 * time.Second

func (p *SSEMCPProvider) client() *http.Client {
	if p.httpClient != nil {
		return p.httpClient
	}
	return http.DefaultClient
}

// ensureConn 惰性建立 SSE 流（含 endpoint 协商）。
//
// 失败会被缓存 connErrTTL 以避免重复拨号，但过期后会重试，避免永久缓存
// 瞬时错误。成功后缓存连接与 baseTransport 供后续调用复用。
func (p *SSEMCPProvider) ensureConn(ctx context.Context) (*baseTransport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// 已有缓存的 baseTransport，直接复用。
	if p.base != nil {
		return p.base, nil
	}
	// 连接错误未过期，返回缓存的错误。
	if p.connErr != nil && time.Since(p.connErrAt) < connErrTTL {
		return nil, p.connErr
	}
	// 清空过期错误以便重试。
	p.connErr = nil
	p.connErrAt = time.Time{}
	conn, err := newSSEConn(ctx, p.URL, p.Headers, p.client())
	if err != nil {
		p.connErr = err
		p.connErrAt = time.Now()
		return nil, err
	}
	p.conn = conn
	p.base = newBaseTransport(p.Timeout, conn)
	return p.base, nil
}

// resetConn 在连接失效后清空缓存的 base/conn 以便重试。
func (p *SSEMCPProvider) resetConn(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.close() // cleanup: 关闭 SSE 连接，忽略已关闭错误
	}
	p.conn = nil
	p.base = nil
	p.connErr = err
	p.connErrAt = time.Now()
}

// ListTools 列出 MCP server 暴露的工具。
func (p *SSEMCPProvider) ListTools(ctx context.Context) ([]Tool, error) {
	bt, err := p.ensureConn(ctx)
	if err != nil {
		return nil, err
	}
	tools, err := bt.ListTools(ctx)
	if err != nil {
		p.resetConn(err)
	}
	return tools, err
}

// Call 调用 MCP server 上的远程工具。
func (p *SSEMCPProvider) Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error) {
	bt, err := p.ensureConn(ctx)
	if err != nil {
		return nil, err
	}
	res, err := bt.Call(ctx, toolName, args)
	if err != nil {
		p.resetConn(err)
	}
	return res, err
}

// Close 关闭 SSE 流并重置缓存。
func (p *SSEMCPProvider) Close() error {
	p.mu.Lock()
	conn := p.conn
	p.conn = nil
	p.base = nil
	p.connErr = nil
	p.connErrAt = time.Time{}
	p.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.close()
}

// ─── sseConn — 常驻 SSE 流 + POST 端点 ───────────────────────────

type sseConn struct {
	url string
	headers map[string]string
	client *http.Client

	endpoint string
	resp *http.Response
	msgs chan readResult
	done chan struct{}

	mu sync.Mutex
	connected bool
}

func newSSEConn(ctx context.Context, urlStr string, headers map[string]string, client *http.Client) (*sseConn, error) {
	c := &sseConn{
		url: urlStr,
		headers: headers,
		client: client,
		msgs: make(chan readResult, 64),
		done: make(chan struct{}),
	}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *sseConn) connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("mcp sse: build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse: connect: %w", err)
	}
	if resp.StatusCode >= 400 {
		_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
		return fmt.Errorf("mcp sse: connect returned %s", resp.Status)
	}
	// 读取 SSE 流直到拿到 endpoint 事件，期间把 message 事件喂给读泵。
	type ev struct {
		event string
		data string
	}
	evCh := make(chan ev, 8)
	parseErr := make(chan error, 1)
	go func() {
		parseErr <- parseSSE(resp.Body, func(event, data string) {
			evCh <- ev{event: event, data: data}
		})
	}()
	// 先取 endpoint 事件。
	select {
	case <-ctx.Done():
		_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
		return ctx.Err()
	case m := <-evCh:
		if m.event != "endpoint" {
			_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
			return fmt.Errorf("mcp sse: expected endpoint event, got %q", m.event)
		}
		ep, err := resolveURL(c.url, m.data)
		if err != nil {
			_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
			return fmt.Errorf("mcp sse: resolve endpoint: %w", err)
		}
		c.endpoint = ep
	case err := <-parseErr:
		_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
		if err != nil {
			return fmt.Errorf("mcp sse: read stream: %w", err)
		}
		return fmt.Errorf("mcp sse: stream closed before endpoint event")
	}
	// 启动读泵：持续消费 evCh（后续 message 事件）与 parseErr。
	go func() {
		defer close(c.done)
		defer close(c.msgs)
		for {
			select {
			case m, ok := <-evCh:
				if !ok {
					return
				}
				if m.data == "" {
					continue
				}
				var r rpcResponse
				if err := json.Unmarshal([]byte(m.data), &r); err != nil {
					c.msgs <- readResult{err: fmt.Errorf("mcp sse: decode message: %w", err)}
					return
				}
				c.msgs <- readResult{resp: r}
			case err, ok := <-parseErr:
				if !ok {
					return
				}
				if err != nil {
					c.msgs <- readResult{err: fmt.Errorf("mcp sse: read stream: %w", err)}
				}
				return
			}
		}
	}()
	c.resp = resp
	c.connected = true
	return nil
}

func (c *sseConn) call(ctx context.Context, req *rpcRequest) (*rpcResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	postResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mcp sse: post request: %w", err)
	}
	_, _ = io.Copy(io.Discard, postResp.Body) // drain: 丢弃 POST 响应残留体
	_ = postResp.Body.Close() // cleanup: 关闭 POST 响应体
	// 等待 SSE 流上匹配 id 的响应。
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r, ok := <-c.msgs:
			if !ok {
				return nil, io.ErrClosedPipe
			}
			if r.err != nil {
				return nil, r.err
			}
			if r.resp.ID == req.ID {
				return &r.resp, nil
			}
		}
	}
}

func (c *sseConn) notify(ctx context.Context, n *rpcNotification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
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
	_, _ = io.Copy(io.Discard, resp.Body) // drain: 丢弃残留响应体
	_ = resp.Body.Close() // cleanup: 关闭响应体，忽略已关闭错误
	return nil
}

func (c *sseConn) close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.connected {
		return nil
	}
	c.connected = false
	if c.resp != nil {
		return c.resp.Body.Close()
	}
	return nil
}

// resolveURL 将 SSE endpoint 事件中的（可能是相对的）URL 解析为绝对 URL。
func resolveURL(base, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("empty endpoint")
	}
	baseU, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	refU, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return baseU.ResolveReference(refU).String(), nil
}
