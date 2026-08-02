package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	// Provider 是根据 settings 创建的 ModelProvider。
	Provider provider.ModelProvider

	// ToolRegistry 是注册了指定工具子集的 ToolRegistry。
	ToolRegistry registry.ToolRegistry

	// MCPServers 是加载了 MCP server 配置的 MCPProviderRegistry。
	MCPServers *mcp.MCPProviderRegistry

	// SkillsDir 是 Skill 扫描目录列表（从 settings.skills.dirs 加载）。
	SkillsDirs []string
}

// LoadAndAssemble 根据 Settings 组装所有依赖组件。
//
// 支持两种配置格式：
// 1. 嵌套格式（
// 2. 扁平格式（向后兼容）：settings.provider / settings.model / settings.extra
//
// 当 Settings 中未指定 Provider/MCP/工具时，对应字段为 nil，
// 调用方仍可手动注入（向后兼容）。
func LoadAndAssemble(settings Settings, projectDir string) (*AssembledComponents, error) {
	ac := &AssembledComponents{}

	// 1. Provider: 使用 GetEffective 方法获取生效值（嵌套优先）。
	providerName := settings.GetEffectiveProvider()
	if providerName != "" {
		modelName := settings.GetEffectiveModel()
		if modelName == "" {
			modelName = DefaultModel
		}
		apiKey := settings.GetEffectiveAPIKey()
		baseURL := settings.GetEffectiveBaseURL()

		cfg := &llmregistry.ProviderConfig{
			Name: providerName,
			Model: modelName,
			APIKey: apiKey,
			BaseURL: baseURL,
		}
		p, err := llmregistry.DefaultRegistry.GetProvider(providerName, cfg)
		if err != nil {
			return nil, fmt.Errorf("config loader: create provider %q: %w", providerName, err)
		}
		ac.Provider = p
	}

	// 2. MCP servers: 优先从 settings.mcp.config_path 加载，回退到 settings.extra。
	if settings.MCPCfg != nil && settings.MCPCfg.ConfigPath != "" {
		mcpPath := settings.MCPCfg.ConfigPath
		if !filepath.IsAbs(mcpPath) {
			mcpPath = filepath.Join(projectDir, mcpPath)
		}
		mcpReg, err := loadMCPFromFile(mcpPath)
		if err != nil {
			return nil, fmt.Errorf("config loader: load mcp from %q: %w", mcpPath, err)
		}
		if mcpReg != nil {
			ac.MCPServers = mcpReg
		}
	} else if raw, ok := settings.Extra["mcp_servers"]; ok && raw != nil {
		entries, err := parseMCPServers(raw)
		if err != nil {
			return nil, fmt.Errorf("config loader: parse mcp_servers: %w", err)
		}
		if len(entries) > 0 {
			mcpReg := mcp.NewMCPProviderRegistry()
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

	// 4. Skills dirs: 从 settings.skills.dirs 收集。
	if settings.SkillsCfg != nil && len(settings.SkillsCfg.Dirs) > 0 {
		dirs := make([]string, 0, len(settings.SkillsCfg.Dirs))
		for _, dir := range settings.SkillsCfg.Dirs {
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(projectDir, dir)
			}
			dirs = append(dirs, dir)
		}
		ac.SkillsDirs = dirs
	}

	return ac, nil
}

// loadMCPFromFile 从 JSON 文件加载 MCP 配置（
//
// mcp.json 格式为：{"mcpServers":{"server-name":{"url":"...","transport":"streamable-http","headers":{}}}}
func loadMCPFromFile(path string) (*mcp.MCPProviderRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mcp config: %w", err)
	}

	// 解析 格式的 mcp.json
	var mcpFile struct {
		MCPServers map[string]struct {
			URL string `json:"url"`
			Transport string `json:"transport"`
			Command string `json:"command"`
			Args []string `json:"args"`
			Env map[string]string `json:"env"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &mcpFile); err != nil {
		return nil, fmt.Errorf("parse mcp config: %w", err)
	}

	if len(mcpFile.MCPServers) == 0 {
		return nil, nil
	}

	// 转换为 MCPEntry 列表
	entries := make([]mcp.MCPEntry, 0, len(mcpFile.MCPServers))
	for name, srv := range mcpFile.MCPServers {
		entry := mcp.MCPEntry{
			Name: name,
			URL: os.ExpandEnv(srv.URL),
			Headers: srv.Headers,
			Command: srv.Command,
			Args: srv.Args,
			Env: srv.Env,
		}
		// 根据 transport 字段确定类型
		switch srv.Transport {
		case "sse":
			entry.Type = "sse"
		case "streamable-http", "streamable_http", "http":
			entry.Type = "http"
		case "stdio":
			entry.Type = "stdio"
		default:
			// 有 URL 默认 http，有 Command 默认 stdio
			if srv.URL != "" {
				entry.Type = "http"
			} else if srv.Command != "" {
				entry.Type = "stdio"
			}
		}
		entries = append(entries, entry)
	}

	mcpReg := mcp.NewMCPProviderRegistry()
	yamlData, err := yaml.Marshal(mcp.MCPConfig{McpServers: entries})
	if err != nil {
		return nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	if err := mcpReg.LoadFromYAML(yamlData); err != nil {
		return nil, fmt.Errorf("load mcp servers: %w", err)
	}

	return mcpReg, nil
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
