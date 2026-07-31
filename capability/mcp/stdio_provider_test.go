package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// ─── 接口契约 ────────────────────────────────────────────────────

// Transport-001: StdioMCPProvider 实现 Transport 接口。
func TestStdioMCPProvider_InterfaceContract(t *testing.T) {
	var _ Transport = (*StdioMCPProvider)(nil)
}

// ─── 功能测试（pipe mock server） ─────────────────────────────────

// Stdio-001: ListTools 返回 mock server 暴露的工具。
func TestStdioMCPProvider_ListTools(t *testing.T) {
	clientPipe, serverPipe := newPipePair()
	go mockStdioServer(t, serverPipe, testTools())
	defer func() { _ = serverPipe.Close() }()

	p := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	defer func() { _ = p.Close() }()

	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	assertTransportTools(t, tools, []string{"search", "fetch"})
}

// Stdio-002: Call 调用远程工具并返回 JSON 结果。
func TestStdioMCPProvider_Call(t *testing.T) {
	clientPipe, serverPipe := newPipePair()
	go mockStdioServer(t, serverPipe, testTools())
	defer func() { _ = serverPipe.Close() }()

	p := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	defer func() { _ = p.Close() }()

	res, err := p.Call(context.Background(), "search", json.RawMessage(`{"q":"x"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// 结果应包含 "called search"。
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(res, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.IsError {
		t.Error("unexpected isError=true")
	}
	if len(result.Content) == 0 || result.Content[0].Text != "called search" {
		t.Errorf("content = %+v, want text 'called search'", result.Content)
	}
}

// Stdio-003: Close 后再次调用会重新连接。
func TestStdioMCPProvider_CloseAndReconnect(t *testing.T) {
	clientPipe1, serverPipe1 := newPipePair()
	go mockStdioServer(t, serverPipe1, testTools())

	p := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe1, clientPipe1), nil
		},
	}

	// 首次连接。
	tools, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("first ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("expected tools")
	}

	// 关闭。
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 重新拨号（新 pipe）。
	clientPipe2, serverPipe2 := newPipePair()
	go mockStdioServer(t, serverPipe2, testTools())
	p.dial = func(_ context.Context) (rpcConn, error) {
		return newStdioConn(clientPipe2, clientPipe2), nil
	}

	// 重连后仍能工作。
	tools2, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("second ListTools: %v", err)
	}
	assertTransportTools(t, tools2, []string{"search", "fetch"})
}

// Stdio-004: Close 可多次调用不报错。
func TestStdioMCPProvider_CloseIdempotent(t *testing.T) {
	clientPipe, serverPipe := newPipePair()
	go mockStdioServer(t, serverPipe, testTools())

	p := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	_, _ = p.ListTools(context.Background())
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// Stdio-005: Timeout 导致上下文取消。
func TestStdioMCPProvider_Timeout(t *testing.T) {
	clientPipe, serverPipe := newPipePair()
	// 使用永不响应的 server：读取后不做任何写回。
	go func() {
		dec := json.NewDecoder(serverPipe)
		for {
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				return
			}
			// 故意不响应。
		}
	}()

	p := &StdioMCPProvider{
		Timeout: 50 * time.Millisecond,
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}
	defer func() { _ = p.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := p.ListTools(ctx)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// Stdio-006: 拨号失败时返回错误。
func TestStdioMCPProvider_DialError(t *testing.T) {
	p := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return nil, errDialFailed
		},
	}
	_, err := p.ListTools(context.Background())
	if err == nil {
		t.Fatal("expected dial error")
	}
}

var errDialFailed = &dialError{}

type dialError struct{}

func (e *dialError) Error() string { return "dial failed" }

// Stdio-007: server 关闭连接后，调用返回 io.ErrClosedPipe（而非永久阻塞）。
// 验证读泵 goroutine 退出前 close(c.msgs)，避免调用方永远阻塞在 channel 读取上。
func TestStdioMCPProvider_ServerCloseNotHang(t *testing.T) {
	clientPipe, serverPipe := newPipePair()
	// 启动 mock server，稍后关闭。
	go mockStdioServer(t, serverPipe, testTools())

	p := &StdioMCPProvider{
		dial: func(_ context.Context) (rpcConn, error) {
			return newStdioConn(clientPipe, clientPipe), nil
		},
	}

	// 首次 ListTools 成功。
	_, err := p.ListTools(context.Background())
	if err != nil {
		t.Fatalf("first ListTools: %v", err)
	}

	// 关闭 server 端 pipe，触发客户端读泵退出。
	_ = serverPipe.Close()

	// 后续 Call 应返回错误（而非永久阻塞）。
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = p.Call(ctx, "search", json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error after server close, got nil")
	}
}
