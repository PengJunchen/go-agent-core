package mcp

import (
	"encoding/json"
	"io"
	"sync"
	"testing"
)

// ─── 共享测试夹具 ────────────────────────────────────────────────

// testTools 返回一组固定测试工具。
func testTools() []Tool {
	return []Tool{
		{Name: "search", Description: "search the web", InputSchema: map[string]any{"type": "object"}},
		{Name: "fetch", Description: "fetch a url"},
	}
}

// bidirPipe 把两个 io.Pipe 组合成双向 io.ReadWriteCloser。
type bidirPipe struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (b *bidirPipe) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *bidirPipe) Write(p []byte) (int, error) { return b.w.Write(p) }
func (b *bidirPipe) Close() error {
	_ = b.r.Close()
	_ = b.w.Close()
	return nil
}

// newPipePair 返回一对互连的 bidirPipe（client 侧 + server 侧）。
func newPipePair() (client, server *bidirPipe) {
	c2sR, c2sW := io.Pipe() // client -> server
	s2cR, s2cW := io.Pipe() // server -> client
	client = &bidirPipe{r: s2cR, w: c2sW}
	server = &bidirPipe{r: c2sR, w: s2cW}
	return client, server
}

// mockStdioServer 在 server 侧 pipe 上模拟一个 MCP server，处理 initialize /
// notifications/initialized / tools/list / tools/call。tools/call 回显调用参数。
//
// 返回时（pipe 关闭）goroutine 退出。
func mockStdioServer(t *testing.T, rw io.ReadWriter, tools []Tool) {
	t.Helper()
	enc := json.NewEncoder(rw)
	dec := json.NewDecoder(rw)
	for {
		var req struct {
			JSONRPC string `json:"jsonrpc"`
			ID int64 `json:"id"`
			Method string `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"protocolVersion": protocolVersion,
					"capabilities": map[string]any{},
					"serverInfo": map[string]any{"name": "mock-stdio", "version": "1.0"},
				},
			})
		case "notifications/initialized":
			// 通知无响应。
		case "tools/list":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"tools": tools},
			})
		case "tools/call":
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{
					"content": []map[string]any{{"type": "text", "text": "called " + extractToolName(req.Params)}},
					"isError": false,
				},
			})
		default:
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
}

// extractToolName 从 tools/call params 中取 name（用于回显）。
func extractToolName(params json.RawMessage) string {
	var p struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(params, &p)
	return p.Name
}

// assertTransportTools 校验 transport.ListTools 返回的工具有期望名称集合。
func assertTransportTools(t *testing.T, tools []Tool, want []string) {
	t.Helper()
	if len(tools) != len(want) {
		t.Fatalf("tools count = %d, want %d (%+v)", len(tools), len(want), tools)
	}
	got := map[string]bool{}
	for _, tt := range tools {
		got[tt.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing tool %q in %v", w, tools)
		}
	}
}

// onceClose 确保清理仅执行一次（测试夹具用）。
type onceClose struct {
	once sync.Once
	fn func()
}

func (o *onceClose) close() { o.once.Do(o.fn) }
