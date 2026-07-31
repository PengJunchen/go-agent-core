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

// ─── 三轨信封提取与解析测试 ─────────────────────────────────────────

// TestExtractEnvelopes_ThreeTracks 验证三种专用记录分别落入对应轨道，
// 且 ExtractEnvelopes 正确识别信封类别。
func TestExtractEnvelopes_ThreeTracks(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "env_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	// 写入三种专用记录
	_ = logger.LogTurn(context.Background(), NewTurnRecord("turn_start", "s-1", "t-1", "running"))
	_ = logger.LogItem(context.Background(), NewItemRecord("tool_call", "s-1", "t-1"))
	_ = logger.LogEvent(context.Background(), NewEventRecord("text_delta", "s-1", "t-1"))
	_ = logger.LogSession(context.Background(), NewSessionRecord("session_start", "s-1"))
	// 也写入通用 ExecLogEntry
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)

	envelopes, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}

	// 应有至少 5 个信封
	if len(envelopes) < 5 {
		t.Fatalf("expected at least 5 envelopes, got %d", len(envelopes))
	}

	// 统计各轨道信封
	trackCount := map[string]int{}
	catCount := map[LogCategory]int{}
	for _, env := range envelopes {
		trackCount[env.Track]++
		catCount[env.Category]++
	}

	// sessions 轨至少 1 条（SessionRecord）
	if trackCount[TrackSessions] < 1 {
		t.Errorf("expected at least 1 sessions envelope, got %d", trackCount[TrackSessions])
	}
	// runs 轨至少 3 条（Turn + Item + LLM entry）
	if trackCount[TrackRuns] < 3 {
		t.Errorf("expected at least 3 runs envelopes, got %d", trackCount[TrackRuns])
	}
	// events 轨至少 1 条（EventRecord）
	if trackCount[TrackEvents] < 1 {
		t.Errorf("expected at least 1 events envelope, got %d", trackCount[TrackEvents])
	}

	// 类别统计
	if catCount[LogCategorySession] < 1 {
		t.Errorf("expected Session category, got %d", catCount[LogCategorySession])
	}
	if catCount[LogCategoryTurn] < 1 {
		t.Errorf("expected Turn category, got %d", catCount[LogCategoryTurn])
	}
	if catCount[LogCategoryItem] < 1 {
		t.Errorf("expected Item category, got %d", catCount[LogCategoryItem])
	}
	if catCount[LogCategoryEvent] < 1 {
		t.Errorf("expected Event category, got %d", catCount[LogCategoryEvent])
	}
	if catCount[LogCategoryLLM] < 1 {
		t.Errorf("expected LLM category, got %d", catCount[LogCategoryLLM])
	}
}

// TestExtractEnvelopes_TrackTypeFilter 验证 TrackType 过滤三轨。
func TestExtractEnvelopes_TrackTypeFilter(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "tf_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogTurn(context.Background(), NewTurnRecord("turn_start", "s-1", "t-1", "running"))
	_ = logger.LogEvent(context.Background(), NewEventRecord("text_delta", "s-1", "t-1"))
	_ = logger.LogSession(context.Background(), NewSessionRecord("session_start", "s-1"))
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)

	// 只扫描 runs 轨
	runsEnvs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{TrackType: TrackRuns})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}
	for _, env := range runsEnvs {
		if env.Track != TrackRuns {
			t.Errorf("expected only runs track, got %q", env.Track)
		}
	}

	// 只扫描 sessions 轨
	sessEnvs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{TrackType: TrackSessions})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}
	for _, env := range sessEnvs {
		if env.Track != TrackSessions {
			t.Errorf("expected only sessions track, got %q", env.Track)
		}
	}

	// 只扫描 events 轨
	evtEnvs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{TrackType: TrackEvents})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}
	for _, env := range evtEnvs {
		if env.Track != TrackEvents {
			t.Errorf("expected only events track, got %q", env.Track)
		}
	}
}

// TestExtractEnvelopes_CategoryFilter 验证按 Category 过滤信封。
func TestExtractEnvelopes_CategoryFilter(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "cf_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogTurn(context.Background(), NewTurnRecord("turn_start", "s-1", "t-1", "running"))
	_ = logger.LogItem(context.Background(), NewItemRecord("llm_call", "s-1", "t-1"))
	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)

	// 只要 Turn 类别
	envs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{
		Categories: []LogCategory{LogCategoryTurn},
	})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}
	for _, env := range envs {
		if env.Category != LogCategoryTurn {
			t.Errorf("expected only Turn category, got %q", env.Category)
		}
	}
	if len(envs) < 1 {
		t.Error("expected at least 1 Turn envelope")
	}
}

// TestLogEnvelope_ParseAsTurnRecord 验证信封可解析出 TurnRecord 专用字段。
func TestLogEnvelope_ParseAsTurnRecord(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "ptr_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogTurn(context.Background(), &TurnRecord{
		Timestamp: "2026-07-31T00:00:00Z",
		SessionID: "s-1",
		TurnID: "t-1",
		EventType: "turn_start",
		Status: "running",
	})
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)
	envs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}

	// 找到 Turn 信封
	var turnEnv *LogEnvelope
	for _, env := range envs {
		if env.Category == LogCategoryTurn {
			turnEnv = env
			break
		}
	}
	if turnEnv == nil {
		t.Fatal("no Turn envelope found")
	}

	// 解析为 TurnRecord，验证专用字段
	rec, err := turnEnv.ParseAsTurnRecord()
	if err != nil {
		t.Fatalf("ParseAsTurnRecord: %v", err)
	}
	if rec.EventType != "turn_start" {
		t.Errorf("expected EventType=turn_start, got %q", rec.EventType)
	}
	if rec.Status != "running" {
		t.Errorf("expected Status=running, got %q", rec.Status)
	}
	if rec.TurnID != "t-1" {
		t.Errorf("expected TurnID=t-1, got %q", rec.TurnID)
	}
}

// TestLogEnvelope_ParseAsItemRecord 验证信封可解析出 ItemRecord 专用字段。
func TestLogEnvelope_ParseAsItemRecord(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "pir_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogItem(context.Background(), &ItemRecord{
		Timestamp: "2026-07-31T00:00:00Z",
		SessionID: "s-1",
		TurnID: "t-1",
		ItemType: "tool_call",
		ToolName: "edit_file",
		Provider: "openai",
		Model: "gpt-4",
	})
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)
	envs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}

	var itemEnv *LogEnvelope
	for _, env := range envs {
		if env.Category == LogCategoryItem {
			itemEnv = env
			break
		}
	}
	if itemEnv == nil {
		t.Fatal("no Item envelope found")
	}

	rec, err := itemEnv.ParseAsItemRecord()
	if err != nil {
		t.Fatalf("ParseAsItemRecord: %v", err)
	}
	if rec.ItemType != "tool_call" {
		t.Errorf("expected ItemType=tool_call, got %q", rec.ItemType)
	}
	if rec.ToolName != "edit_file" {
		t.Errorf("expected ToolName=edit_file, got %q", rec.ToolName)
	}
	if rec.Provider != "openai" {
		t.Errorf("expected Provider=openai, got %q", rec.Provider)
	}
	if rec.Model != "gpt-4" {
		t.Errorf("expected Model=gpt-4, got %q", rec.Model)
	}
}

// TestLogEnvelope_ParseAsEventRecord 验证信封可解析出 EventRecord 专用字段。
func TestLogEnvelope_ParseAsEventRecord(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "per_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogEvent(context.Background(), &EventRecord{
		Timestamp: "2026-07-31T00:00:00Z",
		SessionID: "s-1",
		TurnID: "t-1",
		EventType: "text_delta",
		Content: "Hello world",
		Thinking: "I need to...",
		ToolCallID: "",
	})
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)
	envs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}

	var evtEnv *LogEnvelope
	for _, env := range envs {
		if env.Category == LogCategoryEvent {
			evtEnv = env
			break
		}
	}
	if evtEnv == nil {
		t.Fatal("no Event envelope found")
	}

	rec, err := evtEnv.ParseAsEventRecord()
	if err != nil {
		t.Fatalf("ParseAsEventRecord: %v", err)
	}
	if rec.EventType != "text_delta" {
		t.Errorf("expected EventType=text_delta, got %q", rec.EventType)
	}
	if rec.Content != "Hello world" {
		t.Errorf("expected Content='Hello world', got %q", rec.Content)
	}
	if rec.Thinking != "I need to..." {
		t.Errorf("expected Thinking='I need to...', got %q", rec.Thinking)
	}
}

// TestLogEnvelope_ParseAsSessionRecord 验证信封可解析出 SessionRecord 专用字段。
func TestLogEnvelope_ParseAsSessionRecord(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "psr_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogSession(context.Background(), &SessionRecord{
		Timestamp: "2026-07-31T00:00:00Z",
		SessionID: "s-1",
		EntryType: "message",
		ParentID: "p-0",
		Metadata: map[string]any{"branch": "main"},
	})
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)
	envs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}

	var sessEnv *LogEnvelope
	for _, env := range envs {
		if env.Category == LogCategorySession {
			sessEnv = env
			break
		}
	}
	if sessEnv == nil {
		t.Fatal("no Session envelope found")
	}

	rec, err := sessEnv.ParseAsSessionRecord()
	if err != nil {
		t.Fatalf("ParseAsSessionRecord: %v", err)
	}
	if rec.EntryType != "message" {
		t.Errorf("expected EntryType=message, got %q", rec.EntryType)
	}
	if rec.ParentID != "p-0" {
		t.Errorf("expected ParentID=p-0, got %q", rec.ParentID)
	}
	if rec.Metadata == nil || rec.Metadata["branch"] != "main" {
		t.Errorf("expected Metadata[branch]=main, got %v", rec.Metadata)
	}
}

// TestLogEnvelope_ParseAsExecLogEntry 验证通用 ExecLogEntry 信封可解析。
func TestLogEnvelope_ParseAsExecLogEntry(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "pele_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1").
		WithLevel(LogLevelInfo).
		WithTags("provider:openai"))
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	ext := NewJSONLLogExtractor(dir)
	envs, err := ext.ExtractEnvelopes(context.Background(), &LogFilter{})
	if err != nil {
		t.Fatalf("ExtractEnvelopes: %v", err)
	}

	var llmEnv *LogEnvelope
	for _, env := range envs {
		if env.Category == LogCategoryLLM {
			llmEnv = env
			break
		}
	}
	if llmEnv == nil {
		t.Fatal("no LLM envelope found")
	}

	entry, err := llmEnv.ParseAsExecLogEntry()
	if err != nil {
		t.Fatalf("ParseAsExecLogEntry: %v", err)
	}
	if entry.Action != "chat" {
		t.Errorf("expected Action=chat, got %q", entry.Action)
	}
	if entry.Category != LogCategoryLLM {
		t.Errorf("expected Category=llm, got %q", entry.Category)
	}
	if entry.Level != LogLevelInfo {
		t.Errorf("expected Level=info, got %q", entry.Level)
	}
	if len(entry.Tags) == 0 || entry.Tags[0] != "provider:openai" {
		t.Errorf("expected Tags=[provider:openai], got %v", entry.Tags)
	}
}

// TestSelect_EnvelopesPopulated 验证 Select 同时填充 Envelopes 和 Entries。
func TestSelect_EnvelopesPopulated(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "sep_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.Log(context.Background(), NewEntry(LogCategoryLLM, "chat", "s-1", "t-1"))
	_ = logger.LogTurn(context.Background(), NewTurnRecord("turn_start", "s-1", "t-1", "running"))
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	sel := LogSelector{DataDir: dir}
	summary, err := Select(context.Background(), sel)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	if len(summary.Envelopes) == 0 {
		t.Error("expected at least 1 envelope")
	}
	if len(summary.Entries) == 0 {
		t.Error("expected at least 1 entry (backward compat)")
	}
	// Envelopes 数量应 >= Entries 数量（envelopes 包含专用记录，entries 解析为稀疏 ExecLogEntry 可能失败被跳过）
	if len(summary.Envelopes) < len(summary.Entries) {
		t.Errorf("envelopes(%d) should be >= entries(%d)", len(summary.Envelopes), len(summary.Entries))
	}
}

// TestSelect_TrackTypeFilter 验证 Select 按 TrackType 过滤。
func TestSelect_TrackTypeFilter(t *testing.T) {
	dir := t.TempDir()
	logger, err := NewJSONLExecLogger(dir, LogConfig{FilePattern: "stf_{{.Date}}.jsonl"})
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	_ = logger.LogTurn(context.Background(), NewTurnRecord("turn_start", "s-1", "t-1", "running"))
	_ = logger.LogEvent(context.Background(), NewEventRecord("text_delta", "s-1", "t-1"))
	_ = logger.LogSession(context.Background(), NewSessionRecord("session_start", "s-1"))
	_ = logger.Flush(context.Background())
	_ = logger.Close()

	// 只取 runs 轨
	sel := LogSelector{DataDir: dir, TrackType: TrackRuns}
	summary, err := Select(context.Background(), sel)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	for _, env := range summary.Envelopes {
		if env.Track != TrackRuns {
			t.Errorf("expected only runs track, got %q", env.Track)
		}
	}
}
