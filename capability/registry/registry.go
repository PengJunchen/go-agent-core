// Package registry 定义工具注册表抽象。
//
// ToolRegistry 管理工具的注册、查询与热重载。第三方可通过注册表
// 添加自定义工具，无需修改核心代码。
package registry

import "context"

// ToolDefinition 描述一个工具的完整定义。
type ToolDefinition struct {
	Name string
	Description string
	Parameters map[string]any // JSON Schema
	Handler ToolHandler
	ParallelSafe bool // 标记该工具可安全并行调用
	ValidateArgs bool // 标记该工具的参数需要校验
}

// ToolHandler 是工具的执行函数。
type ToolHandler func(ctx context.Context, args map[string]any) (*ToolResult, error)

// ToolResult 是工具执行的返回结果。
type ToolResult struct {
	Content string
	IsError bool
	Details map[string]any
}

// ToolRegistry 是工具注册表接口。
type ToolRegistry interface {
	RegisterTool(ctx context.Context, tool ToolDefinition) error
	UnregisterTool(ctx context.Context, toolName string) error
	GetTool(ctx context.Context, toolName string) (ToolDefinition, error)
	ListTools(ctx context.Context) ([]ToolDefinition, error)
	Reload(ctx context.Context) error
}
