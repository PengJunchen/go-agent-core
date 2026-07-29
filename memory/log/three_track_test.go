// Package log 的三轨日志测试。
package log

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestThreeTrack_DispatchByCategory 验证通用 Log 按 Category 分发到正确轨道。
func TestThreeTrack_DispatchByCategory(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "test_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()

	entries := []*ExecLogEntry{
		NewEntry(LogCategorySession, "create", "s-1", ""),
		NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"),
		NewEntry(LogCategoryTool, "exec", "s-1", "t-1"),
		NewEntry(LogCategorySystem, "boot", "", ""),
	}
	for _, e := range entries {
		if err := logger.Log(context.Background(), e); err != nil {
			t.Fatalf("Log: %v", err)
		}
	}
	if err := logger.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// session → sessions/
	if files := globLogs(dir, "test_*.jsonl"); len(files) == 0 {
		t.Fatal("expected log files")
	}
	// 验证三轨子目录都创建了
	for _, sub := range []string{"sessions", "runs", "events"} {
		if fi, err := os.Stat(filepath.Join(dir, sub)); err != nil || !fi.IsDir() {
			t.Errorf("expected directory %s/ to exist", sub)
		}
	}
}

// TestThreeTrack_SpecializedMethods 验证 LogTurn/LogItem/LogEvent/LogSession 写入对应轨道。
func TestThreeTrack_SpecializedMethods(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "sp_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()

	if err := logger.LogTurn(context.Background(), NewTurnRecord("turn_start", "s-1", "t-1", "running")); err != nil {
		t.Fatalf("LogTurn: %v", err)
	}
	if err := logger.LogItem(context.Background(), NewItemRecord("llm_call", "s-1", "t-1")); err != nil {
		t.Fatalf("LogItem: %v", err)
	}
	if err := logger.LogEvent(context.Background(), NewEventRecord("text_delta", "s-1", "t-1")); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}
	if err := logger.LogSession(context.Background(), NewSessionRecord("session_start", "s-1")); err != nil {
		t.Fatalf("LogSession: %v", err)
	}
	if err := logger.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// runs 轨应有 2 条（turn + item）
	runsFiles := globLogs(filepath.Join(dir, "runs"), "sp_*.jsonl")
	if len(runsFiles) == 0 {
		t.Fatal("expected runs track file")
	}
	// events 轨应有 1 条
	eventsFiles := globLogs(filepath.Join(dir, "events"), "sp_*.jsonl")
	if len(eventsFiles) == 0 {
		t.Fatal("expected events track file")
	}
	// sessions 轨应有 1 条
	sessFiles := globLogs(filepath.Join(dir, "sessions"), "sp_*.jsonl")
	if len(sessFiles) == 0 {
		t.Fatal("expected sessions track file")
	}
}

// TestThreeTrack_CloseBlocksLog 验证 Close 后 Log 返回错误。
func TestThreeTrack_CloseBlocksLog(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "c_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}
	_ = logger.Close()
	err = logger.Log(context.Background(), NewEntry(LogCategorySystem, "x", "", ""))
	if err == nil {
		t.Error("expected error when logging after close")
	}
}

// TestLogSelector_BasicFilter 验证 LogSelector 按类型/级别过滤。
func TestLogSelector_BasicFilter(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "sel_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}
	defer func() { _ = logger.Close() }()

	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategorySystem, "boot", "", ""))
	_ = logger.Flush(context.Background())

	sel := LogSelector{DataDir: dir, Types: []string{"item"}}
	summary, err := Select(context.Background(), sel)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if summary.TotalScanned == 0 {
		t.Error("expected at least 1 entry for type=item (LLM)")
	}
}
