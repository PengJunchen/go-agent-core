package loop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ─── ApprovalStore 测试 ──────────────────────────────────────────

// AS-001: Record 写入 JSONL 文件。
func TestApprovalStore_RecordWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.jsonl")

	store, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	entry := ApprovalEntry{
		SessionID: "sess-1",
		ToolName: "web_search",
		Decision: "allow",
		Reason: "safe tool",
		Timestamp: 1700000000,
	}

	if err := store.Record(entry); err != nil {
		t.Fatalf("Record: %v", err)
	}

	// 读取文件验证内容
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var got ApprovalEntry
	if err := json.Unmarshal(data, &got); err != nil {
		// 去掉换行符后解析
		t.Fatalf("Unmarshal: %v (data: %q)", err, data)
	}
	if got.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "sess-1")
	}
	if got.ToolName != "web_search" {
		t.Errorf("ToolName = %q, want %q", got.ToolName, "web_search")
	}
	if got.Decision != "allow" {
		t.Errorf("Decision = %q, want %q", got.Decision, "allow")
	}
}

// AS-002: GetDecision 返回缓存的决策。
func TestApprovalStore_GetDecisionCached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.jsonl")

	store, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	_ = store.Record(ApprovalEntry{
		SessionID: "sess-1",
		ToolName: "file_read",
		Decision: "deny",
		Timestamp: 1700000000,
	})

	decision, ok := store.GetDecision("sess-1", "file_read")
	if !ok {
		t.Fatal("GetDecision: expected cache hit, got miss")
	}
	if decision != "deny" {
		t.Errorf("decision = %q, want %q", decision, "deny")
	}

	// 未记录的工具应返回 miss
	_, ok = store.GetDecision("sess-1", "unknown_tool")
	if ok {
		t.Error("GetDecision unknown_tool: expected miss, got hit")
	}
}

// AS-003: 重启（新建 store 实例）加载之前的决策。
func TestApprovalStore_RestartLoadsPrevious(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.jsonl")

	// 第一个 store 实例
	store1, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("NewApprovalStore 1: %v", err)
	}
	_ = store1.Record(ApprovalEntry{
		SessionID: "sess-restart",
		ToolName: "shell_exec",
		Decision: "allow",
		Timestamp: 1700000000,
	})
	_ = store1.Record(ApprovalEntry{
		SessionID: "sess-restart",
		ToolName: "file_delete",
		Decision: "deny",
		Timestamp: 1700000001,
	})
	if err := store1.Close(); err != nil {
		t.Fatalf("Close store1: %v", err)
	}

	// 第二个 store 实例（模拟重启）
	store2, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("NewApprovalStore 2: %v", err)
	}
	defer func() { _ = store2.Close() }()

	// 验证之前记录的决策已加载
	decision, ok := store2.GetDecision("sess-restart", "shell_exec")
	if !ok {
		t.Fatal("GetDecision shell_exec: expected cache hit after restart")
	}
	if decision != "allow" {
		t.Errorf("shell_exec decision = %q, want %q", decision, "allow")
	}

	decision, ok = store2.GetDecision("sess-restart", "file_delete")
	if !ok {
		t.Fatal("GetDecision file_delete: expected cache hit after restart")
	}
	if decision != "deny" {
		t.Errorf("file_delete decision = %q, want %q", decision, "deny")
	}
}

// AS-004: 同一 session+tool 第二次调用返回缓存（不再询问）。
func TestApprovalStore_SameSessionToolNoReask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.jsonl")

	store, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 首次记录决策
	_ = store.Record(ApprovalEntry{
		SessionID: "sess-noreask",
		ToolName: "code_interpreter",
		Decision: "allow",
		Timestamp: 1700000000,
	})

	// 第一次 GetDecision — 命中缓存
	d1, ok1 := store.GetDecision("sess-noreask", "code_interpreter")
	if !ok1 {
		t.Fatal("first GetDecision: expected cache hit")
	}
	if d1 != "allow" {
		t.Errorf("first decision = %q, want %q", d1, "allow")
	}

	// 第二次 GetDecision — 仍命中缓存，值相同
	d2, ok2 := store.GetDecision("sess-noreask", "code_interpreter")
	if !ok2 {
		t.Fatal("second GetDecision: expected cache hit")
	}
	if d2 != "allow" {
		t.Errorf("second decision = %q, want %q", d2, "allow")
	}
	if d1 != d2 {
		t.Errorf("decisions differ: %q vs %q", d1, d2)
	}
}

// AS-005: 不同会话的决策互相独立。
func TestApprovalStore_DifferentSessionsIndependent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approvals.jsonl")

	store, err := NewApprovalStore(path)
	if err != nil {
		t.Fatalf("NewApprovalStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	// sess-A 允许 tool-X
	_ = store.Record(ApprovalEntry{
		SessionID: "sess-A",
		ToolName: "tool-X",
		Decision: "allow",
		Timestamp: 1700000000,
	})

	// sess-B 拒绝 tool-X
	_ = store.Record(ApprovalEntry{
		SessionID: "sess-B",
		ToolName: "tool-X",
		Decision: "deny",
		Timestamp: 1700000001,
	})

	// sess-A 的决策应是 allow
	dA, okA := store.GetDecision("sess-A", "tool-X")
	if !okA {
		t.Fatal("GetDecision sess-A: expected hit")
	}
	if dA != "allow" {
		t.Errorf("sess-A decision = %q, want %q", dA, "allow")
	}

	// sess-B 的决策应是 deny
	dB, okB := store.GetDecision("sess-B", "tool-X")
	if !okB {
		t.Fatal("GetDecision sess-B: expected hit")
	}
	if dB != "deny" {
		t.Errorf("sess-B decision = %q, want %q", dB, "deny")
	}

	// sess-C 没有记录，应返回 miss
	_, okC := store.GetDecision("sess-C", "tool-X")
	if okC {
		t.Error("GetDecision sess-C: expected miss, got hit")
	}
}
