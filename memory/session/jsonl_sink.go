// Package session 的 JSONLSessionSink 实现。
//
// 将会话树条目 append-only 写入 sessions/*.jsonl，
// 支持从文件重建完整会话树（kill-9 恢复）。
package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Compile-time interface satisfaction check.
var _ SessionSink = (*JSONLSessionSink)(nil)

// JSONLSessionSink 是 SessionSink 的 JSONL 文件持久化实现。
//
// 线程安全：互斥锁保护文件写入。
// append-only：历史条目不修改，只追加。
// 重建：LoadTree 逐行反序列化重建会话树。
type JSONLSessionSink struct {
	mu sync.Mutex
	dataDir string
	pattern string
	current string
	fd *os.File
	writer *bufio.Writer
}

// NewJSONLSessionSink 创建一个 JSONLSessionSink。
//
// dataDir 是 sessions 子目录的父目录（自动创建 dataDir/sessions/）。
// filePattern 支持 {{.Date}} 占位符。
func NewJSONLSessionSink(dataDir, filePattern string) (*JSONLSessionSink, error) {
	if filePattern == "" {
		filePattern = "session_{{.Date}}.jsonl"
	}
	dir := filepath.Join(dataDir, "sessions")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("session: mkdir %q: %w", dir, err)
	}
	return &JSONLSessionSink{
		dataDir: dir,
		pattern: filePattern,
	}, nil
}

// Append 追加一条会话树条目。
func (s *JSONLSessionSink) Append(ctx context.Context, entry SessionEntry) error {
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("session: marshal entry: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureWriter(); err != nil {
		return err
	}
	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("session: write: %w", err)
	}
	return nil
}

// LoadTree 从持久化文件重建指定会话的完整会话树。
func (s *JSONLSessionSink) LoadTree(ctx context.Context, sessionID string) (*SessionTreeData, error) {
	tree := &SessionTreeData{
		SessionID: sessionID,
		Branches: []BranchInfo{},
		Entries: []SessionEntry{},
	}
	files, err := filepath.Glob(filepath.Join(s.dataDir, "*.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("session: glob: %w", err)
	}
	for _, f := range files {
		entries, err := s.scanFile(f, sessionID)
		if err != nil {
			return nil, err
		}
		tree.Entries = append(tree.Entries, entries...)
	}
	// 从 entries 重建分支信息
	for _, e := range tree.Entries {
		if e.EntryType == "branch" {
			branchID, _ := e.Metadata["branch_id"].(string)
			parentID := e.ParentID
			tree.Branches = append(tree.Branches, BranchInfo{
				BranchID: branchID,
				ParentID: parentID,
				CreatedAt: parseTime(e.Timestamp),
			})
		}
	}
	return tree, nil
}

// Flush 强制刷盘。
func (s *JSONLSessionSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writer == nil {
		return nil
	}
	return s.writer.Flush()
}

// Close 关闭文件（关闭前自动 Flush）。
func (s *JSONLSessionSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var firstErr error
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.writer = nil
	}
	if s.fd != nil {
		if err := s.fd.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.fd = nil
	}
	return firstErr
}

// ensureWriter 确保写入器指向当前日期文件（调用方持锁）。
func (s *JSONLSessionSink) ensureWriter() error {
	dateStr := time.Now().UTC().Format("2006-01-02")
	name := replaceDate(s.pattern, dateStr)
	if name == s.current && s.writer != nil {
		return nil
	}
	if s.writer != nil {
		_ = s.writer.Flush() // Close 时刷新缓冲
	}
	if s.fd != nil {
		_ = s.fd.Close() // Close 时关闭文件句柄
	}
	path := filepath.Join(s.dataDir, name)
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("session: open %q: %w", path, err)
	}
	s.fd = fd
	s.writer = bufio.NewWriter(fd)
	s.current = name
	return nil
}

// scanFile 扫描文件，返回匹配 sessionID 的条目。
func (s *JSONLSessionSink) scanFile(path, sessionID string) ([]SessionEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var results []SessionEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.SessionID == sessionID {
			results = append(results, entry)
		}
	}
	return results, scanner.Err()
}

func replaceDate(pattern, date string) string {
	out := make([]byte, 0, len(pattern))
	i := 0
	for i < len(pattern) {
		if i+8 <= len(pattern) && pattern[i:i+8] == "{{.Date}}" {
			out = append(out, []byte(date)...)
			i += 8
		} else {
			out = append(out, pattern[i])
			i++
		}
	}
	return string(out)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
