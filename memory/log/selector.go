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
// - TrackType: 限定轨道（"" | "sessions" | "runs" | "events"）
// - Output: 输出目标
type LogSelector struct {
	DataDir string // 日志根目录，空则用 "."
	Types []string // "turn" / "item" / "event" / "session"
	Levels []string // "info" / "warn" / "error" / "debug"
	Tags []string // 自定义标签
	Since *time.Time // 起始时间
	Until *time.Time // 截止时间
	Limit int // 0 = 不限
	TrackType string // "" | "sessions" | "runs" | "events"
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

	// 三轨感知提取
	envelopes, err := extractor.ExtractEnvelopes(ctx, filter)
	if err != nil {
		return nil, err
	}

	// 从信封导出向后兼容的 ExecLogEntry 列表
	// （复刻旧 Extract 的行为：每行解析为 ExecLogEntry，专用字段丢失但通用字段保留）
	var entries []*ExecLogEntry
	for _, env := range envelopes {
		entry, eErr := env.ParseAsExecLogEntry()
		if eErr != nil {
			continue // 跳过无法解析的行
		}
		entries = append(entries, entry)
	}

	summary := &SelectSummary{
		TotalScanned: len(envelopes),
		Envelopes: envelopes,
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
	Entries []*ExecLogEntry `json:"entries"` // 向后兼容轨（通用字段保留，专用字段丢失）
	Envelopes []*LogEnvelope `json:"envelopes"` // 三轨信封（专用字段可解析）
}

// ─── 辅助方法 ────────────────────────────────────────────────────

// toLogFilter 将 LogSelector 转换为 LogFilter。
func (sel LogSelector) toLogFilter() *LogFilter {
	f := &LogFilter{
		StartTime: sel.Since,
		EndTime: sel.Until,
		Limit: sel.Limit,
		Tags: sel.Tags,
		TrackType: sel.TrackType,
	}
	// Types → Categories 映射
	// turn/item/event/session 同时映射到专用类别（Turn/Item/Event）和
	// 通用 ExecLogEntry 类别，确保专用记录信封和通用条目都能匹配。
	catMap := map[string][]LogCategory{
		"turn": {LogCategoryTurn, LogCategoryAgent},
		"item": {LogCategoryItem, LogCategoryTool, LogCategoryLLM, LogCategoryCompact, LogCategoryHITL},
		"event": {LogCategoryEvent, LogCategorySystem},
		"session": {LogCategorySession},
	}
	for _, t := range sel.Types {
		if cats, ok := catMap[t]; ok {
			f.Categories = append(f.Categories, cats...)
		}
	}
	// Levels → LogLevel 字段
	// LogFilter.Level 为单值字段（LogLevel），只取第一个匹配的 level。
	// 若后续需要多级别过滤，可将 LogFilter.Level 改为 Levels []LogLevel。
	for _, l := range sel.Levels {
		switch l {
		case "debug":
			f.Level = LogLevelDebug
		case "info":
			f.Level = LogLevelInfo
		case "warn":
			f.Level = LogLevelWarn
		case "error":
			t := true
			f.HasError = &t
			f.Level = LogLevelError
		}
		return f // 只取第一个匹配的 level，单值字段无需继续
	}
	return f
}
