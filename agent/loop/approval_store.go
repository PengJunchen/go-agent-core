// Package loop 的审批决策持久化实现。
//
// ApprovalStore 将审批决策以 append-only JSONL 格式写入磁盘，
// 启动时加载已有决策填充缓存，实现重启后审批记忆持久化。
//
// 设计要点：
// - 热路径查缓存：GetDecision 仅查内存 map，零 IO
// - 冷路径写磁盘：Record 追加 JSONL 行，刷新后落盘
// - 缓存键 "sessionID:toolName"：同一会话同一工具只问一次
package loop

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ─── ApprovalEntry ───────────────────────────────────────────────

// ApprovalEntry 表示一条持久化的审批决策。
type ApprovalEntry struct {
	SessionID string `json:"session_id"`
	ToolName string `json:"tool_name"`
	Decision string `json:"decision"` // "allow" or "deny"
	Reason string `json:"reason,omitempty"`
	Timestamp int64 `json:"timestamp"`
}

// ─── ApprovalStore ───────────────────────────────────────────────

// ApprovalStore 将审批决策持久化到 JSONL 文件。
//
// 启动时加载已有决策填充缓存；运行时 Record 追加写入并更新缓存；
// 同一 session+tool 只需审批一次（缓存命中即返回）。
type ApprovalStore struct {
	mu sync.RWMutex
	filePath string
	cache map[string]string // key: "sessionID:toolName" → decision
	file *os.File
	writer *bufio.Writer
}

// NewApprovalStore 创建一个 ApprovalStore。
//
// 如果 filePath 已存在，加载已有决策到缓存。
// 如果父目录不存在则自动创建。
func NewApprovalStore(filePath string) (*ApprovalStore, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create directory %q: %w", dir, err)
	}

	s := &ApprovalStore{
		filePath: filePath,
		cache: make(map[string]string),
	}

	// 先加载已有数据
	if err := s.loadExisting(); err != nil {
		return nil, fmt.Errorf("load existing: %w", err)
	}

	// 打开文件用于追加写入
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open file %q: %w", filePath, err)
	}
	s.file = f
	s.writer = bufio.NewWriter(f)

	return s, nil
}

// Record 写入一条审批决策，同时更新缓存和 JSONL 文件。
func (s *ApprovalStore) Record(entry ApprovalEntry) error {
	key := entry.SessionID + ":" + entry.ToolName

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal approval entry: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("write approval entry: %w", err)
	}
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("flush approval entry: %w", err)
	}

	s.cache[key] = entry.Decision
	return nil
}

// GetDecision 返回指定会话+工具的缓存决策。
//
// 返回 ("", false) 表示无缓存决策。
func (s *ApprovalStore) GetDecision(sessionID, toolName string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := sessionID + ":" + toolName
	decision, ok := s.cache[key]
	return decision, ok
}

// Close 刷新缓冲区并关闭文件。
func (s *ApprovalStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.writer != nil {
		if err := s.writer.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.writer = nil
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.file = nil
	}
	return firstErr
}

// loadExisting 读取 JSONL 文件填充缓存。
func (s *ApprovalStore) loadExisting() error {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，正常
		}
		return fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = f.Close() }() // 只读打开，关闭错误无需处理

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var entry ApprovalEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // 跳过损坏行
		}
		key := entry.SessionID + ":" + entry.ToolName
		s.cache[key] = entry.Decision
	}

	return scanner.Err()
}
