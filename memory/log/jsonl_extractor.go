// Package log 的 JSONL LogExtractor 默认实现。
//
// JSONLLogExtractor 扫描日志目录下所有 JSONL 文件，按 LogFilter 过滤
// 条目。支持按类别、会话、时间、动作、是否出错过滤，可输出到内存或文件。
// 扩展能力通过 ExtractOption 函数式选项提供。
package log

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ExtractOption 函数式选项，扩展 Extract 行为。
type ExtractOption func(*extractConfig)

// extractConfig 提取扩展配置。
type extractConfig struct {
	sortByTime bool
	dedup bool
	maxFileSize int64
}

// WithSortByTime 提取结果按时间升序排序。
// 默认 false（保持文件自然顺序）。
func WithSortByTime() ExtractOption {
	return func(c *extractConfig) {
		c.sortByTime = true
	}
}

// WithDedup 跨文件聚合时去重（基于 timestamp+action+category 的 hash）。
// 默认 false（保留所有条目）。
func WithDedup() ExtractOption {
	return func(c *extractConfig) {
		c.dedup = true
	}
}

// WithMaxStreamSize 设置大文件流式读取的 scanner buffer 大小（字节）。
// 默认 64KB。较大的值可处理更长的 JSON 行。
func WithMaxStreamSize(size int64) ExtractOption {
	return func(c *extractConfig) {
		c.maxFileSize = size
	}
}

// JSONLLogExtractor 是默认的 LogExtractor 实现。
type JSONLLogExtractor struct {
	dataDir string
	cfg ExtractConfig
}

// NewJSONLLogExtractor 构造一个提取器（使用默认配置）。
func NewJSONLLogExtractor(dataDir string) *JSONLLogExtractor {
	return &JSONLLogExtractor{
		dataDir: dataDir,
		cfg: DefaultExtractConfig(),
	}
}

// NewJSONLLogExtractorWithConfig 构造一个带自定义配置的提取器。
func NewJSONLLogExtractorWithConfig(dataDir string, cfg ExtractConfig) *JSONLLogExtractor {
	if cfg.MaxScanBufferSize <= 0 {
		cfg.MaxScanBufferSize = DefaultExtractConfig().MaxScanBufferSize
	}
	if cfg.MaxLineSize <= 0 {
		cfg.MaxLineSize = DefaultExtractConfig().MaxLineSize
	}
	return &JSONLLogExtractor{
		dataDir: dataDir,
		cfg: cfg,
	}
}

// Extract 提取符合条件的条目到内存（向后兼容的接口实现）。
// 增强功能请使用 ExtractWithOpts。
func (e *JSONLLogExtractor) Extract(ctx context.Context, filter *LogFilter) ([]*ExecLogEntry, error) {
	return e.ExtractWithOpts(ctx, filter)
}

// ExtractEnvelopes 提取三轨信封，Payload 延迟反序列化，专用字段可解析。
// 按 TrackType 限定扫描轨道，按 Categories/SessionID/时间/HasError 等过滤，
// 返回 []*LogEnvelope，消费者按 Track+Category 选择 ParseAsXxx 解析 Payload。
func (e *JSONLLogExtractor) ExtractEnvelopes(ctx context.Context, filter *LogFilter) ([]*LogEnvelope, error) {
	if filter == nil {
		filter = &LogFilter{}
	}

	files, err := e.listJSONLFilesFilteredByTrack(filter.StartTime, filter.EndTime, filter.TrackType)
	if err != nil {
		return nil, err
	}

	var results []*LogEnvelope

	for _, f := range files {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		envelopes, err := e.scanFileEnvelopes(ctx, f, filter)
		if err != nil {
			return nil, err
		}
		for _, env := range envelopes {
			results = append(results, env)
			if filter.Limit > 0 && len(results) >= filter.Limit {
				results = results[:filter.Limit]
				break
			}
		}
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}

	return results, nil
}

// ExtractWithOpts 提取符合条件的条目到内存，支持函数式选项。
//
// 支持的选项：WithSortByTime(), WithDedup(), WithMaxStreamSize().
func (e *JSONLLogExtractor) ExtractWithOpts(ctx context.Context, filter *LogFilter, opts ...ExtractOption) ([]*ExecLogEntry, error) {
	if filter == nil {
		filter = &LogFilter{}
	}

	// 解析选项
	var ec extractConfig
	for _, opt := range opts {
		opt(&ec)
	}
	if ec.maxFileSize <= 0 {
		ec.maxFileSize = 64 * 1024
	}

	// 按时间范围过滤文件列表
	files, err := e.listJSONLFilesFiltered(filter.StartTime, filter.EndTime)
	if err != nil {
		return nil, err
	}

	seen := make(map[[32]byte]bool) // 去重集合
	var results []*ExecLogEntry

	for _, f := range files {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		entries, err := e.scanFileOpts(ctx, f, filter, filter.Limit-len(results), ec)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			// 去重
			if ec.dedup {
				key := dedupKey(entry)
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			results = append(results, entry)
			if filter.Limit > 0 && len(results) >= filter.Limit {
				results = results[:filter.Limit]
				break
			}
		}
		if filter.Limit > 0 && len(results) >= filter.Limit {
			break
		}
	}

	// 按时间排序
	if ec.sortByTime {
		sort.Slice(results, func(i, j int) bool {
			return results[i].Timestamp < results[j].Timestamp
		})
	}

	return results, nil
}

// ExtractToFile 提取符合条件的条目到文件。
// 自动创建输出文件的父目录。输出为每行一个 JSON 对象的 JSONL 格式。
// 如果 filter.Limit > 0，则最多输出 Limit 条。
func (e *JSONLLogExtractor) ExtractToFile(ctx context.Context, filter *LogFilter, outputPath string) error {
	entries, err := e.Extract(ctx, filter)
	if err != nil {
		return err
	}

	// 自动创建父目录
	dir := filepath.Dir(outputPath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("log: mkdir %s: %w", dir, err)
		}
	}

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("log: create %s: %w", outputPath, err)
	}
	defer func() { _ = out.Close() }()
	w := bufio.NewWriter(out)
	defer func() { _ = w.Flush() }()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("log: marshal entry: %w", err)
		}
		data = append(data, '\n')
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("log: write: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("log: flush: %w", err)
	}
	return nil
}

// listJSONLFilesFiltered 列出日志目录下所有 .jsonl 文件，按时间范围过滤文件名。
// 从文件名提取日期（格式 YYYY-MM-DD）进行过滤。若 start/end 均为 nil，返回全部文件。
func (e *JSONLLogExtractor) listJSONLFilesFiltered(start, end *time.Time) ([]string, error) {
	return e.listJSONLFilesFilteredByTrack(start, end, "")
}

// listJSONLFilesFilteredByTrack 按 TrackType 限定扫描轨道，并按时间范围过滤文件名。
// trackType 为空时扫描所有轨道（根目录 + sessions/runs/events）。
func (e *JSONLLogExtractor) listJSONLFilesFilteredByTrack(start, end *time.Time, trackType string) ([]string, error) {
	var patterns []string
	switch trackType {
	case TrackSessions:
		patterns = []string{filepath.Join(e.dataDir, "sessions", "*.jsonl")}
	case TrackRuns:
		patterns = []string{filepath.Join(e.dataDir, "runs", "*.jsonl")}
	case TrackEvents:
		patterns = []string{filepath.Join(e.dataDir, "events", "*.jsonl")}
	default:
		patterns = []string{
			filepath.Join(e.dataDir, "*.jsonl"),
			filepath.Join(e.dataDir, "sessions", "*.jsonl"),
			filepath.Join(e.dataDir, "runs", "*.jsonl"),
			filepath.Join(e.dataDir, "events", "*.jsonl"),
		}
	}

	// 扫描日志目录下所有 .jsonl 文件
	var matches []string
	for _, pattern := range patterns {
		m, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m...)
	}
	if matches == nil {
		matches = []string{}
	}

	if start == nil && end == nil {
		sort.Strings(matches)
		return matches, nil
	}

	var filtered []string
	for _, path := range matches {
		base := filepath.Base(path)
		// 跳过 crash.log
		if strings.HasPrefix(base, "crash") {
			continue
		}
		// 尝试从文件名提取日期
		fileDate := extractDateFromName(base)
		if fileDate.IsZero() {
			// 无法提取日期则不过滤
			filtered = append(filtered, path)
			continue
		}
		if start != nil && fileDate.Before(start.Truncate(24*time.Hour)) {
			continue
		}
		if end != nil && fileDate.After(end.Truncate(24*time.Hour).Add(24*time.Hour)) {
			continue
		}
		filtered = append(filtered, path)
	}

	sort.Strings(filtered)
	return filtered, nil
}

// extractDateFromName 从文件名中提取日期（支持 2006-01-02 和 20060102 格式）。
func extractDateFromName(name string) time.Time {
	// 尝试匹配 YYYY-MM-DD
	for i := 0; i+10 <= len(name); i++ {
		if name[i+4] == '-' && name[i+7] == '-' {
			if t, err := time.Parse("2006-01-02", name[i:i+10]); err == nil {
				return t
			}
		}
	}
	// 尝试匹配 YYYYMMDD (8 位数字)
	for i := 0; i+8 <= len(name); i++ {
		if isAllDigits(name[i : i+8]) {
			if t, err := time.Parse("20060102", name[i:i+8]); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func isAllDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// scanFile 扫描单个文件，返回匹配过滤条件的条目（向后兼容）。
// limit > 0 时在达到上限后提前停止扫描。
func (e *JSONLLogExtractor) scanFile(path string, filter *LogFilter, limit int) ([]*ExecLogEntry, error) {
	return e.scanFileOpts(context.Background(), path, filter, limit, extractConfig{})
}

// scanFileOpts 扫描单个文件，支持取消和扩展配置。
func (e *JSONLLogExtractor) scanFileOpts(ctx context.Context, path string, filter *LogFilter, limit int, ec extractConfig) ([]*ExecLogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	bufSize := int(ec.maxFileSize)
	if bufSize <= 0 {
		bufSize = 64 * 1024
	}

	var results []*ExecLogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, bufSize), max(e.cfg.MaxLineSize, bufSize))
	for scanner.Scan() {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ExecLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // 跳过损坏行
		}
		if filter.Matches(&entry) {
			results = append(results, &entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, scanner.Err()
}

// dedupKey 计算条目的去重 hash。
func dedupKey(entry *ExecLogEntry) [32]byte {
	payload := entry.Timestamp + "|" + entry.Action + "|" + string(entry.Category)
	if entry.SessionID != "" {
		payload += "|" + entry.SessionID
	}
	if entry.Error != "" {
		payload += "|" + entry.Error
	}
	return sha256.Sum256([]byte(payload))
}

// max 返回两整数中的较大值。
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ─── 三轨信封分类与过滤 ──────────────────────────────────────────

// envelopeProbe 用于探测 JSON 行的记录类型（形态学检测）。
// 字段为指针可区分"缺失"与"空值"。ExecLogEntry 专用字段（action/level/tags）
// 也包含在此，用于信封过滤时决定是否应用 ExecLogEntry 专属过滤维度。
type envelopeProbe struct {
	Category string `json:"category"`
	SessionID string `json:"session_id"`
	Timestamp string `json:"ts"`
	Level string `json:"level"`
	Action string `json:"action"`
	Error string `json:"error"`
	Tags []string `json:"tags"`
	ItemType *string `json:"item_type"` // ItemRecord 专用
	EntryType *string `json:"entry_type"` // SessionRecord 专用
	EventType *string `json:"event_type"` // TurnRecord / EventRecord 共用
	Status *string `json:"status"` // TurnRecord 专用
}

// trackFromPath 从文件路径推导轨道名。
// sessions/*.jsonl → "sessions", runs/*.jsonl → "runs",
// events/*.jsonl → "events", 根目录 → "".
func trackFromPath(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(dir)
	switch base {
	case TrackSessions, TrackRuns, TrackEvents:
		return base
	}
	return ""
}

// classifyEnvelope 根据轨道名 + 探测字段确定信封类别。
//
// 优先级：category 字段存在 → 通用 ExecLogEntry；否则按轨道+形态推导：
// - sessions 轨 → SessionRecord
// - runs 轨 + item_type 存在 → ItemRecord
// - runs 轨 + status/event_type 存在 → TurnRecord
// - events 轨 → EventRecord
func classifyEnvelope(track string, probe envelopeProbe) LogCategory {
	if probe.Category != "" {
		return LogCategory(probe.Category)
	}
	switch track {
	case TrackSessions:
		return LogCategorySession
	case TrackRuns:
		if probe.ItemType != nil {
			return LogCategoryItem
		}
		if probe.Status != nil || probe.EventType != nil {
			return LogCategoryTurn
		}
		return LogCategoryAgent
	case TrackEvents:
		return LogCategoryEvent
	}
	return LogCategory(probe.Category)
}

// envelopeMatches 判断信封是否匹配过滤条件。
//
// 通用维度（Categories/SessionID/时间/HasError）适用于所有记录类型。
// ExecLogEntry 专属维度（Actions/Level/Tags）仅在探测到 category 字段存在
// 时应用（即通用 ExecLogEntry 条目），专用记录（Turn/Item/Event/Session）
// 无这些字段故跳过。
func envelopeMatches(probe envelopeProbe, category LogCategory, filter *LogFilter) bool {
	if len(filter.Categories) > 0 {
		hit := false
		for _, c := range filter.Categories {
			if category == c {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if filter.SessionID != "" && probe.SessionID != filter.SessionID {
		return false
	}
	// Actions 过滤：仅对通用 ExecLogEntry 条目生效
	if len(filter.Actions) > 0 {
		if probe.Category != "" { // 有 category 字段 → ExecLogEntry
			hit := false
			for _, a := range filter.Actions {
				if probe.Action == a {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		// 专用记录无 action 字段，不做过滤
	}
	if filter.HasError != nil {
		hasErr := probe.Error != ""
		if *filter.HasError != hasErr {
			return false
		}
	}
	// Level 过滤：仅对通用 ExecLogEntry 条目生效
	if filter.Level != "" {
		if probe.Category != "" && LogLevel(probe.Level) != filter.Level {
			return false
		}
	}
	// Tags 过滤：仅对通用 ExecLogEntry 条目生效
	if len(filter.Tags) > 0 {
		if probe.Category != "" {
			hit := false
			for _, want := range filter.Tags {
				for _, got := range probe.Tags {
					if got == want {
						hit = true
						break
					}
				}
				if hit {
					break
				}
			}
			if !hit {
				return false
			}
		}
	}
	// 时间过滤
	if filter.StartTime != nil || filter.EndTime != nil {
		ts, err := time.Parse(time.RFC3339Nano, probe.Timestamp)
		if err == nil {
			if filter.StartTime != nil && ts.Before(*filter.StartTime) {
				return false
			}
			if filter.EndTime != nil && ts.After(*filter.EndTime) {
				return false
			}
		}
	}
	return true
}

// scanFileEnvelopes 扫描单个文件，返回匹配过滤条件的信封。
func (e *JSONLLogExtractor) scanFileEnvelopes(ctx context.Context, path string, filter *LogFilter) ([]*LogEnvelope, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	track := trackFromPath(path)
	remaining := filter.Limit

	var results []*LogEnvelope
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, e.cfg.MaxScanBufferSize), e.cfg.MaxLineSize)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// 探测行类型
		var probe envelopeProbe
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			continue // 跳过损坏行
		}
		category := classifyEnvelope(track, probe)

		if envelopeMatches(probe, category, filter) {
			env := &LogEnvelope{
				Track: track,
				Category: category,
				Payload: json.RawMessage(line),
			}
			results = append(results, env)
			if remaining > 0 {
				remaining--
				if remaining == 0 {
					break
				}
			}
		}
	}
	return results, scanner.Err()
}
