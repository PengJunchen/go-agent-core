// Package log 的 LogSelector 实现。
//
// LogSelector 是配置驱动的选择性日志取走，。
// 支持 type/level/tags/time 四维度过滤，输出到 io.Writer 或文件。
//
// 设计原则：日志永远写入（不可关闭），用户通过 Select 选择性取走感兴趣的子集。
package log

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"
)

// ─── LogSelector ─────────────────────────────────────────────────

// LogSelector 是配置驱动的选择性日志取走。
//
// LogSelector 设计：
// - Types: turn/item/event/session（按日志轨道过滤）
// - Levels: info/warn/error/debug（按严重程度过滤）
// - Tags: 自定义标签（如 "tool:edit", "provider:openai"）
// - Since/Until: 时间范围
// - Limit: 限制数量
// - Output: 输出目标
type LogSelector struct {
	DataDir string // 日志根目录，空则用 "."
	Types []string // "turn" / "item" / "event" / "session"
	Levels []string // "info" / "warn" / "error" / "debug"
	Tags []string // 自定义标签
	Since *time.Time // 起始时间
	Until *time.Time // 截止时间
	Limit int // 0 = 不限
	Output io.Writer // 取走到哪里
}

// Select 执行选择性取走，返回匹配条目摘要。
func Select(ctx context.Context, sel LogSelector) (*SelectSummary, error) {
	dataDir := sel.DataDir
	if dataDir == "" {
		dataDir = "."
	}
	extractor := NewJSONLLogExtractor(dataDir)
	filter := sel.toLogFilter()
	entries, err := extractor.Extract(ctx, filter)
	if err != nil {
		return nil, err
	}
	summary := &SelectSummary{
		TotalScanned: len(entries),
		Entries: entries,
	}
	if sel.Output != nil {
		for _, e := range entries {
			data, _ := json.Marshal(e)
			data = append(data, '\n')
			_, _ = sel.Output.Write(data)
		}
	}
	return summary, nil
}

// SelectToFile 执行选择性取走，输出到文件。
func SelectToFile(ctx context.Context, sel LogSelector, outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sel.Output = f
	_, err = Select(ctx, sel)
	return err
}

// SelectSummary 是取走操作的摘要。
type SelectSummary struct {
	TotalScanned int `json:"total_scanned"`
	Entries []*ExecLogEntry `json:"entries"`
}

// ─── 辅助方法 ────────────────────────────────────────────────────

// toLogFilter 将 LogSelector 转换为 LogFilter。
func (sel LogSelector) toLogFilter() *LogFilter {
	f := &LogFilter{
		StartTime: sel.Since,
		EndTime: sel.Until,
		Limit: sel.Limit,
	}
	// Types → Categories 映射
	catMap := map[string][]LogCategory{
		"turn": {LogCategoryAgent},
		"item": {LogCategoryTool, LogCategoryLLM, LogCategoryCompact, LogCategoryHITL},
		"event": {LogCategorySystem},
		"session": {LogCategorySession},
	}
	for _, t := range sel.Types {
		if cats, ok := catMap[t]; ok {
			f.Categories = append(f.Categories, cats...)
		}
	}
	// Levels → HasError 简化
	for _, l := range sel.Levels {
		if l == "error" {
			t := true
			f.HasError = &t
			break
		}
	}
	return f
}
