// Package mcp 定义 MCP（Model Context Protocol）工具发现与调用代理抽象。
//
// MCPProvider 负责连接 MCP server、发现工具、执行远程调用。
// 默认实现 DefaultMCPProvider 支持社区 SDK + 官方 SDK 双轨，并支持热重载。
//
// MCPBroker：包含 Call 方法用于远程工具调用。
package mcp

import (
	"context"
	"encoding/json"
)

// MCPServerConfig 描述一个 MCP server 的连接配置。
type MCPServerConfig struct {
	Name string
	Type string // "stdio" | "sse" | "streamable_http"
	Command string // stdio 模式的启动命令
	URL string // sse/streamable_http 模式的 URL
	Env map[string]string
}

// MCPProvider 是 MCP 工具发现与调用代理接口。
type MCPProvider interface {
	// Connect 连接到 MCP server 并发现工具。返回清理函数。
	Connect(ctx context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error)
	// Disconnect 断开所有 MCP 连接。
	Disconnect() error
	// Reload 热重载 MCP 配置。
	Reload(ctx context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error)
	// Call 调用 MCP server 上的远程工具。
	Call(ctx context.Context, serverName, toolName string, args json.RawMessage) (json.RawMessage, error)
	// ListTools 列出所有已连接 server 暴露的工具。
	ListTools(ctx context.Context) ([]ToolRef, error)
}

// ToolRef 引用一个从 MCP server 发现的工具。
type ToolRef struct {
	Name string
	Description string
	ServerName string
	Parameters map[string]any // JSON Schema
}
