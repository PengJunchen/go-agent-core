// Package log 的 LogExtractor 子能力：选择性取走日志。
//
// LogExtractor 按 LogFilter 过滤 JSONL 日志文件，输出到指定目标。
// 支持按类别、会话、时间、动作、是否出错过滤。
package log

import (
	"context"
	"time"
)

// LogExtractor 是日志提取器接口。
type LogExtractor interface {
	// Extract 提取符合条件的条目到内存。
	Extract(ctx context.Context, filter *LogFilter) ([]*ExecLogEntry, error)
	// ExtractToFile 提取符合条件的条目到文件。
	ExtractToFile(ctx context.Context, filter *LogFilter, outputPath string) error
}

// LogFilter 是日志过滤条件。
type LogFilter struct {
	Categories []LogCategory
	SessionID string
	StartTime *time.Time
	EndTime *time.Time
	Actions []string
	HasError *bool
	Limit int
}

// Matches 判断一条日志是否匹配过滤条件。
func (f *LogFilter) Matches(e *ExecLogEntry) bool {
	if e == nil {
		return false
	}
	if len(f.Categories) > 0 {
		hit := false
		for _, c := range f.Categories {
			if e.Category == c {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if f.SessionID != "" && e.SessionID != f.SessionID {
		return false
	}
	if len(f.Actions) > 0 {
		hit := false
		for _, a := range f.Actions {
			if e.Action == a {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if f.HasError != nil {
		hasErr := e.Error != ""
		if *f.HasError != hasErr {
			return false
		}
	}
	// 时间过滤（解析 Timestamp 失败时不滤除）
	if f.StartTime != nil || f.EndTime != nil {
		ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err == nil {
			if f.StartTime != nil && ts.Before(*f.StartTime) {
				return false
			}
			if f.EndTime != nil && ts.After(*f.EndTime) {
				return false
			}
		}
	}
	return true
}
