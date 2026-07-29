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
	// 扫描 dataDir 及其子目录（sessions/runs/events）中的 .jsonl 文件
	var matches []string
	for _, pattern := range []string{
		filepath.Join(e.dataDir, "*.jsonl"),
		filepath.Join(e.dataDir, "sessions", "*.jsonl"),
		filepath.Join(e.dataDir, "runs", "*.jsonl"),
		filepath.Join(e.dataDir, "events", "*.jsonl"),
	} {
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

// listJSONLFiles 列出日志目录下所有 .jsonl 文件（按名升序，向后兼容占位）。
// 当前逻辑委托给 listJSONLFilesFiltered。
func (e *JSONLLogExtractor) listJSONLFiles() ([]string, error) {
	return e.listJSONLFilesFiltered(nil, nil)
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
