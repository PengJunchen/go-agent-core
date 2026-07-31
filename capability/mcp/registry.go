// MCPProviderRegistry 与 DefaultMCPProvider。
//
// MCPProviderRegistry 从 YAML 配置加载多个 MCP server，按 type 构建对应 Transport
// 并按 name 索引。Transport 构建逻辑通过 TransportFactory 注册表扩展（P5 可扩展），
// 内置 stdio / sse / http(streamable_http) 三种。
//
// DefaultMCPProvider 实现 mcp.go 中的 MCPProvider 代理接口：按 server 名路由到
// Transport，聚合工具发现结果。这是 issue GOA-21 设计中 "MCPProvider 默认实现"
// 的上层组合点，复用三种传输的 Transport 实现。

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// ─── YAML 配置类型 ───────────────────────────────────────────────

// MCPConfig 是 MCP server 配置文件的顶层结构。
type MCPConfig struct {
	McpServers []MCPEntry `yaml:"mcp_servers"`
}

// MCPEntry 描述单个 MCP server 的配置项。
type MCPEntry struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // stdio | sse | http | streamable_http
	Command string `yaml:"command,omitempty"`
	Args []string `yaml:"args,omitempty"`
	Env map[string]string `yaml:"env,omitempty"`
	URL string `yaml:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty"`
	Timeout string `yaml:"timeout,omitempty"` // Go duration 字符串，如 "30s"
}

// ─── TransportFactory 注册表 ─────────────────────────────────────

// TransportFactory 按 MCPEntry 构建一个 Transport。
type TransportFactory func(entry MCPEntry) (Transport, error)

// MCPProviderRegistry 管理多个命名 Transport，支持从 YAML 加载与手动注册。
type MCPProviderRegistry struct {
	mu sync.Mutex
	transports map[string]Transport
	order []string
	factories map[string]TransportFactory
}

// NewMCPProviderRegistry 创建注册表，预装 stdio / sse / http / streamable_http 工厂。
func NewMCPProviderRegistry() *MCPProviderRegistry {
	r := &MCPProviderRegistry{
		transports: map[string]Transport{},
		factories: map[string]TransportFactory{},
	}
	r.RegisterFactory("stdio", newStdioFromEntry)
	r.RegisterFactory("sse", newSSEFromEntry)
	r.RegisterFactory("http", newHTTPFromEntry)
	r.RegisterFactory("streamable_http", newHTTPFromEntry)
	return r
}

// RegisterFactory 注册或覆盖某 type 的 Transport 工厂。
func (r *MCPProviderRegistry) RegisterFactory(typeName string, f TransportFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[normalizeType(typeName)] = f
}

// Register 手动注册一个已构建的 Transport（同名覆盖）。
func (r *MCPProviderRegistry) Register(name string, t Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.transports[name]; !exists {
		r.order = append(r.order, name)
	}
	r.transports[name] = t
}

// Get 按 name 取 Transport。
func (r *MCPProviderRegistry) Get(name string) (Transport, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.transports[name]
	if !ok {
		return nil, fmt.Errorf("mcp registry: provider %q not found", name)
	}
	return t, nil
}

// Names 返回已注册 provider 名（按注册顺序）。
func (r *MCPProviderRegistry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// Close 关闭全部已注册 Transport。
func (r *MCPProviderRegistry) Close() error {
	r.mu.Lock()
	ts := r.transports
	r.transports = map[string]Transport{}
	r.order = nil
	r.mu.Unlock()
	var firstErr error
	for _, t := range ts {
		if err := t.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// LoadFromYAML 解析 YAML 配置并构建/注册全部 provider。
func (r *MCPProviderRegistry) LoadFromYAML(data []byte) error {
	var cfg MCPConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("mcp registry: parse yaml: %w", err)
	}
	for _, entry := range cfg.McpServers {
		t, err := r.build(entry)
		if err != nil {
			return err
		}
		r.Register(entry.Name, t)
	}
	return nil
}

// LoadFromFile 从文件读取 YAML 配置并加载。
func (r *MCPProviderRegistry) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("mcp registry: read %s: %w", path, err)
	}
	return r.LoadFromYAML(data)
}

func (r *MCPProviderRegistry) build(entry MCPEntry) (Transport, error) {
	if entry.Name == "" {
		return nil, fmt.Errorf("mcp registry: server entry missing name")
	}
	r.mu.Lock()
	f := r.factories[normalizeType(entry.Type)]
	r.mu.Unlock()
	if f == nil {
		return nil, fmt.Errorf("mcp registry: unknown type %q for server %q", entry.Type, entry.Name)
	}
	return f(entry)
}

// ─── 内置工厂 ────────────────────────────────────────────────────

func newStdioFromEntry(e MCPEntry) (Transport, error) {
	if e.Command == "" {
		return nil, fmt.Errorf("mcp: stdio server %q missing command", e.Name)
	}
	d, err := parseTimeout(e.Timeout)
	if err != nil {
		return nil, fmt.Errorf("mcp: stdio server %q: %w", e.Name, err)
	}
	return &StdioMCPProvider{Command: e.Command, Args: e.Args, Env: e.Env, Timeout: d}, nil
}

func newHTTPFromEntry(e MCPEntry) (Transport, error) {
	if e.URL == "" {
		return nil, fmt.Errorf("mcp: http server %q missing url", e.Name)
	}
	d, err := parseTimeout(e.Timeout)
	if err != nil {
		return nil, fmt.Errorf("mcp: http server %q: %w", e.Name, err)
	}
	return &HTTPMCPProvider{URL: e.URL, Headers: e.Headers, Timeout: d}, nil
}

func newSSEFromEntry(e MCPEntry) (Transport, error) {
	if e.URL == "" {
		return nil, fmt.Errorf("mcp: sse server %q missing url", e.Name)
	}
	d, err := parseTimeout(e.Timeout)
	if err != nil {
		return nil, fmt.Errorf("mcp: sse server %q: %w", e.Name, err)
	}
	return &SSEMCPProvider{URL: e.URL, Headers: e.Headers, Timeout: d}, nil
}

func parseTimeout(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

func normalizeType(t string) string {
	return strings.ToLower(strings.TrimSpace(t))
}

// ─── DefaultMCPProvider — MCPProvider 代理默认实现 ───────────────

// DefaultMCPProvider 实现 MCPProvider 接口，按 server 名路由到 Transport。
//
// 它把多 server 路由语义与单连接 Transport 解耦：Connect 为每个 MCPServerConfig
// 构建 Transport 并发现工具；Call/ListTools 聚合各 Transport 结果。
type DefaultMCPProvider struct {
	mu sync.Mutex
	transports map[string]Transport
	order []string
}

// NewDefaultMCPProvider 创建空的默认 MCPProvider。
func NewDefaultMCPProvider() *DefaultMCPProvider {
	return &DefaultMCPProvider{transports: map[string]Transport{}}
}

// Connect 连接全部 server，发现工具并返回清理函数。
//
// 每个成功连接的 server 对应一个清理函数（关闭其 Transport）；
// errs 与 servers 一一对应，nil 表示该 server 连接成功。
func (p *DefaultMCPProvider) Connect(ctx context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error) {
	// 重置既有连接。
	_ = p.disconnectLocked()

	var allTools []ToolRef
	cleanups := make([]func(), 0, len(servers))
	errs := make([]error, len(servers))

	for i, s := range servers {
		t, err := buildTransportFromConfig(s)
		if err != nil {
			errs[i] = err
			continue
		}
		tools, err := t.ListTools(ctx)
		if err != nil {
			errs[i] = err
			_ = t.Close()
			continue
		}
		p.mu.Lock()
		p.transports[s.Name] = t
		p.order = append(p.order, s.Name)
		p.mu.Unlock()
		for _, tool := range tools {
			allTools = append(allTools, ToolRef{
				Name: tool.Name,
				Description: tool.Description,
				ServerName: s.Name,
				Parameters: tool.InputSchema,
			})
		}
		transport := t
		name := s.Name
		cleanups = append(cleanups, func() {
			_ = transport.Close()
			p.mu.Lock()
			delete(p.transports, name)
			p.mu.Unlock()
		})
	}
	return allTools, cleanups, errs
}

// Disconnect 断开全部连接。
func (p *DefaultMCPProvider) Disconnect() error {
	return p.disconnectLocked()
}

func (p *DefaultMCPProvider) disconnectLocked() error {
	p.mu.Lock()
	ts := p.transports
	p.transports = map[string]Transport{}
	p.order = nil
	p.mu.Unlock()
	var firstErr error
	for _, t := range ts {
		if err := t.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Reload 断开既有连接后重新连接。
func (p *DefaultMCPProvider) Reload(ctx context.Context, servers []MCPServerConfig) ([]ToolRef, []func(), []error) {
	_ = p.disconnectLocked()
	return p.Connect(ctx, servers)
}

// Call 调用指定 server 上的工具。
func (p *DefaultMCPProvider) Call(ctx context.Context, serverName, toolName string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	t, ok := p.transports[serverName]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mcp: server %q not connected", serverName)
	}
	return t.Call(ctx, toolName, args)
}

// ListTools 聚合全部已连接 server 的工具。
func (p *DefaultMCPProvider) ListTools(ctx context.Context) ([]ToolRef, error) {
	p.mu.Lock()
	names := append([]string(nil), p.order...)
	p.mu.Unlock()
	var all []ToolRef
	for _, name := range names {
		p.mu.Lock()
		t := p.transports[name]
		p.mu.Unlock()
		if t == nil {
			continue
		}
		tools, err := t.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools from %q: %w", name, err)
		}
		for _, tool := range tools {
			all = append(all, ToolRef{
				Name: tool.Name,
				Description: tool.Description,
				ServerName: name,
				Parameters: tool.InputSchema,
			})
		}
	}
	return all, nil
}

// buildTransportFromConfig 从 broker 层 MCPServerConfig 构建 Transport。
//
// MCPServerConfig 字段较 MCPEntry 精简（无 Args/Headers/Timeout），适合上层
// 简化连接；完整配置请走 MCPProviderRegistry + MCPEntry（YAML）路径。
func buildTransportFromConfig(s MCPServerConfig) (Transport, error) {
	switch normalizeType(s.Type) {
	case "stdio":
		if s.Command == "" {
			return nil, fmt.Errorf("mcp: stdio server %q missing command", s.Name)
		}
		return &StdioMCPProvider{Command: s.Command, Env: s.Env}, nil
	case "sse":
		if s.URL == "" {
			return nil, fmt.Errorf("mcp: sse server %q missing url", s.Name)
		}
		return &SSEMCPProvider{URL: s.URL}, nil
	case "http", "streamable_http":
		if s.URL == "" {
			return nil, fmt.Errorf("mcp: http server %q missing url", s.Name)
		}
		return &HTTPMCPProvider{URL: s.URL}, nil
	default:
		return nil, fmt.Errorf("mcp: unknown server type %q for %q", s.Type, s.Name)
	}
}

// 接口实现契约编译期检查。
var (
	_ Transport = (*StdioMCPProvider)(nil)
	_ Transport = (*SSEMCPProvider)(nil)
	_ Transport = (*HTTPMCPProvider)(nil)
	_ MCPProvider = (*DefaultMCPProvider)(nil)
)
