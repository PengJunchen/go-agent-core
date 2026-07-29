package log

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// VQ-001: ExecLogger 写入的条目可被正确序列化且包含必需字段。
func TestJSONLExecLogger_WriteRead(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, "session_{{.Date}}.jsonl")
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	entry := NewEntry(LogCategoryLLM, "stream_chat", "s-1", "t-1").
		WithDuration(1823 * time.Millisecond).
		WithPayload(map[string]any{"provider": "openai", "model": "gpt-4o", "input_tokens": 1520})

	if err := logger.Log(context.Background(), entry); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := logger.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 验证文件存在且可读取
	matches, _ := filepath.Glob(filepath.Join(dir, "session_*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("log file is empty")
	}
	// 验证 JSON 包含必需字段
	jsonStr := string(data)
	for _, field := range []string{`"ts"`, `"session_id":"s-1"`, `"turn_id":"t-1"`, `"category":"llm"`, `"action":"stream_chat"`, `"duration_ms":1823`} {
		if !contains(jsonStr, field) {
			t.Errorf("log entry missing field %q", field)
		}
	}
}

// VS-001: ExecLogger 是强制依赖（不可关闭）—— Close 后不应 panic 但应安全。
func TestJSONLExecLogger_CloseSafe(t *testing.T) {
	dir := t.TempDir()
	logger, _ := NewJSONLExecLogger(dir, "session_{{.Date}}.jsonl")
	if err := logger.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
	// 双重 Close 安全
	if err := logger.Close(); err != nil {
		t.Errorf("double Close returned error: %v", err)
	}
}

// VT-001: 日志按日期轮转。
func TestJSONLExecLogger_Rotation(t *testing.T) {
	dir := t.TempDir()
	logger, _ := NewJSONLExecLogger(dir, "session_{{.Date}}.jsonl")
	t.Cleanup(func() { _ = logger.Close() })

	entry := NewEntry(LogCategorySystem, "start", "", "")
	_ = logger.Log(context.Background(), entry)
	_ = logger.Flush(context.Background())

	matches, _ := filepath.Glob(filepath.Join(dir, "session_*.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 file after single write, got %d", len(matches))
	}
}

// VC-001: LogExtractor 可从日志文件提取条目。
func TestJSONLLogExtractor_Extract(t *testing.T) {
	dir := t.TempDir()
	logger, _ := NewJSONLExecLogger(dir, "session_{{.Date}}.jsonl")
	t.Cleanup(func() { _ = logger.Close() })

	// 写入不同类别
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "stream_chat", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "execute", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "execute", "s-2", "t-1"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(dir)

	// 提取全部
	all, err := ext.Extract(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("Extract all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 entries, got %d", len(all))
	}

	// 按 session 提取
	bySession, _ := ext.Extract(context.Background(), &LogFilter{SessionID: "s-2"})
	if len(bySession) != 1 {
		t.Errorf("expected 1 entry for s-2, got %d", len(bySession))
	}

	// 按类别提取
	byCat, _ := ext.Extract(context.Background(), &LogFilter{Categories: []LogCategory{LogCategoryTool}})
	if len(byCat) != 2 {
		t.Errorf("expected 2 tool entries, got %d", len(byCat))
	}
}

// VH-001: LogFilter.Matches 正确过滤。
func TestLogFilter_Matches(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now().Add(time.Hour)

	tests := []struct {
		name string
		filter *LogFilter
		entry *ExecLogEntry
		want bool
	}{
		{"empty filter matches all", &LogFilter{}, &ExecLogEntry{Category: LogCategoryLLM}, true},
		{"category match", &LogFilter{Categories: []LogCategory{LogCategoryLLM}}, &ExecLogEntry{Category: LogCategoryLLM}, true},
		{"category mismatch", &LogFilter{Categories: []LogCategory{LogCategoryTool}}, &ExecLogEntry{Category: LogCategoryLLM}, false},
		{"session match", &LogFilter{SessionID: "s-1"}, &ExecLogEntry{SessionID: "s-1"}, true},
		{"session mismatch", &LogFilter{SessionID: "s-1"}, &ExecLogEntry{SessionID: "s-2"}, false},
		{"action match", &LogFilter{Actions: []string{"execute"}}, &ExecLogEntry{Action: "execute"}, true},
		{"hasError true match", &LogFilter{HasError: boolPtr(true)}, &ExecLogEntry{Error: "fail"}, true},
		{"hasError true mismatch", &LogFilter{HasError: boolPtr(true)}, &ExecLogEntry{Error: ""}, false},
		{"hasError false match", &LogFilter{HasError: boolPtr(false)}, &ExecLogEntry{Error: ""}, true},
		{"time range match", &LogFilter{StartTime: &start, EndTime: &end}, &ExecLogEntry{Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}, true},
		{"nil entry", &LogFilter{}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.Matches(tt.entry); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
