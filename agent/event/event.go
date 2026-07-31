// Package event 定义 Agent 事件系统。
//
// AgentEvent 是 Agent 引擎对外发射的事件，事件类型：
// Turn 开始/结束、文本增量、思维增量、工具调用开始/结果、完成、错误等。
// 事件通过 EventStream 流式推送，带背压控制。
package event

// EventType 枚举事件类型。
type EventType int

const (
	// EventTurnStart Turn 开始。
	EventTurnStart EventType = iota
	// EventTextDelta 文本增量。
	EventTextDelta
	// EventThinkingDelta 思维增量。
	EventThinkingDelta
	// EventToolCallStart 工具调用开始。
	EventToolCallStart
	// EventToolCallResult 工具调用结果。
	EventToolCallResult
	// EventTurnEnd Turn 结束。
	EventTurnEnd
	// EventCompleted 正常完成。
	EventCompleted
	// EventMaxTurnsReached 达到最大轮次。
	EventMaxTurnsReached
	// EventToolLoopDetected 工具循环检测。
	EventToolLoopDetected
	// EventError 错误。
	EventError
	// EventCompactStart 压缩开始。
	EventCompactStart
	// EventCompactEnd 压缩结束。
	EventCompactEnd
	// EventApprovalRequest 审批请求。
	EventApprovalRequest
	// EventToolExecutionUpdate 工具执行状态更新。
	EventToolExecutionUpdate
)

// ToolExecutionUpdate 是 EventToolExecutionUpdate 事件的载荷。
type ToolExecutionUpdate struct {
	ToolCallID string
	ToolName string
	Status string // "started", "progress", "completed", "failed"
	Progress float64 // 0.0 - 1.0，可选进度指示
	Message string // 可选状态消息
}

// AgentEvent 是一个 Agent 事件。
type AgentEvent struct {
	Type EventType
	SubmissionID string
	TurnID string
	SessionID string
	Payload any
	Error error
	Timestamp int64
}

// AgentStatus 枚举 Agent 状态。
type AgentStatus string

const (
	// StatusIdle 空闲。
	StatusIdle AgentStatus = "idle"
	// StatusRunning 运行中。
	StatusRunning AgentStatus = "running"
	// StatusCompleted 已完成。
	StatusCompleted AgentStatus = "completed"
	// StatusError 错误。
	StatusError AgentStatus = "error"
	// StatusWaitingApproval 等待审批。
	StatusWaitingApproval AgentStatus = "waiting_approval"
	// StatusCanceled 已取消。
	StatusCanceled AgentStatus = "canceled"
)

// agentStatusTransitions 是合法的状态转换白名单。
var agentStatusTransitions = map[AgentStatus]map[AgentStatus]bool{
	StatusIdle: {
		StatusRunning: true,
	},
	StatusRunning: {
		StatusCompleted: true,
		StatusError: true,
		StatusWaitingApproval: true,
		StatusCanceled: true,
	},
	StatusWaitingApproval: {
		StatusRunning: true,
		StatusCanceled: true,
	},
	StatusCompleted: {
		StatusRunning: true,
		StatusIdle: true,
	},
	StatusError: {
		StatusRunning: true,
		StatusIdle: true,
	},
	StatusCanceled: {
		StatusIdle: true,
		StatusRunning: true,
	},
}

// CanTransition 判断状态转换是否合法。
func CanTransition(from, to AgentStatus) bool {
	allowed, ok := agentStatusTransitions[from]
	if !ok {
		return false
	}
	return allowed[to]
}
