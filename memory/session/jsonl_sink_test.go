// Package session 的 JSONLSessionSink 测试。
package session

import (
	"context"
	"testing"
)

// TestJSONLSessionSink_AppendAndLoad 验证追加条目并重建会话树。
func TestJSONLSessionSink_AppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewJSONLSessionSink(dir, "session_{{.Date}}.jsonl")
	if err != nil {
		t.Fatalf("NewJSONLSessionSink: %v", err)
	}
	defer func() { _ = sink.Close() }()

	entries := []SessionEntry{
		{EntryType: "session_start", SessionID: "s-1"},
		{EntryType: "message", SessionID: "s-1", ParentID: "", Data: "hello"},
		{EntryType: "message", SessionID: "s-1", ParentID: "", Data: "world"},
	}
	for _, e := range entries {
		if err := sink.Append(context.Background(), e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := sink.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	tree, err := sink.LoadTree(context.Background(), "s-1")
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if tree.SessionID != "s-1" {
		t.Errorf("expected session_id s-1, got %s", tree.SessionID)
	}
	if len(tree.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(tree.Entries))
	}
}

// TestJSONLSessionSink_LoadNonexistent 验证加载不存在的会话返回空树。
func TestJSONLSessionSink_LoadNonexistent(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewJSONLSessionSink(dir, "session_{{.Date}}.jsonl")
	if err != nil {
		t.Fatalf("NewJSONLSessionSink: %v", err)
	}
	defer func() { _ = sink.Close() }()

	tree, err := sink.LoadTree(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if len(tree.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(tree.Entries))
	}
}

// TestJSONLSessionSink_MultipleSessions 验证多会话条目只返回匹配的。
func TestJSONLSessionSink_MultipleSessions(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewJSONLSessionSink(dir, "session_{{.Date}}.jsonl")
	if err != nil {
		t.Fatalf("NewJSONLSessionSink: %v", err)
	}
	defer func() { _ = sink.Close() }()

	_ = sink.Append(context.Background(), SessionEntry{EntryType: "message", SessionID: "s-1", Data: "a"})
	_ = sink.Append(context.Background(), SessionEntry{EntryType: "message", SessionID: "s-2", Data: "b"})
	_ = sink.Append(context.Background(), SessionEntry{EntryType: "message", SessionID: "s-1", Data: "c"})
	_ = sink.Flush(context.Background())

	tree, err := sink.LoadTree(context.Background(), "s-1")
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if len(tree.Entries) != 2 {
		t.Errorf("expected 2 entries for s-1, got %d", len(tree.Entries))
	}
}

// TestJSONLSessionSink_BranchReconstruction 验证分支信息重建。
func TestJSONLSessionSink_BranchReconstruction(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewJSONLSessionSink(dir, "session_{{.Date}}.jsonl")
	if err != nil {
		t.Fatalf("NewJSONLSessionSink: %v", err)
	}
	defer func() { _ = sink.Close() }()

	_ = sink.Append(context.Background(), SessionEntry{
		EntryType: "branch",
		SessionID: "s-1",
		ParentID: "msg-1",
		Metadata: map[string]any{"branch_id": "br-1"},
	})
	_ = sink.Flush(context.Background())

	tree, err := sink.LoadTree(context.Background(), "s-1")
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if len(tree.Branches) != 1 {
		t.Fatalf("expected 1 branch, got %d", len(tree.Branches))
	}
	if tree.Branches[0].BranchID != "br-1" {
		t.Errorf("expected branch_id br-1, got %s", tree.Branches[0].BranchID)
	}
}
