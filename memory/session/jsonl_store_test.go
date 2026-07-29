package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// JSONLSessionStore 测试
//
// 测试策略：
// - 每个测试使用独立的临时文件，避免互相干扰
// - 覆盖 CRUD 全流程、幂等性、状态转换、持久化恢复和并发安全

// newTestStore 创建测试用 JSONLSessionStore（临时文件路径，调用者负责 cleanup）。
func newTestStore(t *testing.T) (*JSONLSessionStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	store, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore: %v", err)
	}
	return store, path
}

// newTestCtx 返回一个基础的测试上下文。
func newTestCtx() context.Context {
	return context.Background()
}

// JT-001: 创建会话后 GetSession 能正确返回。
func TestJSONLSessionStore_CreateAndGet(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected non-empty session ID")
	}
	if sess.Status != SessionActive {
		t.Errorf("expected active status, got %s", sess.Status)
	}
	if sess.ContextID != "ctx-1" {
		t.Errorf("expected context_id=ctx-1, got %s", sess.ContextID)
	}

	got, err := store.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("session ID mismatch: %s vs %s", got.ID, sess.ID)
	}
}

// JT-002: 相同 ContextID 重复创建返回同一会话（幂等）。
func TestJSONLSessionStore_CreateSessionIdempotent(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	sess1, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-dup"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	sess2, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-dup"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if sess1.ID != sess2.ID {
		t.Errorf("expected same session ID for same contextID, got %s vs %s", sess1.ID, sess2.ID)
	}
}

// JT-003: 空 ContextID 每次创建新会话。
func TestJSONLSessionStore_CreateNewOnEmptyContext(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	sess1, err := store.CreateSession(ctx, nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess2, err := store.CreateSession(ctx, nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess1.ID == sess2.ID {
		t.Error("expected different session IDs for nil contextID")
	}
}

// JT-004: GetSession 对不存在的 ID 返回错误。
func TestJSONLSessionStore_GetNonExistent(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	_, err := store.GetSession(ctx, "non-existent")
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

// JT-005: UpdateSession 更新状态。
func TestJSONLSessionStore_UpdateStatus(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-upd"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	completed := SessionCompleted
	err = store.UpdateSession(ctx, sess.ID, &SessionUpdate{Status: &completed})
	if err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, err := store.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != SessionCompleted {
		t.Errorf("expected completed status, got %s", got.Status)
	}
}

// JT-006: UpdateSession 对不存在会话返回错误。
func TestJSONLSessionStore_UpdateNonExistent(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	completed := SessionCompleted
	err := store.UpdateSession(ctx, "non-existent", &SessionUpdate{Status: &completed})
	if err == nil {
		t.Fatal("expected error for non-existent session update")
	}
}

// JT-007: 非法状态转换被拒绝。
func TestJSONLSessionStore_InvalidTransition(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-trn"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// active -> failed 合法
	failed := SessionFailed
	if err := store.UpdateSession(ctx, sess.ID, &SessionUpdate{Status: &failed}); err != nil {
		t.Fatalf("active->failed should be valid: %v", err)
	}

	// failed -> active 非法
	active := SessionActive
	if err := store.UpdateSession(ctx, sess.ID, &SessionUpdate{Status: &active}); err == nil {
		t.Fatal("expected error for failed->active transition")
	}
}

// JT-008: DeleteSession 标记为已取消。
func TestJSONLSessionStore_DeleteSession(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-del"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := store.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	got, err := store.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != SessionCanceled {
		t.Errorf("expected canceled status, got %s", got.Status)
	}
}

// JT-009: DeleteSession 对不存在的会话返回错误。
func TestJSONLSessionStore_DeleteNonExistent(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	if err := store.DeleteSession(ctx, "non-existent"); err == nil {
		t.Fatal("expected error for non-existent session delete")
	}
}

// JT-010: ListSessions 列出所有会话。
func TestJSONLSessionStore_ListSessions(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	// 创建 3 个会话
	for i := 0; i < 3; i++ {
		_, err := store.CreateSession(ctx, nil)
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}

	sessions, err := store.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

// JT-011: ListSessions 按 ContextID 过滤。
func TestJSONLSessionStore_ListByContextID(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	store.CreateSession(ctx, &SessionOptions{ContextID: "group-a"})
	store.CreateSession(ctx, &SessionOptions{ContextID: "group-b"})
	store.CreateSession(ctx, &SessionOptions{ContextID: "group-a"})

	sessions, err := store.ListSessions(ctx, &ListOptions{ContextID: "group-a"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 { // group-a 幂等只创建一个
		t.Errorf("expected 1 session for group-a, got %d", len(sessions))
	}

	sessions, err = store.ListSessions(ctx, &ListOptions{ContextID: "group-b"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session for group-b, got %d", len(sessions))
	}
}

// JT-012: ListSessions 按 Status 过滤。
func TestJSONLSessionStore_ListByStatus(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	// 创建活跃会话
	active1, _ := store.CreateSession(ctx, &SessionOptions{ContextID: "active-1"})
	store.CreateSession(ctx, &SessionOptions{ContextID: "active-2"})

	// 完成一个
	completed := SessionCompleted
	store.UpdateSession(ctx, active1.ID, &SessionUpdate{Status: &completed})

	sessions, err := store.ListSessions(ctx, &ListOptions{Status: SessionActive})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 active session, got %d", len(sessions))
	}

	sessions, err = store.ListSessions(ctx, &ListOptions{Status: SessionCompleted})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 completed session, got %d", len(sessions))
	}
}

// JT-013: ListSessions 按 Limit 限制返回数。
func TestJSONLSessionStore_ListWithLimit(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	for i := 0; i < 5; i++ {
		store.CreateSession(ctx, nil)
	}

	sessions, err := store.ListSessions(ctx, &ListOptions{Limit: 3})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) > 3 {
		t.Errorf("expected at most 3 sessions, got %d", len(sessions))
	}
}

// JT-014: 持久化恢复 — 创建会话后重新加载应恢复状态。
func TestJSONLSessionStore_PersistenceRecovery(t *testing.T) {
	store, path := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-rec"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 模拟重启：新建 store 回放相同文件
	store2, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (recovery): %v", err)
	}

	recovered, err := store2.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession after recovery: %v", err)
	}
	if recovered.ID != sess.ID {
		t.Errorf("session ID mismatch after recovery")
	}
	if recovered.ContextID != sess.ContextID {
		t.Errorf("context ID mismatch after recovery")
	}
	if recovered.Status != sess.Status {
		t.Errorf("status mismatch after recovery")
	}
}

// JT-015: 持久化恢复 — 更新和删除也应恢复。
func TestJSONLSessionStore_PersistenceUpdateRecovery(t *testing.T) {
	store, path := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-up-rec"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 更新状态
	completed := SessionCompleted
	if err := store.UpdateSession(ctx, sess.ID, &SessionUpdate{Status: &completed}); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	// 模拟重启
	store2, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (recovery): %v", err)
	}

	recovered, err := store2.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession after recovery: %v", err)
	}
	if recovered.Status != SessionCompleted {
		t.Errorf("expected completed status after recovery, got %s", recovered.Status)
	}
}

// JT-016: 持久化恢复 — 删除操作也应恢复。
func TestJSONLSessionStore_PersistenceDeleteRecovery(t *testing.T) {
	store, path := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-del-rec"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 删除会话
	if err := store.DeleteSession(ctx, sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	// 模拟重启
	store2, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (recovery): %v", err)
	}

	recovered, err := store2.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession after recovery: %v", err)
	}
	if recovered.Status != SessionCanceled {
		t.Errorf("expected canceled status after recovery, got %s", recovered.Status)
	}
}

// JT-017: 持久化恢复后 ContextID 索引应正确。
func TestJSONLSessionStore_PersistenceContextIDIndex(t *testing.T) {
	store, path := newTestStore(t)
	ctx := newTestCtx()

	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-idx"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 模拟重启
	store2, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (recovery): %v", err)
	}

	// 通过幂等性验证 ContextID 索引正确
	recovered, err := store2.CreateSession(ctx, &SessionOptions{ContextID: "ctx-idx"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if recovered.ID != sess.ID {
		t.Errorf("expected same session for contextID after recovery, got %s vs %s",
			recovered.ID, sess.ID)
	}
}

// JT-018: 并发安全 — 并发创建不 panic。
func TestJSONLSessionStore_ConcurrentCreate(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := newTestCtx()

	const goroutines = 10
	done := make(chan struct{}, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			_, err := store.CreateSession(ctx, &SessionOptions{ContextID: fmt.Sprintf("conc-%d", idx)})
			if err != nil {
				t.Errorf("concurrent CreateSession: %v", err)
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-done
	}

	sessions, err := store.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != goroutines {
		t.Errorf("expected %d sessions, got %d", goroutines, len(sessions))
	}
}

// JT-019: 初始化时目录自动创建（文件在首次写入时创建）。
func TestJSONLSessionStore_AutoCreateDir(t *testing.T) {
	dir := t.TempDir()
	deepPath := filepath.Join(dir, "sub", "nested", "store.jsonl")

	store, err := NewJSONLSessionStore(deepPath)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (deep path): %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// 写入一条记录，文件应自动创建
	ctx := context.Background()
	_, err = store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-dir"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 验证文件已创建
	if _, err := os.Stat(deepPath); os.IsNotExist(err) {
		t.Fatal("expected file to be created after write")
	}
}

// JT-020: 破坏的文件行被跳过，不影响恢复。
func TestJSONLSessionStore_CorruptedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupted.jsonl")

	// 写入一些有效数据 + 破坏的数据
	store, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore: %v", err)
	}

	ctx := newTestCtx()
	sess, err := store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-cor"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 手动追加一行破坏的数据
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("not-json\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	// 恢复应跳过破坏行
	store2, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (recovery): %v", err)
	}

	recovered, err := store2.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession after corruption: %v", err)
	}
	if recovered.ID != sess.ID {
		t.Errorf("session ID mismatch after corruption recovery")
	}
}

// JT-021: validTransition 单元测试。
func TestValidTransition(t *testing.T) {
	tests := []struct {
		current SessionStatus
		target SessionStatus
		valid bool
	}{
		{SessionActive, SessionActive, true},
		{SessionActive, SessionCompleted, true},
		{SessionActive, SessionFailed, true},
		{SessionActive, SessionCanceled, true},
		{SessionCompleted, SessionActive, false},
		{SessionCompleted, SessionFailed, false},
		{SessionFailed, SessionActive, false},
		{SessionCanceled, SessionActive, false},
	}

	for _, tt := range tests {
		err := validTransition(tt.current, tt.target)
		if tt.valid && err != nil {
			t.Errorf("validTransition(%s, %s) should succeed, got: %v", tt.current, tt.target, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("validTransition(%s, %s) should fail", tt.current, tt.target)
		}
	}
}

// JT-022: 空文件创建 store 不报错。
func TestJSONLSessionStore_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")

	store, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (empty file): %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

// JT-023: 恢复空文件不报错。
func TestJSONLSessionStore_RecoverEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty-recover.jsonl")

	store, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore: %v", err)
	}

	// 向空文件追加有效数据
	ctx := newTestCtx()
	_, err = store.CreateSession(ctx, &SessionOptions{ContextID: "ctx-emr"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 恢复
	store2, err := NewJSONLSessionStore(path)
	if err != nil {
		t.Fatalf("NewJSONLSessionStore (recovery): %v", err)
	}

	sessions, err := store2.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
}

func TestJSONLSessionStore_Interface(t *testing.T) {
	// JT-024: 编译期接口实现检查
	var _ SessionManager = (*JSONLSessionStore)(nil)
}
