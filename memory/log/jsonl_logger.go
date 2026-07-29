// Package log 的 JSONL ExecLogger 默认实现。
//
// JSONLExecLogger 将执行日志永远写入 JSONL 文件（append-only）。
// 写入失败不阻塞主流程：记入 crash 旁路日志，调用方仍可感知错误。
package log

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// JSONLExecLogger 是默认的 ExecLogger 实现。
//
// 日志文件按日期轮转：session_YYYYMMDD.jsonl。写入使用 bufio.Writer
// 缓冲，Flush 强制刷盘。Close 关闭前自动 Flush。
type JSONLExecLogger struct {
	mu sync.Mutex
	dataDir string
	filePat string
	crashLog string
	currentFD *os.File
	writer *bufio.Writer
	current string
}

// NewJSONLExecLogger 构造一个 JSONL 日志器。
//
// dataDir 是日志目录；不存在则创建。filePattern 支持 {{.Date}} 占位符。
func NewJSONLExecLogger(dataDir, filePattern string) (*JSONLExecLogger, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("log: mkdir %s: %w", dataDir, err)
	}
	return &JSONLExecLogger{
		dataDir: dataDir,
		filePat: filePattern,
		crashLog: filepath.Join(dataDir, "crash.log"),
	}, nil
}

// Log 写入一条日志条目。写入失败不 panic，记录旁路并返回错误。
func (l *JSONLExecLogger) Log(ctx context.Context, entry *ExecLogEntry) error {
	if entry == nil {
		return nil
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return l.crash(fmt.Errorf("log: marshal: %w", err))
	}
	data = append(data, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureWriter(); err != nil {
		return l.crash(err)
	}
	if _, err := l.writer.Write(data); err != nil {
		return l.crash(fmt.Errorf("log: write: %w", err))
	}
	return nil
}

// Flush 强制刷盘。
func (l *JSONLExecLogger) Flush(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.writer == nil {
		return nil
	}
	if err := l.writer.Flush(); err != nil {
		return l.crash(fmt.Errorf("log: flush: %w", err))
	}
	return nil
}

// Close 关闭日志器，关闭前自动 Flush。
func (l *JSONLExecLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	if l.writer != nil {
		if err := l.writer.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		l.writer = nil
	}
	if l.currentFD != nil {
		if err := l.currentFD.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		l.currentFD = nil
	}
	return firstErr
}

// ensureWriter 确保当前 writer 指向正确日期的文件（调用方持锁）。
func (l *JSONLExecLogger) ensureWriter() error {
	dateStr := time.Now().UTC().Format("20060102")
	name := strings.ReplaceAll(l.filePat, "{{.Date}}", dateStr)
	if name == l.current && l.writer != nil {
		return nil
	}
	// 轮转：刷旧文件
	if l.writer != nil {
		_ = l.writer.Flush()
	}
	if l.currentFD != nil {
		_ = l.currentFD.Close()
	}
	path := filepath.Join(l.dataDir, name)
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("log: open %s: %w", path, err)
	}
	l.currentFD = fd
	l.writer = bufio.NewWriter(fd)
	l.current = name
	return nil
}

// crash 将错误写入旁路日志并返回原错误。
func (l *JSONLExecLogger) crash(err error) error {
	msg := fmt.Sprintf("%s %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
	_ = os.WriteFile(l.crashLog, []byte(msg), 0o644)
	return err
}
