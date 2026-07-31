// Package session 定义会话管理抽象。
//
// JSONLSessionStore 是 SessionManager 的 JSONL 文件持久化实现。
// 每个 CRUD 操作以 append-only JSONL 条目写入磁盘。启动时回放已有条目重建内存状态，
// 支持 kill-9 后恢复。
package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// defaultFileIOTimeout 是文件 IO 操作的默认超时时间。
const defaultFileIOTimeout = 5 * time.Second

// jsonlEntry 是写入磁盘的顶层 JSONL 记录结构。
type jsonlEntry struct {
	EntryType string `json:"entry_type"`
	EntryData json.RawMessage `json:"entry_data"`
	Timestamp string `json:"timestamp"`
}

// sessionCreateData 是 entry_type=session_create 的负载。
type sessionCreateData struct {
	ID string `json:"id"`
	ContextID string `json:"context_id"`
	Status SessionStatus `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// sessionUpdateData 是 entry_type=session_update 的负载。
type sessionUpdateData struct {
	SessionID string `json:"session_id"`
	Status SessionStatus `json:"status,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// sessionDeleteData 是 entry_type=session_delete 的负载。
type sessionDeleteData struct {
	SessionID string `json:"session_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Compile-time interface satisfaction check.
var _ SessionManager = (*JSONLSessionStore)(nil)

// JSONLSessionStore 是 SessionManager 的 JSONL 文件持久化实现。
//
// 线程安全：读写锁保护内存状态，文件锁序列化 append 写入。
// 启动时如果文件已存在则回放重建内存状态。目录自动创建。
type JSONLSessionStore struct {
	mu sync.RWMutex
	fileMu sync.Mutex // 序列化文件写入
	filePath string
	sessions map[string]*Session
	byContextID map[string]*Session // contextID → Session
	sink SessionSink // 可选：持久化委托给 SessionSink
}

// NewJSONLSessionStore 创建一个指定文件路径的 JSONLSessionStore。
// 如果文件已存在，回放条目重建内存状态。
// 如果父目录不存在则自动创建。
func NewJSONLSessionStore(filePath string) (*JSONLSessionStore, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create directory %q: %w", dir, err)
	}

	s := &JSONLSessionStore{
		filePath: filePath,
		sessions: make(map[string]*Session),
		byContextID: make(map[string]*Session),
	}

	if err := s.loadFromFile(); err != nil {
		return nil, fmt.Errorf("load from file %q: %w", filePath, err)
	}

	slog.Info("jsonl session store initialized", "op", "persist_init",
		"backend", "jsonl", "path", filePath, "sessions_loaded", len(s.sessions))

	return s, nil
}

// CreateSession 创建一个新会话并持久化。
// 如果 opts.ContextID 非空且已有同 ContextID 的活跃会话，返回该会话（幂等）。
func (s *JSONLSessionStore) CreateSession(ctx context.Context, opts *SessionOptions) (*Session, error) {
	// 如果 ContextID 非空，检查是否已有会话
	if opts != nil && opts.ContextID != "" {
		s.mu.RLock()
		if sess, ok := s.byContextID[opts.ContextID]; ok {
			s.mu.RUnlock()
			return sess, nil
		}
		s.mu.RUnlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 二次检查（并发安全）
	if opts != nil && opts.ContextID != "" {
		if sess, ok := s.byContextID[opts.ContextID]; ok {
			return sess, nil
		}
	}

	now := time.Now()
	contextID := ""
	if opts != nil {
		contextID = opts.ContextID
	}

	sess := &Session{
		ID: newSessionID(),
		ContextID: contextID,
		Status: SessionActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.sessions[sess.ID] = sess
	if contextID != "" {
		s.byContextID[contextID] = sess
	}

	// 持久化到 JSONL
	if err := s.appendCreateEntry(ctx, sess); err != nil {
		slog.Error("failed to persist session_create", "op", "persist",
			"backend", "jsonl", "error", err, "session_id", sess.ID)
		// 内存状态已更新，持久化失败仅记录日志不致命
	}

	slog.Info("session created", "op", "persist",
		"entry_type", "session_create",
		"session_id", sess.ID, "context_id", contextID)

	return sess, nil
}

// GetSession 通过会话 ID 查询会话。
func (s *JSONLSessionStore) GetSession(_ context.Context, sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return sess, nil
}

// UpdateSession 更新指定会话的字段。
// 当前支持更新 Status，未来可扩展。
func (s *JSONLSessionStore) UpdateSession(ctx context.Context, sessionID string, update *SessionUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if update == nil {
		return fmt.Errorf("update must not be nil")
	}

	if update.Status != nil {
		if err := validTransition(sess.Status, *update.Status); err != nil {
			return err
		}
		sess.Status = *update.Status
	}
	sess.UpdatedAt = time.Now()

	// 持久化到 JSONL
	updateData := &sessionUpdateData{
		SessionID: sessionID,
		UpdatedAt: sess.UpdatedAt,
	}
	if update.Status != nil {
		updateData.Status = *update.Status
	}

	if err := s.appendUpdateEntry(ctx, updateData); err != nil {
		slog.Error("failed to persist session_update", "op", "persist",
			"backend", "jsonl", "error", err, "session_id", sessionID)
	}

	slog.Info("session updated", "op", "persist",
		"entry_type", "session_update",
		"session_id", sessionID, "status", string(sess.Status))

	return nil
}

// DeleteSession 删除指定会话并持久化。
// 会话被标记为已取消而不是物理删除。
func (s *JSONLSessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	sess.Status = SessionCanceled
	sess.UpdatedAt = time.Now()

	// 从 ContextID 索引移除
	if sess.ContextID != "" && s.byContextID[sess.ContextID] == sess {
		delete(s.byContextID, sess.ContextID)
	}

	// 持久化到 JSONL
	if err := s.appendDeleteEntry(ctx, sessionID, sess.UpdatedAt); err != nil {
		slog.Error("failed to persist session_delete", "op", "persist",
			"backend", "jsonl", "error", err, "session_id", sessionID)
	}

	slog.Info("session deleted", "op", "persist",
		"entry_type", "session_delete",
		"session_id", sessionID)

	return nil
}

// ListSessions 按过滤条件列出会话。
// 支持的过滤：ContextID、Status。Limit 限制返回条数（0 表示不限制）。
// 返回按 UpdatedAt 降序排列。
func (s *JSONLSessionStore) ListSessions(_ context.Context, opts *ListOptions) ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*Session
	for _, sess := range s.sessions {
		if opts != nil {
			if opts.ContextID != "" && sess.ContextID != opts.ContextID {
				continue
			}
			if opts.Status != "" && sess.Status != opts.Status {
				continue
			}
		}
		result = append(result, sess)
	}

	// 按 UpdatedAt 降序
	sortSessionsByUpdatedAtDesc(result)

	if opts != nil && opts.Limit > 0 && len(result) > opts.Limit {
		result = result[:opts.Limit]
	}

	return result, nil
}

// sortSessionsByUpdatedAtDesc 按 UpdatedAt 降序排序。
func sortSessionsByUpdatedAtDesc(sessions []*Session) {
	for i := 0; i < len(sessions); i++ {
		for j := i + 1; j < len(sessions); j++ {
			if sessions[j].UpdatedAt.After(sessions[i].UpdatedAt) {
				sessions[i], sessions[j] = sessions[j], sessions[i]
			}
		}
	}
}

// validTransition 校验状态转换是否合法。
func validTransition(current, target SessionStatus) error {
	if current == target {
		return nil
	}
	switch current {
	case SessionActive:
		switch target {
		case SessionCompleted, SessionFailed, SessionCanceled:
			return nil
		}
	case SessionCompleted, SessionFailed, SessionCanceled:
		return fmt.Errorf("cannot transition from %s to %s", current, target)
	}
	return fmt.Errorf("invalid transition from %s to %s", current, target)
}

// appendCreateEntry 写入 session_create 条目。
func (s *JSONLSessionStore) appendCreateEntry(ctx context.Context, sess *Session) error {
	data := sessionCreateData{
		ID: sess.ID,
		ContextID: sess.ContextID,
		Status: sess.Status,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
	}
	return s.appendEntry(ctx, "session_create", data)
}

// appendUpdateEntry 写入 session_update 条目。
func (s *JSONLSessionStore) appendUpdateEntry(ctx context.Context, data *sessionUpdateData) error {
	return s.appendEntry(ctx, "session_update", data)
}

// appendDeleteEntry 写入 session_delete 条目。
func (s *JSONLSessionStore) appendDeleteEntry(ctx context.Context, sessionID string, updatedAt time.Time) error {
	data := sessionDeleteData{
		SessionID: sessionID,
		UpdatedAt: updatedAt,
	}
	return s.appendEntry(ctx, "session_delete", data)
}

// appendEntry 序列化并追加一条 JSONL 条目到文件（带超时保护）。
func (s *JSONLSessionStore) appendEntry(ctx context.Context, entryType string, data any) error {
	// 优先委托给 SessionSink（热冷分离：Sink 管冷路径持久化）
	if s.sink != nil {
		sessID := ""
		if sd, ok := data.(interface{ GetSessionID() string }); ok {
			sessID = sd.GetSessionID()
		}
		if err := s.sink.Append(ctx, SessionEntry{
			EntryType: entryType,
			SessionID: sessID,
			Data: data,
		}); err != nil {
			// Sink 失败不阻断主流程，降级到本地文件
		}
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("persist %s: %w", entryType, ctx.Err())
	default:
	}

	rawData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal entry_data for %s: %w", entryType, err)
	}

	entry := jsonlEntry{
		EntryType: entryType,
		EntryData: rawData,
		Timestamp: time.Now().Format(time.RFC3339Nano),
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry for %s: %w", entryType, err)
	}
	line = append(line, '\n')

	return s.writeToFile(ctx, line)
}

// writeToFile 带超时保护追加字节到 JSONL 文件。
func (s *JSONLSessionStore) writeToFile(ctx context.Context, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, defaultFileIOTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		s.fileMu.Lock()
		defer s.fileMu.Unlock()

		f, err := os.OpenFile(s.filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			done <- fmt.Errorf("open file: %w", err)
			return
		}
		defer func() { _ = f.Close() }() // append-only 写入，关闭错误无需处理

		if _, err := f.Write(data); err != nil {
			done <- fmt.Errorf("write file: %w", err)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("file IO timed out: %w", ctx.Err())
	}
}

// loadFromFile 回放已有 JSONL 条目重建内存状态。
func (s *JSONLSessionStore) loadFromFile() error {
	f, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，正常
		}
		return fmt.Errorf("open file: %w", err)
	}
	defer func() { _ = f.Close() }() // 只读打开，关闭错误无需处理

	scanner := bufio.NewScanner(f)
	corruptedLines := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		var entry jsonlEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			corruptedLines++
			continue
		}

		switch entry.EntryType {
		case "session_create":
			var data sessionCreateData
			if err := json.Unmarshal(entry.EntryData, &data); err != nil {
				corruptedLines++
				continue
			}
			sess := &Session{
				ID: data.ID,
				ContextID: data.ContextID,
				Status: data.Status,
				CreatedAt: data.CreatedAt,
				UpdatedAt: data.UpdatedAt,
			}
			s.sessions[sess.ID] = sess
			if sess.ContextID != "" {
				s.byContextID[sess.ContextID] = sess
			}

		case "session_update":
			var data sessionUpdateData
			if err := json.Unmarshal(entry.EntryData, &data); err != nil {
				corruptedLines++
				continue
			}
			sess, ok := s.sessions[data.SessionID]
			if !ok {
				continue // 孤儿更新，跳过
			}
			if data.Status != "" {
				sess.Status = data.Status
			}
			if !data.UpdatedAt.IsZero() {
				sess.UpdatedAt = data.UpdatedAt
			}

		case "session_delete":
			var data sessionDeleteData
			if err := json.Unmarshal(entry.EntryData, &data); err != nil {
				corruptedLines++
				continue
			}
			sess, ok := s.sessions[data.SessionID]
			if !ok {
				continue
			}
			sess.Status = SessionCanceled
			if !data.UpdatedAt.IsZero() {
				sess.UpdatedAt = data.UpdatedAt
			}
			if sess.ContextID != "" && s.byContextID[sess.ContextID] == sess {
				delete(s.byContextID, sess.ContextID)
			}
		}
	}

	if corruptedLines > 0 {
		slog.Warn("corrupted lines skipped during recovery", "op", "persist_recover",
			"backend", "jsonl", "corrupted_lines", corruptedLines, "path", s.filePath)
	}

	return scanner.Err()
}

// newSessionID generates a new session ID using crypto/rand.
func newSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use timestamp + counter (should never happen)
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	// Format as UUID-like: 8-4-4-4-12
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return hex.EncodeToString(b[:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:])
}

// SetSink 设置可选的 SessionSink。设置后，所有持久化操作同时委托给 Sink。
// SessionSink 管冷路径（全量落盘+重建），SessionStore 管热路径（内存缓存+文件持久化）。
func (s *JSONLSessionStore) SetSink(sink SessionSink) {
	s.sink = sink
}
