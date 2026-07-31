// Package log 的 JSONL ExecLogger 默认实现（三轨）。
//
// 三轨文件布局（）：
//
//	<dataDir>/
//	 ├── sessions/<ts>_<uuid>.jsonl 会话树（可分支，compaction 检查点）
//	 ├── runs/<sessionID>.jsonl turn/item 级执行轨迹
//	 └── events/<sessionID>.jsonl 事件流原样
//
// 通用 Log(entry) 按 Category 自动分发：session→sessions, agent/tool→runs, 其余→events。
// 专用方法 LogTurn/LogItem→runs，LogEvent→events，LogSession→sessions。
//
// 写入失败不阻塞主流程：记入 crash 旁路日志，调用方仍可感知错误。
package log

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── 三轨文件布局常量 ────────────────────────────────────────────

const (
	trackSessions = "sessions"
	trackRuns = "runs"
	trackEvents = "events"
)

// ─── JSONLExecLogger ─────────────────────────────────────────────

// JSONLExecLogger 是默认的 ExecLogger 实现（三轨 + 轮转 + crash 旁路）。
type JSONLExecLogger struct {
	cfg LogConfig
	crashLog string
	closed bool

	mu sync.Mutex
	trackWriters map[string]*trackWriter // key: "sessions" | "runs" | "events"
	flushCancel context.CancelFunc
	flushWg sync.WaitGroup // 确保 interval goroutine 退出完成
}

// trackWriter 是单个轨道的写入器。
type trackWriter struct {
	dir string
	pattern string // 如 "exec_{{.Date}}.jsonl"
	current string
	fd *os.File
	writer *bufio.Writer
}

// NewJSONLExecLogger 构造一个 JSONL 三轨日志器。
//
// dataDir 是日志根目录；其下自动创建 sessions/runs/events 子目录。
// 传入 LogConfig 以指定文件名模式、缓冲策略、轮转、保留期等。
func NewJSONLExecLogger(dataDir string, cfg LogConfig) (*JSONLExecLogger, error) {
	cfg.DataDir = dataDir
	return NewJSONLExecLoggerWithConfig(cfg)
}

// NewWithLogConfig 是 NewJSONLExecLoggerWithConfig 的别名（向后兼容）。
// 传入 nil 时使用默认配置。
func NewWithLogConfig(cfg *LogConfig) (*JSONLExecLogger, error) {
	if cfg == nil {
		def := DefaultLogConfig()
		cfg = &def
	}
	return NewJSONLExecLoggerWithConfig(*cfg)
}

// NewJSONLExecLoggerWithConfig 用完整配置构造三轨日志器。
func NewJSONLExecLoggerWithConfig(cfg LogConfig) (*JSONLExecLogger, error) {
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("log: invalid config: %w", err)
	}

	l := &JSONLExecLogger{
		cfg: cfg,
		crashLog: filepath.Join(cfg.DataDir, cfg.CrashLogName),
		trackWriters: make(map[string]*trackWriter),
	}

	for _, track := range []string{trackSessions, trackRuns, trackEvents} {
		dir := filepath.Join(cfg.DataDir, track)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("log: mkdir %s: %w", dir, err)
		}
		l.trackWriters[track] = &trackWriter{
			dir: dir,
			pattern: cfg.FilePattern,
		}
	}

	// 启动 interval 刷盘定时器
	if cfg.FlushMode == "interval" && cfg.FlushIntervalMs > 0 {
		ctx, cancel := context.WithCancel(context.Background())
		l.flushCancel = cancel
		l.flushWg.Add(1)
		go l.intervalFlush(ctx)
	}

	// 清理过期文件
	if cfg.RetentionDays > 0 {
		l.cleanupOldFiles(cfg.RetentionDays)
	}

	return l, nil
}

// ─── 通用 Log（按 Category 分发） ────────────────────────────────

// Log 通用写入入口，按 Category 自动分发到对应轨道。
func (l *JSONLExecLogger) Log(ctx context.Context, entry *ExecLogEntry) error {
	if entry == nil {
		return nil
	}
	track := l.categoryToTrack(entry.Category)
	data, err := json.Marshal(entry)
	if err != nil {
		return l.crash(fmt.Errorf("log: marshal: %w", err))
	}
	return l.writeTrack(ctx, track, data)
}

// LogTurn 写入 runs 轨。
func (l *JSONLExecLogger) LogTurn(ctx context.Context, rec *TurnRecord) error {
	if rec == nil {
		return nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return l.crash(fmt.Errorf("log: marshal turn: %w", err))
	}
	return l.writeTrack(ctx, trackRuns, data)
}

// LogItem 写入 runs 轨。
func (l *JSONLExecLogger) LogItem(ctx context.Context, rec *ItemRecord) error {
	if rec == nil {
		return nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return l.crash(fmt.Errorf("log: marshal item: %w", err))
	}
	return l.writeTrack(ctx, trackRuns, data)
}

// LogEvent 写入 events 轨。
func (l *JSONLExecLogger) LogEvent(ctx context.Context, rec *EventRecord) error {
	if rec == nil {
		return nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return l.crash(fmt.Errorf("log: marshal event: %w", err))
	}
	return l.writeTrack(ctx, trackEvents, data)
}

// LogSession 写入 sessions 轨。
func (l *JSONLExecLogger) LogSession(ctx context.Context, rec *SessionRecord) error {
	if rec == nil {
		return nil
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return l.crash(fmt.Errorf("log: marshal session: %w", err))
	}
	return l.writeTrack(ctx, trackSessions, data)
}

// Flush 强制刷盘所有轨道。
func (l *JSONLExecLogger) Flush(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	for _, tw := range l.trackWriters {
		if tw.writer != nil {
			if err := tw.writer.Flush(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Close 关闭所有轨道（关闭前自动 Flush）。
func (l *JSONLExecLogger) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	if l.flushCancel != nil {
		l.flushCancel()
	}
	l.mu.Unlock()
	l.flushWg.Wait() // 等待 interval goroutine 退出
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	for _, tw := range l.trackWriters {
		if tw.writer != nil {
			if err := tw.writer.Flush(); err != nil && firstErr == nil {
				firstErr = err
			}
			tw.writer = nil
		}
		if tw.fd != nil {
			if err := tw.fd.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
			tw.fd = nil
		}
	}
	return firstErr
}

// ─── 内部方法 ────────────────────────────────────────────────────

// categoryToTrack 按日志类别映射到轨道。
func (l *JSONLExecLogger) categoryToTrack(c LogCategory) string {
	switch c {
	case LogCategorySession:
		return trackSessions
	case LogCategoryAgent, LogCategoryTool, LogCategoryLLM, LogCategoryCompact, LogCategoryHITL:
		return trackRuns
	default:
		return trackEvents
	}
}

// writeTrack 写入指定轨道（线程安全）。
func (l *JSONLExecLogger) writeTrack(ctx context.Context, track string, data []byte) error {
	data = append(data, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return fmt.Errorf("log: logger is closed")
	}
	tw, ok := l.trackWriters[track]
	if !ok {
		return fmt.Errorf("log: unknown track %q", track)
	}
	if err := tw.ensureWriter(l.cfg); err != nil {
		return l.crash(err)
	}
	if _, err := tw.writer.Write(data); err != nil {
		return l.crash(fmt.Errorf("log: write %s: %w", track, err))
	}
	// line 模式立即 flush
	if l.cfg.FlushMode == "line" {
		_ = tw.writer.Flush()
	}
	return nil
}

// rotatedFileName 生成轮转后的文件名（如 exec_2026-07-29.jsonl → exec_2026-07-29.1.jsonl）。
func (tw *trackWriter) rotatedFileName(cfg LogConfig) string {
	base := strings.TrimSuffix(tw.current, ".jsonl")
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s.%d.jsonl", base, i)
		if _, err := os.Stat(filepath.Join(tw.dir, name)); os.IsNotExist(err) {
			return name
		}
		if i > 100 {
			return name // 防止无限循环
		}
	}
}

// ensureWriter 确保 trackWriter 的 writer 指向正确日期的文件（调用方持锁）。
func (tw *trackWriter) ensureWriter(cfg LogConfig) error {
	dateStr := time.Now().UTC().Format("2006-01-02")
	name := strings.ReplaceAll(tw.pattern, "{{.Date}}", dateStr)
	if name == tw.current && tw.writer != nil {
		// 检查大小轮转
		if cfg.RotationBySizeEnabled && tw.fd != nil {
			if fi, err := tw.fd.Stat(); err == nil && fi.Size() >= cfg.MaxFileSize {
				// 关闭当前文件，生成轮转文件名
				_ = tw.writer.Flush()
				_ = tw.fd.Close()
				// 重命名当前文件为 .1, .2, ...
				oldPath := filepath.Join(tw.dir, tw.current)
				rotatedName := tw.rotatedFileName(cfg)
				_ = os.Rename(oldPath, filepath.Join(tw.dir, rotatedName))
				tw.fd = nil
				tw.writer = nil
				tw.current = ""
			}
		}
		if tw.writer != nil {
			return nil
		}
	}
	// 日期变更或首次：刷旧文件
	if tw.writer != nil {
		_ = tw.writer.Flush()
	}
	if tw.fd != nil {
		_ = tw.fd.Close()
	}
	path := filepath.Join(tw.dir, name)
	fd, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, cfg.OpenFilePerm)
	if err != nil {
		return fmt.Errorf("log: open %s: %w", path, err)
	}
	tw.fd = fd
	tw.writer = bufio.NewWriterSize(fd, cfg.BufferSize)
	tw.current = name
	return nil
}

// crash 将错误写入旁路日志并返回原错误。
func (l *JSONLExecLogger) crash(err error) error {
	if l.cfg.DisableCrashLog {
		return err
	}
	msg := fmt.Sprintf("%s %v\n", time.Now().UTC().Format(time.RFC3339Nano), err)
	// append 模式写入，避免并发 crash 互相覆盖
	f, ferr := os.OpenFile(l.crashLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if ferr == nil {
		_, _ = f.WriteString(msg)
		_ = f.Close()
	}
	return err
}

// intervalFlush 后台定时刷盘 goroutine。
func (l *JSONLExecLogger) intervalFlush(ctx context.Context) {
	defer l.flushWg.Done()
	ticker := time.NewTicker(time.Duration(l.cfg.FlushIntervalMs) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = l.Flush(ctx)
		}
	}
}

// cleanupOldFiles 清理 n 天前的过期日志文件。
func (l *JSONLExecLogger) cleanupOldFiles(days int) {
	cutoff := time.Now().AddDate(0, 0, -days)
	for _, track := range []string{trackSessions, trackRuns, trackEvents} {
		dir := filepath.Join(l.cfg.DataDir, track)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

// applyDefaults 用默认值填充零值字段。
func applyDefaults(cfg *LogConfig) {
	def := DefaultLogConfig()
	if cfg.DataDir == "" {
		cfg.DataDir = def.DataDir
	}
	if cfg.FilePattern == "" {
		cfg.FilePattern = def.FilePattern
	}
	if cfg.BufferSize <= 0 {
		cfg.BufferSize = def.BufferSize
	}
	if cfg.FlushMode == "" {
		cfg.FlushMode = def.FlushMode
	}
	if cfg.FlushIntervalMs <= 0 && cfg.FlushMode == "interval" {
		cfg.FlushIntervalMs = 1000
	}
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = def.MaxFileSize
	}
	if cfg.CrashLogName == "" {
		cfg.CrashLogName = def.CrashLogName
	}
	if cfg.MaxCrashLogSize <= 0 && !cfg.DisableCrashLog {
		cfg.MaxCrashLogSize = def.MaxCrashLogSize
	}
	if cfg.OpenFilePerm == 0 {
		cfg.OpenFilePerm = def.OpenFilePerm
	}
}
