package log

import (
	"errors"
	"fmt"
	"os"
)

// LogConfig 汇聚所有日志相关的可配置参数。
// 零值 = 使用默认值，NewWithConfig 自动填充缺省字段。
type LogConfig struct {
	// === 文件布局 ===
	// DataDir 是日志文件存放目录，默认 "./logs"
	DataDir string `json:"data_dir" yaml:"data_dir"`

	// FilePattern 是文件命名模板，支持 {{.Date}} 占位符，默认 "exec_{{.Date}}.jsonl"
	// 示例: "exec_{{.Date}}.jsonl" → exec_2026-07-29.jsonl
	FilePattern string `json:"file_pattern" yaml:"file_pattern"`

	// === 缓冲策略 ===
	// BufferSize 是 bufio.Writer 缓冲区大小（字节），默认 4096 (4KB)
	BufferSize int `json:"buffer_size" yaml:"buffer_size"`

	// FlushMode 是刷盘触发模式: "line" / "batch"(默认) / "interval"
	// "line" — 每写入一行立即 Flush（最低延迟，最高 IO）
	// "batch" — 缓冲区写满或显式 Flush 时刷盘（吞吐优先）
	// "interval" — 定时刷盘（配合 FlushIntervalMs 使用）
	FlushMode string `json:"flush_mode" yaml:"flush_mode"`

	// FlushIntervalMs 是定时刷盘间隔（毫秒），仅 FlushMode="interval" 时生效
	// 默认 0（不使用定时器）
	FlushIntervalMs int `json:"flush_interval_ms" yaml:"flush_interval_ms"`

	// === 文件轮转 ===
	// RotationBySizeEnabled 是否启用按大小轮转，默认 true
	RotationBySizeEnabled bool `json:"rotation_by_size_enabled" yaml:"rotation_by_size_enabled"`

	// MaxFileSize 是单文件大小上限（字节），默认 100 * 1024 * 1024 (100MB)
	MaxFileSize int64 `json:"max_file_size" yaml:"max_file_size"`

	// RotationByDateEnabled 是否启用按日期轮转，默认 true
	RotationByDateEnabled bool `json:"rotation_by_date_enabled" yaml:"rotation_by_date_enabled"`

	// MaxBackups 是每个日期保留的最大轮转文件数（不含基准文件）。
	// 超过此数量的旧轮转文件在创建新文件时被清理。零值 = 不限制。
	MaxBackups int `json:"max_backups" yaml:"max_backups"`

	// === crash 旁路日志 ===
	// CrashLogName 是 crash 旁路日志文件名，默认 "crash.log"
	CrashLogName string `json:"crash_log_name" yaml:"crash_log_name"`

	// MaxCrashLogSize 是 crash 旁路日志的最大字节数。达到上限后截断（保留后半段）。
	// 默认值：1MB (1048576)
	MaxCrashLogSize int64 `json:"max_crash_log_size" yaml:"max_crash_log_size"`

	// DisableCrashLog 禁用 crash 旁路日志（故障条目静默丢弃）。
	// 默认 false（启用 crash 旁路）。
	DisableCrashLog bool `json:"disable_crash_log" yaml:"disable_crash_log"`

	// === 保留策略 ===
	// RetentionDays 是日志文件保留天数。超过此天数的文件在初始化时被清理。
	// 零值表示不清理。
	RetentionDays int `json:"retention_days" yaml:"retention_days"`

	// TODO(M5): 考虑增加 CompressArchive bool 控制轮转后是否 gzip 归档旧文件。
	// TODO(M5): 考虑增加 ArchiveRetentionDays int 控制归档文件的保留天数。

	// === 文件权限 ===
	// OpenFilePerm 是创建日志文件时的权限位，默认 0600
	OpenFilePerm os.FileMode `json:"open_file_perm" yaml:"open_file_perm"`

	// === 提取规则（可选，M3 定义为元数据，M5 启用）===
	// ExtractRules 是预定义的提取规则列表。M3 定义为元数据，M5 启用。
	ExtractRules []ExtractRule `json:"extract_rules,omitempty" yaml:"extract_rules,omitempty"`

	// === line 模式专用缓冲阈值 ===
	// LineBufferFlushSize 是 line 模式下每累积多少字节才 Flush。
	// 零值 = 每行立即 Flush。仅 FlushMode="line" 时生效。
	LineBufferFlushSize int `json:"line_buffer_flush_size" yaml:"line_buffer_flush_size"`
}

// ExtractRule 定义一条提取规则。
type ExtractRule struct {
	Name string `json:"name" yaml:"name"`
	Output string `json:"output" yaml:"output"`
	Filter LogFilter `json:"filter" yaml:"filter"`
	SortByTime bool `json:"sort_by_time" yaml:"sort_by_time"`
}

// DefaultLogConfig 返回不可变的默认配置副本。
func DefaultLogConfig() LogConfig {
	return LogConfig{
		DataDir: "./logs",
		FilePattern: "exec_{{.Date}}.jsonl",
		BufferSize: 4096,
		FlushMode: "batch",
		FlushIntervalMs: 0,
		RotationBySizeEnabled: true,
		MaxFileSize: 100 * 1024 * 1024, // 100MB
		RotationByDateEnabled: true,
		MaxBackups: 0,
		CrashLogName: "crash.log",
		MaxCrashLogSize: 1024 * 1024, // 1MB
		DisableCrashLog: false,
		RetentionDays: 0,
		OpenFilePerm: 0o600,
		ExtractRules: nil,
		LineBufferFlushSize: 0,
	}
}

// Validate 校验配置合法性，返回首个错误。
// 校验项：BufferSize > 0, MaxFileSize > 0, FlushMode ∈ {line,batch,interval}
func (c *LogConfig) Validate() error {
	if c.BufferSize <= 0 {
		return errors.New("log: BufferSize must be > 0")
	}
	if c.MaxFileSize <= 0 {
		return errors.New("log: MaxFileSize must be > 0")
	}
	switch c.FlushMode {
	case "line", "batch", "interval":
		// valid
	default:
		return fmt.Errorf("log: invalid FlushMode %q (must be line/batch/interval)", c.FlushMode)
	}
	if c.FlushMode == "interval" && c.FlushIntervalMs <= 0 {
		return errors.New("log: FlushIntervalMs must be > 0 when FlushMode=interval")
	}
	if c.MaxCrashLogSize <= 0 && !c.DisableCrashLog {
		return errors.New("log: MaxCrashLogSize must be > 0")
	}
	if c.DataDir == "" {
		return errors.New("log: DataDir must not be empty")
	}
	return nil
}

// applyDefaults 用默认值填充零值字段，返回副本。
func (c *LogConfig) applyDefaults() LogConfig {
	def := DefaultLogConfig()
	cfg := *c

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
		cfg.FlushIntervalMs = 1000 // default 1s if interval mode
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

	return cfg
}
