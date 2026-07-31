package log

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// -------------------------------------------------------------------------
// VC 规则：LogExtractor 提取与过滤（扩展测试集）
// -------------------------------------------------------------------------

// VC002: ExtractToFile 输出正确的 JSONL 格式。
func TestVC002_ExtractToFile(t *testing.T) {
	logger := newTestLogger(t, "extfile_{{.Date}}.jsonl")

	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "exec", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "stream", "s-2", "t-2"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	outPath := filepath.Join(t.TempDir(), "extracted.jsonl")
	if err := ext.ExtractToFile(context.Background(),
		&LogFilter{Categories: []LogCategory{LogCategoryLLM}}, outPath); err != nil {
		t.Fatalf("ExtractToFile: %v", err)
	}

	// 读取输出文件并验证
	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open output: %v", err)
	}
	defer func() { _ = f.Close() }() // 测试清理：读取后关闭，错误无需处理

	var count int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 LLM entries in output, got %d", count)
	}
}

// VC003: ExtractToFile 输出全部条目（无过滤）。
func TestVC003_ExtractToFileAll(t *testing.T) {
	logger := newTestLogger(t, "extall_{{.Date}}.jsonl")

	for i := 0; i < 5; i++ {
		_ = logger.Log(context.Background(), NewEntry(LogCategorySystem, "ping", "s-1", "t-1"))
	}
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	outPath := filepath.Join(t.TempDir(), "all.jsonl")
	if err := ext.ExtractToFile(context.Background(), nil, outPath); err != nil {
		t.Fatalf("ExtractToFile: %v", err)
	}

	// 验证 5 条
	entries, err := ext.Extract(context.Background(), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("expected 5 entries, got %d", len(entries))
	}
}

// VC004: 提取结果为空时输出空文件。
func TestVC004_ExtractEmptyResult(t *testing.T) {
	logger := newTestLogger(t, "empty_{{.Date}}.jsonl")
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	outPath := filepath.Join(t.TempDir(), "empty_out.jsonl")
	// 过滤一个不存在的 action
	if err := ext.ExtractToFile(context.Background(),
		&LogFilter{Actions: []string{"nonexistent"}}, outPath); err != nil {
		t.Fatalf("ExtractToFile: %v", err)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("expected empty output file, got size %d", info.Size())
	}
}

// VC005: 按动作（Actions）过滤。
func TestVC005_FilterByAction(t *testing.T) {
	logger := newTestLogger(t, "actionf_{{.Date}}.jsonl")

	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "execute", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "cancel", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	matched, err := ext.Extract(context.Background(), &LogFilter{Actions: []string{"execute", "cancel"}})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(matched) != 2 {
		t.Errorf("expected 2 entries for execute/cancel, got %d", len(matched))
	}
}

// VC006: 按时间范围过滤。
func TestVC006_FilterByTimeRange(t *testing.T) {
	// 使用时间戳已知的条目，避免时间条件不确定性
	now := time.Now().UTC()
	later := now.Add(10 * time.Minute)

	filter := &LogFilter{
		StartTime: &now,
		EndTime: &later,
	}

	entryBefore := &ExecLogEntry{Timestamp: now.Add(-time.Hour).Format(time.RFC3339Nano), Category: LogCategorySystem, Action: "before", SessionID: "s-1"}
	entryIn := &ExecLogEntry{Timestamp: now.Add(5 * time.Minute).Format(time.RFC3339Nano), Category: LogCategorySystem, Action: "in_range", SessionID: "s-1"}
	entryAfter := &ExecLogEntry{Timestamp: later.Add(time.Hour).Format(time.RFC3339Nano), Category: LogCategorySystem, Action: "after", SessionID: "s-1"}

	if !filter.Matches(entryIn) {
		t.Error("entryIn should match time range")
	}
	if filter.Matches(entryBefore) {
		t.Error("entryBefore should not match time range")
	}
	if filter.Matches(entryAfter) {
		t.Error("entryAfter should not match time range")
	}
}

// VC007: Limit 限制提取条目数。
func TestVC007_ExtractLimit(t *testing.T) {
	logger := newTestLogger(t, "limitf_{{.Date}}.jsonl")

	for i := 0; i < 10; i++ {
		_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	}
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	limited, err := ext.Extract(context.Background(), &LogFilter{Limit: 3})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(limited) != 3 {
		t.Errorf("expected 3 entries with limit=3, got %d", len(limited))
	}
}

// VC008: 提取时跳过损坏的日志行。
func TestVC008_SkipCorruptedLines(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test_corrupt.jsonl")

	// 写入混合内容：正常行、损坏行、空行
	content := []byte(
		`{"ts":"2026-01-01T00:00:00Z","session_id":"s-1","turn_id":"t-1","category":"llm","action":"chat","duration_ms":100}
not-json-line
{"ts":"2026-01-01T00:00:01Z","session_id":"s-1","turn_id":"t-1","category":"tool","action":"exec","duration_ms":200}

`)
	if err := os.WriteFile(testFile, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ext := NewJSONLLogExtractor(dir)
	entries, err := ext.Extract(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 valid entries (skipping corrupted lines), got %d", len(entries))
	}
}

// VC009: 多个过滤条件联合使用。
func TestVC009_CombinedFilters(t *testing.T) {
	logger := newTestLogger(t, "combf_{{.Date}}.jsonl")

	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "execute", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "execute", "s-2", "t-2"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	// 同时按 session 和 category 过滤
	matched, err := ext.Extract(context.Background(), &LogFilter{
		SessionID: "s-1",
		Categories: []LogCategory{LogCategoryTool},
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(matched) != 1 {
		t.Errorf("expected 1 entry (session=s-1 & category=tool), got %d", len(matched))
	}
}

// VC010: 并发提取安全。
func TestVC010_ConcurrentExtract(t *testing.T) {
	logger := newTestLogger(t, "concext_{{.Date}}.jsonl")

	for i := 0; i < 20; i++ {
		_ = logger.Log(context.Background(), NewEntry(LogCategorySystem, "ping", "s-1", "t-1"))
	}
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	n := 5
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := ext.Extract(context.Background(), &LogFilter{})
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent extract error: %v", err)
		}
	}
}

// VC011: NewJSONLLogExtractorWithConfig 使用自定义配置。
func TestVC011_ExtractorWithConfig(t *testing.T) {
	logger := newTestLogger(t, "cfgext_{{.Date}}.jsonl")
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractorWithConfig(logDir(t, logger), ExtractConfig{
		MaxScanBufferSize: 128 * 1024,
		MaxLineSize: 512 * 1024,
	})

	entries, err := ext.Extract(context.Background(), nil)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

// VC012: ExtractToFile 创建嵌套父目录。
func TestVC012_ExtractToFileNestedDir(t *testing.T) {
	logger := newTestLogger(t, "nestedf_{{.Date}}.jsonl")
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	// 嵌套目录
	outPath := filepath.Join(t.TempDir(), "sub1", "sub2", "nested_out.jsonl")
	if err := ext.ExtractToFile(context.Background(), nil, outPath); err != nil {
		t.Fatalf("ExtractToFile to nested dir: %v", err)
	}

	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Fatal("output file should exist in nested directory")
	}
}

// VC013: ExtractToFile 输出可被再次读取为 JSONL。
func TestVC013_ExtractToFileRoundTrip(t *testing.T) {
	logger := newTestLogger(t, "rtrip_{{.Date}}.jsonl")

	entry := NewEntry(LogCategoryLLM, "roundtrip", "s-1", "t-1").
		WithPayload(map[string]any{"key": "value"})
	_ = logger.Log(context.Background(), entry)
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	outPath := filepath.Join(t.TempDir(), "roundtrip.jsonl")
	if err := ext.ExtractToFile(context.Background(), nil, outPath); err != nil {
		t.Fatalf("ExtractToFile: %v", err)
	}

	// 用新提取器读取输出文件
	ext2 := NewJSONLLogExtractor(filepath.Dir(outPath))
	// 因为输出文件在另一个目录，直接指定文件名
	rerun, err := ext2.Extract(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("Re-extract: %v", err)
	}
	if len(rerun) != 1 {
		t.Fatalf("expected 1 re-extracted entry, got %d", len(rerun))
	}
	if rerun[0].Action != "roundtrip" {
		t.Errorf("expected action=roundtrip, got %s", rerun[0].Action)
	}
}

// VC014: 按 HasError 过滤提取。
func TestVC014_FilterByHasError(t *testing.T) {
	logger := newTestLogger(t, "errf_{{.Date}}.jsonl")

	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "execute", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "fail", "s-1", "t-1").WithError(os.ErrPermission))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryTool, "timeout", "s-1", "t-1").WithError(os.ErrDeadlineExceeded))
	_ = logger.Flush(context.Background())

	ext := NewJSONLLogExtractor(logDir(t, logger))

	hasErr := true
	errorEntries, err := ext.Extract(context.Background(), &LogFilter{HasError: &hasErr})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(errorEntries) != 2 {
		t.Errorf("expected 2 error entries, got %d", len(errorEntries))
	}

	noErr := false
	okEntries, err := ext.Extract(context.Background(), &LogFilter{HasError: &noErr})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(okEntries) != 1 {
		t.Errorf("expected 1 non-error entry, got %d", len(okEntries))
	}
}
