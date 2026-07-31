// MCP 传输层抽象与共享类型。
//
// 本文件定义单个 MCP server 连接的传输抽象 Transport，由 stdio / sse /
// streamable_http 三种具体传输实现（见 stdio_provider.go / sse_provider.go /
// http_provider.go）。上层 DefaultMCPProvider（见 registry.go）作为 MCPProvider
// 代理，按 server 名路由到对应 Transport。
//
// 设计
// 与 broker 层 MCPProvider 接口解耦，避免单连接语义与多 server 路由语义混淆。

package mcp

import (
	"context"
	"encoding/json"
)

// Tool 描述一个从 MCP server 发现的工具。
//
// 对应 MCP 协议 tools/list 返回的 tool 对象；InputSchema 为 JSON Schema。
type Tool struct {
	Name string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// Transport 表示与单个 MCP server 的连接，按传输协议实现。
//
// 每种传输（stdio / sse / streamable_http）提供本接口的具体实现：
// - Call 调用 server 上的远程工具，返回 tools/call 的原始 JSON 结果
// - ListTools 列出 server 暴露的工具
// - Close 清理底层连接
//
// Transport 不负责多 server 路由——那是上层 DefaultMCPProvider 的职责。
type Transport interface {
	// Call 调用指定工具，args 为 JSON 编码的参数，返回 tools/call 的原始 JSON 结果。
	Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error)
	// ListTools 列出当前 server 暴露的工具。
	ListTools(ctx context.Context) ([]Tool, error)
	// Close 清理连接。多次调用不应返回错误。
	Close() error
}
