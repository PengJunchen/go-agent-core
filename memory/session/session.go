// Package session 定义会话管理抽象。
//
// 会话管理分为两个独立接口：
//
// - SessionManager 管理活跃会话的 CRUD（热路径，影响 Agent 运行时）
// - SessionSink 管理会话树的持久化与重建（冷路径，影响审计与恢复）
//
// 热冷分离的好处：
// - SessionManager 可替换为 Redis/DB 后端（低延迟要求）
// - SessionSink 可替换为 OTel/Loki 后端（高吞吐要求）
// - kill-9 后从 SessionSink 重建会话树，不依赖 SessionManager 的内存状态
package session

import (
	"context"
	"time"
)

// ─── SessionManager（热路径：活跃会话 CRUD） ─────────────────────

// SessionManager 管理活跃会话。
type SessionManager interface {
	CreateSession(ctx context.Context, opts *SessionOptions) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	UpdateSession(ctx context.Context, sessionID string, update *SessionUpdate) error
	DeleteSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, opts *ListOptions) ([]*Session, error)
}

// ─── SessionSink（冷路径：持久化与重建） ─────────────────────────

// SessionSink 管理会话树的全量持久化（冷路径）。
//
// 与 SessionManager 解耦：
// - Memory 管活跃分支（热路径，影响推理）
// - Sink 管全量落盘（冷路径，影响审计）
//
// 默认实现 JSONLSessionSink 写入 sessions/*.jsonl。
// 替换场景：OTel Exporter、Loki Push、DB 批量写入。
type SessionSink interface {
	// Append 追加一条会话树条目（append-only，不修改历史）。
	Append(ctx context.Context, entry SessionEntry) error
	// LoadTree 从持久化存储加载完整会话树。
	LoadTree(ctx context.Context, sessionID string) (*SessionTreeData, error)
	// Flush 强制刷盘。
	Flush(ctx context.Context) error
	// Close 释放资源。
	Close() error
}

// SessionEntry 是会话树的一条持久化条目。
type SessionEntry struct {
	Timestamp string `json:"ts"`
	SessionID string `json:"session_id"`
	EntryType string `json:"entry_type"` // "message" | "branch" | "compaction" | "label" | "session_start" | "session_end"
	ParentID string `json:"parent_id,omitempty"`
	Data any `json:"data,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SessionTreeData 是从 SessionSink 重建的完整会话树快照。
//
// 这是一个纯数据容器，由 SessionSink.LoadTree 返回。
// 活跃的树管理（增删节点、分支导航）使用 SessionTree 类型。
type SessionTreeData struct {
	SessionID string
	RootID string
	Branches []BranchInfo
	Entries []SessionEntry
}

// BranchInfo 描述一个分支。
type BranchInfo struct {
	BranchID string
	ParentID string
	EntryPoint string // 分支起始 entry ID
	CreatedAt time.Time
}

// ─── 共享类型 ────────────────────────────────────────────────────

// Session 是一个会话实例。
type Session struct {
	ID string
	ContextID string
	Status SessionStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SessionStatus 枚举会话状态。
type SessionStatus string

const (
	SessionActive SessionStatus = "active"
	SessionCompleted SessionStatus = "completed"
	SessionFailed SessionStatus = "failed"
	SessionCanceled SessionStatus = "canceled"
)

// SessionOptions 是创建会话的参数。
type SessionOptions struct {
	ContextID string
}

// SessionUpdate 是更新会话的参数。
type SessionUpdate struct {
	Status *SessionStatus
}

// ListOptions 是列出会话的过滤参数。
type ListOptions struct {
	ContextID string
	Status SessionStatus
	Limit int
}
