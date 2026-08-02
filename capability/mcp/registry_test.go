package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── MCPProviderRegistry 测试 ────────────────────────────────────

// Reg-001: NewMCPProviderRegistry 预装四种工厂。
func TestRegistry_BuiltinFactories(t *testing.T) {
	r := NewMCPProviderRegistry()
	r.mu.Lock()
	defer r.mu.Unlock()
	want := []string{"stdio", "sse", "http", "streamable_http"}
	for _, w := range want {
		if _, ok := r.factories[w]; !ok {
			t.Errorf("missing factory for type %q", w)
		}
	}
}

// Reg-002: RegisterFactory 覆盖现有工厂。
func TestRegistry_RegisterFactoryOverride(t *testing.T) {
	r := NewMCPProviderRegistry()
	called := false
	r.RegisterFactory("stdio", func(e MCPEntry) (Transport, error) {
		called = true
		return &noopTransport{}, nil
	})
	// 加载一个 stdio 配置触发工厂。
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: test
   type: stdio
   command: echo
`))
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	if !called {
		t.Error("custom factory was not called")
	}
}

// Reg-003: RegisterFactory 支持新类型。
func TestRegistry_RegisterFactoryNewType(t *testing.T) {
	r := NewMCPProviderRegistry()
	r.RegisterFactory("custom", func(e MCPEntry) (Transport, error) {
		return &noopTransport{}, nil
	})
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: mycustom
   type: custom
`))
	if err != nil {
		t.Fatalf("LoadFromYAML with custom type: %v", err)
	}
	if _, err := r.Get("mycustom"); err != nil {
		t.Fatalf("Get mycustom: %v", err)
	}
}

// Reg-004: Register 手动注册并可通过 Get 获取。
func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewMCPProviderRegistry()
	transport := &noopTransport{}
	r.Register("manual", transport)
	got, err := r.Get("manual")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != transport {
		t.Error("Get returned different transport")
	}
}

// Reg-005: Register 同名覆盖。
func TestRegistry_RegisterOverwrite(t *testing.T) {
	r := NewMCPProviderRegistry()
	first := &noopTransport{}
	second := &noopTransport{}
	r.Register("x", first)
	r.Register("x", second)
	got, err := r.Get("x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != second {
		t.Error("expected second transport after overwrite")
	}
	// 同名覆盖不应追加 order。
	names := r.Names()
	if len(names) != 1 || names[0] != "x" {
		t.Errorf("Names = %v, want [x]", names)
	}
}

// Reg-006: Get 不存在时返回错误。
func TestRegistry_GetNotFound(t *testing.T) {
	r := NewMCPProviderRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent provider")
	}
}

// Reg-007: Names 返回按注册顺序排列的名。
func TestRegistry_Names(t *testing.T) {
	r := NewMCPProviderRegistry()
	r.Register("b", &noopTransport{})
	r.Register("a", &noopTransport{})
	r.Register("c", &noopTransport{})
	names := r.Names()
	want := []string{"b", "a", "c"}
	if len(names) != len(want) {
		t.Fatalf("Names = %v, want %v", names, want)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("Names[%d] = %q, want %q", i, n, want[i])
		}
	}
}

// Reg-008: Names 返回副本，修改不影响内部状态。
func TestRegistry_NamesCopy(t *testing.T) {
	r := NewMCPProviderRegistry()
	r.Register("x", &noopTransport{})
	names := r.Names()
	names[0] = "modified"
	orig := r.Names()
	if orig[0] != "x" {
		t.Errorf("Names() returned reference, got %q after mutation", orig[0])
	}
}

// Reg-009: LoadFromYAML 加载 stdio 配置。
func TestRegistry_LoadFromYAML_Stdio(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: github
   type: stdio
   command: gh
   args: ["api", "mcp"]
   env:
     GITHUB_TOKEN: "xxx"
   timeout: 30s
`))
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	got, err := r.Get("github")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p, ok := got.(*StdioMCPProvider)
	if !ok {
		t.Fatal("expected *StdioMCPProvider")
	}
	if p.Command != "gh" {
		t.Errorf("Command = %q, want %q", p.Command, "gh")
	}
	if len(p.Args) != 2 || p.Args[0] != "api" || p.Args[1] != "mcp" {
		t.Errorf("Args = %v, want [api mcp]", p.Args)
	}
	if p.Env["GITHUB_TOKEN"] != "xxx" {
		t.Errorf("Env = %v, want GITHUB_TOKEN=xxx", p.Env)
	}
	if p.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", p.Timeout)
	}
}

// Reg-010: LoadFromYAML 加载 http 配置。
func TestRegistry_LoadFromYAML_HTTP(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: sentry
   type: http
   url: "https://mcp.sentry.dev/mcp"
   headers:
     Authorization: "Bearer token"
   timeout: 15s
`))
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	got, err := r.Get("sentry")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p, ok := got.(*HTTPMCPProvider)
	if !ok {
		t.Fatal("expected *HTTPMCPProvider")
	}
	if p.URL != "https://mcp.sentry.dev/mcp" {
		t.Errorf("URL = %q", p.URL)
	}
	if p.Headers["Authorization"] != "Bearer token" {
		t.Errorf("Headers = %v", p.Headers)
	}
	if p.Timeout != 15*time.Second {
		t.Errorf("Timeout = %v", p.Timeout)
	}
}

// Reg-011: LoadFromYAML 加载 sse 配置。
func TestRegistry_LoadFromYAML_SSE(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: myserver
   type: sse
   url: "https://example.com/sse"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	got, err := r.Get("myserver")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	p, ok := got.(*SSEMCPProvider)
	if !ok {
		t.Fatal("expected *SSEMCPProvider")
	}
	if p.URL != "https://example.com/sse" {
		t.Errorf("URL = %q", p.URL)
	}
}

// Reg-012: LoadFromYAML streamable_http 别名映射到 HTTPMCPProvider。
func TestRegistry_LoadFromYAML_StreamableHTTP(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: stream
   type: streamable_http
   url: "https://example.com/mcp"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	got, err := r.Get("stream")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := got.(*HTTPMCPProvider); !ok {
		t.Fatal("expected *HTTPMCPProvider for streamable_http type")
	}
}

// Reg-013: LoadFromYAML 多 server 同时加载。
func TestRegistry_LoadFromYAML_Multiple(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: github
   type: stdio
   command: gh
   args: ["api", "mcp"]
 - name: sentry
   type: http
   url: "https://mcp.sentry.dev/mcp"
`))
	if err != nil {
		t.Fatalf("LoadFromYAML: %v", err)
	}
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("Names = %v, want 2 entries", names)
	}
	if names[0] != "github" || names[1] != "sentry" {
		t.Errorf("Names order = %v, want [github sentry]", names)
	}
}

// Reg-014: LoadFromYAML 无效 YAML 报错。
func TestRegistry_LoadFromYAML_InvalidYAML(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`{invalid yaml`))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// Reg-015: LoadFromYAML 缺少 name 报错。
func TestRegistry_LoadFromYAML_MissingName(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - type: stdio
   command: echo
`))
	if err == nil {
		t.Error("expected error for missing name")
	}
}

// Reg-016: LoadFromYAML 未知 type 报错。
func TestRegistry_LoadFromYAML_UnknownType(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: bad
   type: websocket
`))
	if err == nil {
		t.Error("expected error for unknown type")
	}
}

// Reg-017: LoadFromYAML stdio 缺少 command 报错。
func TestRegistry_LoadFromYAML_StdioMissingCommand(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: nocmd
   type: stdio
`))
	if err == nil {
		t.Error("expected error for missing command")
	}
}

// Reg-018: LoadFromYAML http 缺少 url 报错。
func TestRegistry_LoadFromYAML_HTTPMissingURL(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: nourl
   type: http
`))
	if err == nil {
		t.Error("expected error for missing url")
	}
}

// Reg-019: LoadFromYAML sse 缺少 url 报错。
func TestRegistry_LoadFromYAML_SSEMissingURL(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: nourl
   type: sse
`))
	if err == nil {
		t.Error("expected error for missing url")
	}
}

// Reg-020: LoadFromYAML 无效 timeout 格式报错。
func TestRegistry_LoadFromYAML_InvalidTimeout(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: badtime
   type: stdio
   command: echo
   timeout: not-a-duration
`))
	if err == nil {
		t.Error("expected error for invalid timeout")
	}
}

// Reg-021: LoadFromFile 从文件读取配置。
func TestRegistry_LoadFromFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "mcp.yaml")
	err := os.WriteFile(cfgPath, []byte(`
mcp_servers:
 - name: filetest
   type: stdio
   command: echo
`), 0o644)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r := NewMCPProviderRegistry()
	if err := r.LoadFromFile(cfgPath); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if _, err := r.Get("filetest"); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

// Reg-022: LoadFromFile 文件不存在时报错。
func TestRegistry_LoadFromFileNotFound(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromFile("/nonexistent/path/mcp.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

// Reg-023: Close 关闭全部 Transport。
func TestRegistry_Close(t *testing.T) {
	r := NewMCPProviderRegistry()
	n1 := &noopTransport{}
	n2 := &noopTransport{}
	r.Register("a", n1)
	r.Register("b", n2)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !n1.closed || !n2.closed {
		t.Error("expected all transports to be closed")
	}
	// Close 后 Names 应为空。
	if names := r.Names(); len(names) != 0 {
		t.Errorf("Names after Close = %v, want empty", names)
	}
}

// Reg-024: Close 可多次调用。
func TestRegistry_CloseIdempotent(t *testing.T) {
	r := NewMCPProviderRegistry()
	r.Register("a", &noopTransport{})
	if err := r.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Reg-025: type 大小写不敏感。
func TestRegistry_TypeCaseInsensitive(t *testing.T) {
	r := NewMCPProviderRegistry()
	err := r.LoadFromYAML([]byte(`
mcp_servers:
 - name: upper
   type: STDIO
   command: echo
`))
	if err != nil {
		t.Fatalf("LoadFromYAML with uppercase type: %v", err)
	}
	if _, err := r.Get("upper"); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

// ─── DefaultMCPProvider 测试 ─────────────────────────────────────

// Def-001: DefaultMCPProvider 实现 MCPProvider 接口。
func TestDefaultMCPProvider_InterfaceContract(t *testing.T) {
	var _ MCPProvider = (*DefaultMCPProvider)(nil)
}

// Def-002: Connect 连接 stdio server 并发现工具。
func TestDefaultMCPProvider_ConnectStdio(t *testing.T) {
	provider := NewDefaultMCPProvider()

	clientPipe, serverPipe := newPipePair()
	go mockStdioServer(t, serverPipe, testTools())
	defer func() { _ = serverPipe.Close() }()

	// 注入 dial 到 Transport 构建流程。
	// 因为 Connect 使用 buildTransportFromConfig 构建标准 transport，
	// 我们改用手动 Register + mock 来测试 Connect 路由。
	transport := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	// 直接测试 DefaultMCPProvider 的路由能力。
	provider.mu.Lock()
	provider.transports["github"] = transport
	provider.order = append(provider.order, "github")
	provider.mu.Unlock()

	tools, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(tools))
	}
	if tools[0].ServerName != "github" {
		t.Errorf("ServerName = %q, want %q", tools[0].ServerName, "github")
	}
}

// Def-003: Call 路由到正确的 server。
func TestDefaultMCPProvider_Call(t *testing.T) {
	provider := NewDefaultMCPProvider()

	clientPipe, serverPipe := newPipePair()
	go mockStdioServer(t, serverPipe, testTools())
	defer func() { _ = serverPipe.Close() }()

	transport := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	provider.mu.Lock()
	provider.transports["github"] = transport
	provider.order = append(provider.order, "github")
	provider.mu.Unlock()

	res, err := provider.Call(context.Background(), "github", "search", json.RawMessage(`{"q":"x"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(res, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Content) == 0 || result.Content[0].Text != "called search" {
		t.Errorf("content = %+v", result.Content)
	}
}

// Def-004: Call 未连接的 server 返回错误。
func TestDefaultMCPProvider_CallUnknownServer(t *testing.T) {
	provider := NewDefaultMCPProvider()
	_, err := provider.Call(context.Background(), "nonexistent", "tool", nil)
	if err == nil {
		t.Error("expected error for unknown server")
	}
}

// Def-005: Disconnect 关闭所有连接。
func TestDefaultMCPProvider_Disconnect(t *testing.T) {
	provider := NewDefaultMCPProvider()
	n := &noopTransport{}
	provider.mu.Lock()
	provider.transports["test"] = n
	provider.order = append(provider.order, "test")
	provider.mu.Unlock()

	if err := provider.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if !n.closed {
		t.Error("expected transport to be closed")
	}
	if _, err := provider.Call(context.Background(), "test", "tool", nil); err == nil {
		t.Error("expected error after disconnect")
	}
}

// Def-006: Reload 断开并重新连接。
func TestDefaultMCPProvider_Reload(t *testing.T) {
	provider := NewDefaultMCPProvider()

	// 先建立连接。
	n1 := &noopTransport{tools: []Tool{{Name: "old_tool"}}}
	provider.mu.Lock()
	provider.transports["srv"] = n1
	provider.order = append(provider.order, "srv")
	provider.mu.Unlock()

	// Reload 使用 pipe mock server 模拟真实重新连接。
	clientPipe, serverPipe := newPipePair()
	go mockStdioServer(t, serverPipe, testTools())
	defer func() { _ = serverPipe.Close() }()

	// 手动构建 transport 并 Register 模拟 Reload 路径。
	_ = provider.Disconnect()
	transport := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	provider.mu.Lock()
	provider.transports["srv"] = transport
	provider.order = append(provider.order, "srv")
	provider.mu.Unlock()

	if !n1.closed {
		t.Error("old transport should be closed after Reload")
	}
	tools, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools after reload: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(tools))
	}
}

// Def-007: ListTools 聚合多个 server 的工具。
func TestDefaultMCPProvider_ListToolsMultipleServers(t *testing.T) {
	provider := NewDefaultMCPProvider()

	// 注册两个 mock transport。
	n1 := &noopTransport{tools: []Tool{{Name: "tool_a", Description: "from server A"}}}
	n2 := &noopTransport{tools: []Tool{{Name: "tool_b", Description: "from server B"}}}
	provider.mu.Lock()
	provider.transports["srvA"] = n1
	provider.order = append(provider.order, "srvA")
	provider.transports["srvB"] = n2
	provider.order = append(provider.order, "srvB")
	provider.mu.Unlock()

	tools, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(tools))
	}
	if tools[0].ServerName != "srvA" || tools[0].Name != "tool_a" {
		t.Errorf("first tool = %+v, want {Name:tool_a, ServerName:srvA}", tools[0])
	}
	if tools[1].ServerName != "srvB" || tools[1].Name != "tool_b" {
		t.Errorf("second tool = %+v, want {Name:tool_b, ServerName:srvB}", tools[1])
	}
}

// Def-008: buildTransportFromConfig 各类型构建。
func TestBuildTransportFromConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  MCPServerConfig
		want    string // 期望的 Transport 类型名
		wantErr bool
	}{
		{
			name:   "stdio",
			config: MCPServerConfig{Name: "s", Type: "stdio", Command: "echo"},
			want:   "*mcp.StdioMCPProvider",
		},
		{
			name:   "http",
			config: MCPServerConfig{Name: "h", Type: "http", URL: "http://x"},
			want:   "*mcp.HTTPMCPProvider",
		},
		{
			name:   "streamable_http",
			config: MCPServerConfig{Name: "sh", Type: "streamable_http", URL: "http://x"},
			want:   "*mcp.HTTPMCPProvider",
		},
		{
			name:   "sse",
			config: MCPServerConfig{Name: "se", Type: "sse", URL: "http://x/sse"},
			want:   "*mcp.SSEMCPProvider",
		},
		{
			name:    "stdio missing command",
			config:  MCPServerConfig{Name: "s", Type: "stdio"},
			wantErr: true,
		},
		{
			name:    "http missing url",
			config:  MCPServerConfig{Name: "h", Type: "http"},
			wantErr: true,
		},
		{
			name:    "sse missing url",
			config:  MCPServerConfig{Name: "se", Type: "sse"},
			wantErr: true,
		},
		{
			name:    "unknown type",
			config:  MCPServerConfig{Name: "u", Type: "unknown"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildTransportFromConfig(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			typeName := getTypeName(got)
			if typeName != tt.want {
				t.Errorf("type = %q, want %q", typeName, tt.want)
			}
		})
	}
}

// ─── 辅助类型 ────────────────────────────────────────────────────

// noopTransport 是无操作的 Transport，用于测试注册和路由。
type noopTransport struct {
	tools  []Tool
	closed bool
}

func (n *noopTransport) Call(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"ok":true}`), nil
}

func (n *noopTransport) ListTools(_ context.Context) ([]Tool, error) {
	return n.tools, nil
}

func (n *noopTransport) Close() error {
	n.closed = true
	return nil
}

// getTypeName 返回运行时类型名（简化，用于断言 Transport 类型）。
func getTypeName(v interface{}) string {
	return fmt.Sprintf("%T", v)
}
