// Package mcp 定义 MCP（Model Context Protocol）工具发现代理抽象。
//
// MCPProvider 负责连接 MCP server、发现工具、执行远程调用。
// 默认实现 DefaultMCPProvider 支持社区 SDK + 官方 SDK 双轨，
// 并支持热重载。
package mcp

import "context"

// MCPServerConfig 描述一个 MCP server 的连接配置。
type MCPServerConfig struct {
	Name string
	Type string // "stdio" | "sse" | "streamable_http"
	Command string // stdio 模式的启动命令
	URL string // sse/streamable_http 模式的 URL
	Env map[string]string
}

// MCPProvider 是 MCP 工具发现代理接口。
type MCPProvider interface {
	// Connect 连接到 MCP server 并发现工具。返回清理函数。
	Connect(ctx context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error)
	// Disconnect 断开所有 MCP 连接。
	Disconnect() error
	// Reload 热重载 MCP 配置。
	Reload(ctx context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error)
}

// ToolRef 引用一个从 MCP server 发现的工具。
type ToolRef struct {
	Name string
	Description string
	ServerName string
	Parameters map[string]any // JSON Schema
}
