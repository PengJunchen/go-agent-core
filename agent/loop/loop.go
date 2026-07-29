// Package loop 定义 LoopAgent 核心调度接口。
//
// LoopAgent 是 go-agent-core 的核心：query -> dispatch -> turn -> event
// 调度循环。它保持 完整能力（串行 dispatch、Turn 管理、
// Interrupt/Steer/FollowUp 干预、状态机），同时通过 L4/L3/L2 接口
// 解耦实现可替换性。
package loop

import (
	"context"

	"github.com/pengjunchen/go-agent-core/agent/event"
)

// AgentInput 是一次查询的输入。
type AgentInput struct {
	Prompt string
	SessionID string
}

// LoopAgent 是核心调度接口。
type LoopAgent interface {
	// Query 提交查询，返回事件通道。
	Query(ctx context.Context, input AgentInput) (<-chan event.AgentEvent, error)
	// Interrupt 中断当前 Turn。
	Interrupt(ctx context.Context) error
	// Steer 中途调整方向（不中断 Turn）。
	Steer(ctx context.Context, message string) error
	// FollowUp 在 Turn 完成后追加后续消息。
	FollowUp(ctx context.Context, content string) error
	// Status 返回当前状态。
	Status() event.AgentStatus
	// Close 释放资源。
	Close() error
}
