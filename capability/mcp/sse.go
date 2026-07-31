// SSE（Server-Sent Events）解析工具，复用于 streamable HTTP 与 SSE 传输。
//
// MCP 的两种基于 HTTP 的传输都会用 text/event-stream 承载 JSON-RPC 响应：
// - streamable HTTP：POST 请求后，响应可能是 SSE 流，结果作为 message 事件
// - SSE 传输：常驻 GET 流，先发 endpoint 事件告知 POST 端点，后续响应作为 message 事件

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// parseSSE 按行解析 SSE 流，对每个完整事件回调其 event 名与 data（多行 data 以 \n 拼接）。
//
// 事件以空行分隔；一行前缀 "event:" 设置事件名，"data:" 追加数据（去掉一个可选前导空格）。
// 解析持续到 reader 返回 EOF 或错误。
func parseSSE(r io.Reader, onEvent func(event, data string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var eventParts, dataParts []string
	flush := func() {
		if len(eventParts) == 0 && len(dataParts) == 0 {
			return
		}
		event := strings.Join(eventParts, "")
		data := strings.Join(dataParts, "\n")
		onEvent(event, data)
		eventParts, dataParts = nil, nil
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventParts = append(eventParts, strings.TrimSpace(strings.TrimPrefix(line, "event:")))
		case strings.HasPrefix(line, "data:"):
			// SSE 规范：去掉 "data:" 后一个可选前导空格。
			rest := strings.TrimPrefix(line, "data:")
			rest = strings.TrimPrefix(rest, " ")
			dataParts = append(dataParts, rest)
		case line[0] == ':':
			// 注释行，忽略。
		default:
			// 未知行，忽略。
		}
	}
	flush()
	return sc.Err()
}

// readSSEResponse 从 SSE 流中读取 JSON-RPC 响应，返回 id 匹配的那一条。
//
// 用于 streamable HTTP：POST 后响应体为 SSE 流，逐事件解码直到匹配。
func readSSEResponse(ctx context.Context, r io.Reader, wantID int64) (*rpcResponse, error) {
	type result struct {
		resp *rpcResponse
		err error
	}
	ch := make(chan result, 1)
	go func() {
		var done bool
		err := parseSSE(r, func(event, data string) {
			if done || data == "" {
				return
			}
			var resp rpcResponse
			if e := json.Unmarshal([]byte(data), &resp); e != nil {
				ch <- result{err: fmt.Errorf("mcp sse: decode event: %w", e)}
				done = true
				return
			}
			if resp.ID == wantID {
				ch <- result{resp: &resp}
				done = true
			}
		})
		if !done {
			if err != nil {
				ch <- result{err: fmt.Errorf("mcp sse: read stream: %w", err)}
			} else {
				ch <- result{err: fmt.Errorf("mcp sse: stream closed before response id %d", wantID)}
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}
