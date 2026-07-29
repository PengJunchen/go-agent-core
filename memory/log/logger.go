// Package log 定义执行日志抽象。
//
// ExecLogger 是 go-agent-core 的核心设计约束之一：执行日志永远写入
// JSONL 文件，不可关闭。所有执行动作（LLM 调用、工具执行、会话变更、
// 上下文压缩、HITL 审批）都通过 ExecLogger 记录。
//
// 用户通过 LogExtractor 选择性取走感兴趣的条目，而非关闭写入。
// 设计原则：可审计性、可调试性、可训练性。
package log

import (
	"context"
	"time"
)

// ExecLogger 是执行日志接口。
//
// Log 写入一条日志条目，Flush 强制刷盘，Close 释放资源。
// 实现必须保证：写入失败不阻塞主流程（记录到 crash 旁路），
// 但调用方仍可感知错误。
type ExecLogger interface {
	Log(ctx context.Context, entry *ExecLogEntry) error
	Flush(ctx context.Context) error
	Close() error
}

// ExecLogEntry 是一条执行日志。
type ExecLogEntry struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	TurnID string `json:"turn_id"`
	Category LogCategory `json:"category"`
	Action string `json:"action"`
	Payload map[string]any `json:"payload"`
	Duration int64 `json:"duration_ms"`
	Error string `json:"error,omitempty"`
}

// LogCategory 枚举日志类别。
type LogCategory string

const (
	// LogCategoryLLM LLM 调用。
	LogCategoryLLM LogCategory = "llm"
	// LogCategoryTool 工具执行。
	LogCategoryTool LogCategory = "tool"
	// LogCategorySession 会话变更。
	LogCategorySession LogCategory = "session"
	// LogCategoryCompact 上下文压缩。
	LogCategoryCompact LogCategory = "compact"
	// LogCategoryAgent Agent 生命周期。
	LogCategoryAgent LogCategory = "agent"
	// LogCategoryHITL HITL 审批。
	LogCategoryHITL LogCategory = "hitl"
	// LogCategorySystem 系统。
	LogCategorySystem LogCategory = "system"
)

// NewEntry 构造一条带时间戳的日志条目。
func NewEntry(category LogCategory, action, sessionID, turnID string) *ExecLogEntry {
	return &ExecLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		TurnID: turnID,
		Category: category,
		Action: action,
		Payload: make(map[string]any),
	}
}

// WithDuration 设置耗时。
func (e *ExecLogEntry) WithDuration(d time.Duration) *ExecLogEntry {
	e.Duration = d.Milliseconds()
	return e
}

// WithError 设置错误。
func (e *ExecLogEntry) WithError(err error) *ExecLogEntry {
	if err != nil {
		e.Error = err.Error()
	}
	return e
}

// WithPayload 合并 payload。
func (e *ExecLogEntry) WithPayload(p map[string]any) *ExecLogEntry {
	for k, v := range p {
		e.Payload[k] = v
	}
	return e
}
