// Package log 的 JSONL LogExtractor 默认实现。
//
// JSONLLogExtractor 扫描日志目录下所有 JSONL 文件，按 LogFilter 过滤
// 条目。支持按类别、会话、时间、动作、是否出错过滤，可输出到内存或文件。
package log

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// JSONLLogExtractor 是默认的 LogExtractor 实现。
type JSONLLogExtractor struct {
	dataDir string
}

// NewJSONLLogExtractor 构造一个提取器。
func NewJSONLLogExtractor(dataDir string) *JSONLLogExtractor {
	return &JSONLLogExtractor{dataDir: dataDir}
}

// Extract 提取符合条件的条目到内存。
func (e *JSONLLogExtractor) Extract(ctx context.Context, filter *LogFilter) ([]*ExecLogEntry, error) {
	if filter == nil {
		filter = &LogFilter{}
	}
	files, err := e.listJSONLFiles()
	if err != nil {
		return nil, err
	}
	var results []*ExecLogEntry
	for _, f := range files {
		entries, err := e.scanFile(f, filter)
		if err != nil {
			return nil, err
		}
		results = append(results, entries...)
		if filter.Limit > 0 && len(results) >= filter.Limit {
			results = results[:filter.Limit]
			break
		}
	}
	return results, nil
}

// ExtractToFile 提取符合条件的条目到文件。
func (e *JSONLLogExtractor) ExtractToFile(ctx context.Context, filter *LogFilter, outputPath string) error {
	entries, err := e.Extract(ctx, filter)
	if err != nil {
		return err
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
			return err
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// listJSONLFiles 列出日志目录下所有 .jsonl 文件（按名升序）。
func (e *JSONLLogExtractor) listJSONLFiles() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(e.dataDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	// 排除 crash.log（虽不是 .jsonl，但防御性过滤）
	var files []string
	files = append(files, matches...)
	return files, nil
}

// scanFile 扫描单个文件，返回匹配过滤条件的条目。
func (e *JSONLLogExtractor) scanFile(path string, filter *LogFilter) ([]*ExecLogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var results []*ExecLogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
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
		}
	}
	return results, scanner.Err()
}
