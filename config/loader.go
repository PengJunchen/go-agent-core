package config

import (
	"context"
	"fmt"
	"os"

	"github.com/pengjunchen/go-agent-core/capability/mcp"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/tools"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	llmregistry "github.com/pengjunchen/go-agent-core/llm/registry"
	"gopkg.in/yaml.v3"
)

// AssembledComponents 保存配置驱动组装的结果。
//
// 各字段在对应配置缺失时为 nil，调用方按需使用。
type AssembledComponents struct {
	// Provider 是根据 settings.Provider + settings.Model 创建的 ModelProvider。
	// 当 settings.Provider 为空时为 nil。
	Provider provider.ModelProvider

	// ToolRegistry 是注册了指定工具子集的 ToolRegistry。
	// 当 settings.Extra["tools"] 为空时为 nil。
	ToolRegistry registry.ToolRegistry

	// MCPServers 是加载了 MCP server 配置的 MCPProviderRegistry。
	// 当 settings.Extra["mcp_servers"] 为空时为 nil。
	MCPServers *mcp.MCPProviderRegistry
}

// LoadAndAssemble 根据 Settings 组装所有依赖组件。
//
// 当 Settings 中未指定 Provider/MCP/工具时，对应字段为 nil，
// 调用方仍可手动注入（向后兼容）。
func LoadAndAssemble(settings Settings) (*AssembledComponents, error) {
	ac := &AssembledComponents{}

	// 1. Provider: 根据 settings.Provider + settings.Model 从注册表创建。
	if settings.Provider != "" {
		providerName := settings.Provider
		modelName := settings.Model
		if modelName == "" {
			modelName = DefaultModel
		}
		cfg := &llmregistry.ProviderConfig{
			Name: providerName,
			Model: modelName,
			APIKey: settings.APIKey,
		}
		p, err := llmregistry.DefaultRegistry.GetProvider(providerName, cfg)
		if err != nil {
			return nil, fmt.Errorf("config loader: create provider %q: %w", providerName, err)
		}
		ac.Provider = p
	}

	// 2. MCP servers: 从 settings.Extra["mcp_servers"] 加载。
	if raw, ok := settings.Extra["mcp_servers"]; ok && raw != nil {
		entries, err := parseMCPServers(raw)
		if err != nil {
			return nil, fmt.Errorf("config loader: parse mcp_servers: %w", err)
		}
		if len(entries) > 0 {
			mcpReg := mcp.NewMCPProviderRegistry()
			// 将 entries 序列化为 YAML 后用 LoadFromYAML 加载。
			yamlData, err := yaml.Marshal(mcp.MCPConfig{McpServers: entries})
			if err != nil {
				return nil, fmt.Errorf("config loader: marshal mcp config: %w", err)
			}
			if err := mcpReg.LoadFromYAML(yamlData); err != nil {
				return nil, fmt.Errorf("config loader: load mcp servers: %w", err)
			}
			ac.MCPServers = mcpReg
		}
	}

	// 3. 工具子集: 从 settings.Extra["tools"] 注册指定工具。
	if raw, ok := settings.Extra["tools"]; ok && raw != nil {
		toolNames, err := parseToolNames(raw)
		if err != nil {
			return nil, fmt.Errorf("config loader: parse tools: %w", err)
		}
		if len(toolNames) > 0 {
			tr := registry.NewDefaultToolRegistry()
			workDir := settings.Workspace
			if workDir == "" {
				workDir, _ = os.Getwd()
			}
			if err := registerToolSubset(context.Background(), tr, workDir, toolNames); err != nil {
				return nil, fmt.Errorf("config loader: register tools: %w", err)
			}
			ac.ToolRegistry = tr
		}
	}

	return ac, nil
}

// parseMCPServers 将 Extra["mcp_servers"] 的原始值解析为 []mcp.MCPEntry。
//
// 原始值通常是 JSON 反序列化后的 []any（每个元素为 map[string]any）。
func parseMCPServers(raw any) ([]mcp.MCPEntry, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected []any, got %T", raw)
	}
	entries := make([]mcp.MCPEntry, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entry := mcp.MCPEntry{
			Name: getStr(m, "name"),
			Type: getStr(m, "type"),
			Command: getStr(m, "command"),
			URL: getStr(m, "url"),
			Timeout: getStr(m, "timeout"),
		}
		if args, ok := m["args"].([]any); ok {
			for _, a := range args {
				if s, ok := a.(string); ok {
					entry.Args = append(entry.Args, s)
				}
			}
		}
		if env, ok := m["env"].(map[string]any); ok {
			entry.Env = make(map[string]string, len(env))
			for k, v := range env {
				if s, ok := v.(string); ok {
					entry.Env[k] = s
				}
			}
		}
		if headers, ok := m["headers"].(map[string]any); ok {
			entry.Headers = make(map[string]string, len(headers))
			for k, v := range headers {
				if s, ok := v.(string); ok {
					entry.Headers[k] = s
				}
			}
		}
		if entry.Name == "" {
			return nil, fmt.Errorf("mcp_servers[%d]: missing name", i)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// parseToolNames 将 Extra["tools"] 的原始值解析为 []string。
func parseToolNames(raw any) ([]string, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected []any, got %T", raw)
	}
	names := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			continue
		}
		names = append(names, s)
	}
	return names, nil
}

// registerToolSubset 将指定名称的内置工具注册到 ToolRegistry。
func registerToolSubset(ctx context.Context, tr registry.ToolRegistry, workDir string, names []string) error {
	// 将工具名映射到构造函数。
	constructors := map[string]func(string) registry.ToolDefinition{
		"read_file": tools.NewReadFileTool,
		"write_file": tools.NewWriteFileTool,
		"edit_file": tools.NewEditFileTool,
		"execute": tools.NewExecuteTool,
		"grep": tools.NewGrepTool,
		"glob": tools.NewGlobTool,
		"image_view": tools.NewImageViewTool,
		"ls": tools.NewLsTool,
		"web_fetch": tools.NewWebFetchTool,
	}

	for _, name := range names {
		constructor, ok := constructors[name]
		if !ok {
			return fmt.Errorf("unknown tool: %q", name)
		}
		toolDef := constructor(workDir)
		if err := tr.RegisterTool(ctx, toolDef); err != nil {
			return fmt.Errorf("register tool %q: %w", name, err)
		}
	}
	return nil
}

// getStr 从 map 中安全获取字符串值。
func getStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
