// JSON-RPC 2.0 核心与 MCP 握手基类。
//
// 三种传输共享同一套 JSON-RPC 编解码与 MCP initialize 握手流程，仅在底层
// rpcConn（如何发送请求 / 接收响应）上不同。baseTransport 封装握手 + tools/list
// + tools/call，具体传输只需提供 rpcConn 实现即可复用全部协议逻辑。
//
// 协议版本

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MCP 协议版本（initialize 协商）。
const protocolVersion = "2024-11-05"

// ─── JSON-RPC 2.0 消息类型 ────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID int64 `json:"id"`
	Method string `json:"method"`
	Params any `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method string `json:"method"`
	Params any `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID int64 `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code int `json:"code"`
	Message string `json:"message"`
	Data json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

// ─── MCP 方法参数 / 结果类型 ──────────────────────────────────────

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities map[string]any `json:"capabilities"`
	ClientInfo clientInfo `json:"clientInfo"`
}

type clientInfo struct {
	Name string `json:"name"`
	Version string `json:"version"`
}

type toolsCallParams struct {
	Name string `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type toolsListResult struct {
	Tools []Tool `json:"tools"`
}

// rpcConn 是底层 JSON-RPC 通道抽象，由各传输实现。
//
// - call 发送请求并等待匹配 id 的响应
// - notify 发送通知（无响应）
// - close 关闭通道
type rpcConn interface {
	call(ctx context.Context, req *rpcRequest) (*rpcResponse, error)
	notify(ctx context.Context, n *rpcNotification) error
	close() error
}

var rpcIDCounter int64

// nextID 返回单调递增的 JSON-RPC 请求 id。
func nextID() int64 { return atomic.AddInt64(&rpcIDCounter, 1) }

// baseTransport 封装 MCP 握手与工具调用，复用于三种传输。
//
// 首次 Call/ListTools 时惰性完成 initialize 握手；Close 后可再次使用（重新握手）。
type baseTransport struct {
	timeout time.Duration
	conn rpcConn

	mu sync.Mutex
	inited bool
}

func newBaseTransport(timeout time.Duration, conn rpcConn) *baseTransport {
	return &baseTransport{timeout: timeout, conn: conn}
}

// ensureInit 完成 MCP initialize 握手（仅一次，失败后下次重试）。
func (b *baseTransport) ensureInit(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inited {
		return nil
	}
	initReq := &rpcRequest{
		JSONRPC: "2.0",
		ID: nextID(),
		Method: "initialize",
		Params: initializeParams{
			ProtocolVersion: protocolVersion,
			Capabilities: map[string]any{},
			ClientInfo: clientInfo{Name: "go-agent-core", Version: "1.0.0"},
		},
	}
	resp, err := b.conn.call(ctx, initReq)
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("mcp initialize: %w", resp.Error)
	}
	if err := b.conn.notify(ctx, &rpcNotification{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		return fmt.Errorf("mcp initialized notification: %w", err)
	}
	b.inited = true
	return nil
}

// withTimeout 为单次调用附加超时；无超时则仅提供 cancel 句柄。
func (b *baseTransport) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if b.timeout > 0 {
		return context.WithTimeout(ctx, b.timeout)
	}
	return context.WithCancel(ctx)
}

// ListTools 调用 tools/list 并解码工具列表。
func (b *baseTransport) ListTools(ctx context.Context) ([]Tool, error) {
	if err := b.ensureInit(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := b.withTimeout(ctx)
	defer cancel()
	resp, err := b.conn.call(ctx, &rpcRequest{JSONRPC: "2.0", ID: nextID(), Method: "tools/list"})
	if err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", resp.Error)
	}
	var res toolsListResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		return nil, fmt.Errorf("mcp tools/list: decode result: %w", err)
	}
	return res.Tools, nil
}

// Call 调用 tools/call 并返回原始 JSON 结果。
func (b *baseTransport) Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error) {
	if err := b.ensureInit(ctx); err != nil {
		return nil, err
	}
	ctx, cancel := b.withTimeout(ctx)
	defer cancel()
	resp, err := b.conn.call(ctx, &rpcRequest{
		JSONRPC: "2.0",
		ID: nextID(),
		Method: "tools/call",
		Params: toolsCallParams{Name: toolName, Arguments: args},
	})
	if err != nil {
		return nil, fmt.Errorf("mcp tools/call %q: %w", toolName, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp tools/call %q: %w", toolName, resp.Error)
	}
	return resp.Result, nil
}

// Close 关闭底层连接并重置握手状态以便复用。
func (b *baseTransport) Close() error {
	b.mu.Lock()
	wasInited := b.inited
	b.inited = false
	b.mu.Unlock()
	if !wasInited {
		return nil
	}
	return b.conn.close()
}
