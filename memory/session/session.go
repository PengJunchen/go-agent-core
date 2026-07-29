// Package session 定义会话管理抽象。
//
// SessionManager 管理 Agent 会话的创建、查询、更新、删除。
// 默认实现 JSONLSessionStore 落盘到 JSONL 文件，第三方可替换为
// Redis/DB 后端。设计 SessionManager。
package session

import (
	"context"
	"time"
)

// SessionManager 是会话管理接口。
type SessionManager interface {
	CreateSession(ctx context.Context, opts *SessionOptions) (*Session, error)
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	UpdateSession(ctx context.Context, sessionID string, update *SessionUpdate) error
	DeleteSession(ctx context.Context, sessionID string) error
	ListSessions(ctx context.Context, opts *ListOptions) ([]*Session, error)
}

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
	// SessionActive 活跃。
	SessionActive SessionStatus = "active"
	// SessionCompleted 已完成。
	SessionCompleted SessionStatus = "completed"
	// SessionFailed 失败。
	SessionFailed SessionStatus = "failed"
	// SessionCanceled 已取消。
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
