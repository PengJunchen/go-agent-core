// stdio 传输 MCPProvider 实现。
//
// StdioMCPProvider 通过启动子进程并经 stdin/stdout 交换换行分隔的 JSON-RPC
// 消息来连接 MCP server。这是最常见的本地 MCP server 传输方式（参考 Trae CLI
// 配置格式）。
//
//

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// StdioMCPProvider 通过子进程 stdin/stdout 与 MCP server 通信。
type StdioMCPProvider struct {
	Command string // 可执行文件路径
	Args []string // 参数列表
	Env map[string]string // 环境变量
	Timeout time.Duration // 单次调用超时

	// dial 允许测试注入替代的 rpcConn；nil 时使用默认 exec.Command 实现。
	dial func(ctx context.Context) (rpcConn, error)

	mu sync.Mutex
	base *baseTransport
}

// ensureBase 惰性建立 baseTransport（首次调用时 connect）。
func (p *StdioMCPProvider) ensureBase(ctx context.Context) (*baseTransport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.base != nil {
		return p.base, nil
	}
	dial := p.dial
	if dial == nil {
		dial = p.defaultDial
	}
	conn, err := dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: dial: %w", err)
	}
	p.base = newBaseTransport(p.Timeout, conn)
	return p.base, nil
}

// ListTools 列出 MCP server 暴露的工具。
func (p *StdioMCPProvider) ListTools(ctx context.Context) ([]Tool, error) {
	bt, err := p.ensureBase(ctx)
	if err != nil {
		return nil, err
	}
	return bt.ListTools(ctx)
}

// Call 调用 MCP server 上的远程工具。
func (p *StdioMCPProvider) Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error) {
	bt, err := p.ensureBase(ctx)
	if err != nil {
		return nil, err
	}
	return bt.Call(ctx, toolName, args)
}

// Close 关闭子进程连接。可多次调用。
func (p *StdioMCPProvider) Close() error {
	p.mu.Lock()
	bt := p.base
	p.base = nil
	p.mu.Unlock()
	if bt == nil {
		return nil
	}
	return bt.Close()
}

// ─── 默认子进程拨号 ──────────────────────────────────────────────

func (p *StdioMCPProvider) defaultDial(ctx context.Context) (rpcConn, error) {
	// 不使用 CommandContext：进程生命周期由 Close 管理，而非 per-call ctx。
	cmd := exec.Command(p.Command, p.Args...)
	if len(p.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range p.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("mcp stdio: start process: %w", err)
	}
	rw := &stdioRW{w: stdin, r: stdout}
	closer := &processCloser{cmd: cmd, stdin: stdin, stdout: stdout}
	return newStdioConn(rw, closer), nil
}

// ─── stdioConn — 换行分隔 JSON-RPC over io.ReadWriter ────────────

// readResult 是从读泵 goroutine 传递的解码结果。
type readResult struct {
	resp rpcResponse
	err error
}

type stdioConn struct {
	enc *json.Encoder
	msgs chan readResult // 读泵输出
	closer io.Closer
	mu sync.Mutex // 保护 enc 写入
}

func newStdioConn(rw io.ReadWriter, closer io.Closer) *stdioConn {
	c := &stdioConn{
		enc: json.NewEncoder(rw),
		msgs: make(chan readResult, 64),
		closer: closer,
	}
	dec := json.NewDecoder(rw)
	// 独占读泵 goroutine：独占 dec，无数据竞争。
	go func() {
		defer close(c.msgs) // 退出前关闭 channel，避免调用方永久阻塞
		for {
			var resp rpcResponse
			if err := dec.Decode(&resp); err != nil {
				c.msgs <- readResult{err: err}
				return
			}
			c.msgs <- readResult{resp: resp}
		}
	}()
	return c
}

func (c *stdioConn) call(ctx context.Context, req *rpcRequest) (*rpcResponse, error) {
	c.mu.Lock()
	err := c.enc.Encode(req)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r, ok := <-c.msgs:
			if !ok {
				return nil, io.ErrClosedPipe
			}
			if r.err != nil {
				return nil, r.err
			}
			if r.resp.ID == req.ID {
				return &r.resp, nil
			}
			// 非 matching（server 通知等），跳过继续等待。
		}
	}
}

func (c *stdioConn) notify(_ context.Context, n *rpcNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enc.Encode(n)
}

func (c *stdioConn) close() error {
	if c.closer != nil {
		return c.closer.Close()
	}
	return nil
}

// ─── 辅助类型 ────────────────────────────────────────────────────

// stdioRW 将独立的 io.Reader + io.Writer 组合为 io.ReadWriter。
type stdioRW struct {
	r io.Reader
	w io.Writer
}

func (rw *stdioRW) Read(p []byte) (int, error) { return rw.r.Read(p) }
func (rw *stdioRW) Write(p []byte) (int, error) { return rw.w.Write(p) }

// processCloser 关闭子进程的管道并终止进程。
type processCloser struct {
	cmd *exec.Cmd
	stdin io.Closer
	stdout io.Closer
}

func (pc *processCloser) Close() error {
	_ = pc.stdin.Close()
	_ = pc.stdout.Close()
	if pc.cmd.Process != nil {
		_ = pc.cmd.Process.Kill()
		_ = pc.cmd.Wait()
	}
	return nil
}
