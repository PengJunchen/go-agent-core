// Package log 定义执行日志抽象。
//
// ExecLogger 是 go-agent-core 的核心设计约束之一：执行日志永远写入
// JSONL 文件，不可关闭。所有执行动作（LLM 调用、工具执行、会话变更、
// 上下文压缩、HITL 审批）都通过 ExecLogger 记录。
//
// 日志按三轨落盘（）：
// - sessions/*.jsonl — 会话树（可分支，compaction 检查点）
// - runs/*.jsonl — turn/item 级执行轨迹（推理/工具调用/中断/Steer）
// - events/*.jsonl — 事件流原样（含 thinking_delta/text_delta/tool delta）
//
// 三轨设计保证：
// - kill-9 后可从 sessions+runs 重建会话树与最近 turn 状态
// - events 轨可重放完整事件流（UI 重绘、观测）
// - runs 轨可审计每次推理与工具调用的输入输出与耗时
//
// 用户通过 LogSelector 选择性取走感兴趣的条目，而非关闭写入。
// 设计原则：可审计性、可调试性、可训练性。
package log

import (
	"context"
	"time"
)

// ─── 核心接口 ─────────────────────────────────────────────────────

// ExecLogger 是执行日志接口。
//
// 三轨专用方法（LogTurn/LogItem/LogEvent/LogSession）按轨道写入；
// Log 是通用入口，按 Category 自动分发到对应轨道。
// Flush 强制刷盘所有轨道，Close 释放资源。
type ExecLogger interface {
	// Log 通用写入入口，按 Category 分发到对应轨道。
	Log(ctx context.Context, entry *ExecLogEntry) error
	// LogTurn 写入 turn 级记录 → runs/*.jsonl
	LogTurn(ctx context.Context, rec *TurnRecord) error
	// LogItem 写入 item 级记录 → runs/*.jsonl
	LogItem(ctx context.Context, rec *ItemRecord) error
	// LogEvent 写入事件流记录 → events/*.jsonl
	LogEvent(ctx context.Context, rec *EventRecord) error
	// LogSession 写入会话树记录 → sessions/*.jsonl
	LogSession(ctx context.Context, rec *SessionRecord) error
	// Flush 强制刷盘所有轨道。
	Flush(ctx context.Context) error
	// Close 释放资源（关闭前自动 Flush）。
	Close() error
}

// ─── 通用日志条目（保持向后兼容） ────────────────────────────────

// ExecLogEntry 是一条通用执行日志（向后兼容，新代码优先用专用 Record）。
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
	LogCategoryLLM LogCategory = "llm"
	LogCategoryTool LogCategory = "tool"
	LogCategorySession LogCategory = "session"
	LogCategoryCompact LogCategory = "compact"
	LogCategoryAgent LogCategory = "agent"
	LogCategoryHITL LogCategory = "hitl"
	LogCategorySystem LogCategory = "system"
)

// ─── 三轨专用记录类型 ────────────────────────────────────────────

// TurnRecord 是 runs 轨的 turn 级记录。
//
// 每个 turn 开始/结束时写入，记录整体耗时与状态。
type TurnRecord struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	TurnID string `json:"turn_id"`
	EventType string `json:"event_type"` // "turn_start" | "turn_end"
	Status string `json:"status"` // "running" | "completed" | "error" | "interrupted"
	TokenUsage *TokenUsageLog `json:"token_usage,omitempty"`
	Duration int64 `json:"duration_ms,omitempty"`
	Error string `json:"error,omitempty"`
}

// ItemRecord 是 runs 轨的 item 级记录。
//
// 每个 LLM 调用、工具调用、中断、Steer 事件写入。
type ItemRecord struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	TurnID string `json:"turn_id"`
	ItemType string `json:"item_type"` // "llm_call" | "tool_call" | "tool_result" | "interrupt" | "steer"
	Provider string `json:"provider,omitempty"`
	Model string `json:"model,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	Input any `json:"input,omitempty"`
	Output any `json:"output,omitempty"`
	TokenUsage *TokenUsageLog `json:"token_usage,omitempty"`
	Duration int64 `json:"duration_ms,omitempty"`
	Error string `json:"error,omitempty"`
}

// EventRecord 是 events 轨的记录。
//
// 事件流原样写入，含 thinking_delta / text_delta / tool delta。
type EventRecord struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	TurnID string `json:"turn_id"`
	EventType string `json:"event_type"` // "text_delta" | "thinking_delta" | "tool_call_start" | "tool_call_result" | "done" | "error"
	Content string `json:"content,omitempty"`
	Thinking string `json:"thinking,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName string `json:"tool_name,omitempty"`
	Duration int64 `json:"duration_ms,omitempty"`
	Error string `json:"error,omitempty"`
}

// SessionRecord 是 sessions 轨的记录。
//
// 会话树的每条 entry（message/branch/compaction/label）写入。
type SessionRecord struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	EntryType string `json:"entry_type"` // "message" | "branch" | "compaction" | "label" | "session_start" | "session_end"
	ParentID string `json:"parent_id,omitempty"`
	Data any `json:"data,omitempty"`
	TokenUsage *TokenUsageLog `json:"token_usage,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TokenUsageLog 是 token 用量的日志表示。
type TokenUsageLog struct {
	Input int `json:"input"`
	Output int `json:"output"`
	CacheRead int `json:"cache_read,omitempty"`
	CacheWrite int `json:"cache_write,omitempty"`
}

// ─── 构造辅助 ────────────────────────────────────────────────────

// NewEntry 构造一条带时间戳的通用日志条目。
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

// NewTurnRecord 构造一条 Turn 记录。
func NewTurnRecord(eventType, sessionID, turnID, status string) *TurnRecord {
	return &TurnRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		TurnID: turnID,
		EventType: eventType,
		Status: status,
	}
}

// NewItemRecord 构造一条 Item 记录。
func NewItemRecord(itemType, sessionID, turnID string) *ItemRecord {
	return &ItemRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		TurnID: turnID,
		ItemType: itemType,
	}
}

// NewEventRecord 构造一条 Event 记录。
func NewEventRecord(eventType, sessionID, turnID string) *EventRecord {
	return &EventRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		TurnID: turnID,
		EventType: eventType,
	}
}

// NewSessionRecord 构造一条 Session 记录。
func NewSessionRecord(entryType, sessionID string) *SessionRecord {
	return &SessionRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		SessionID: sessionID,
		EntryType: entryType,
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
