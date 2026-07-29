package event

import "testing"

// Interface-001: AgentStatus 状态转换合法性。
func TestAgentStatus_Transitions(t *testing.T) {
	tests := []struct {
		from AgentStatus
		to AgentStatus
		want bool
	}{
		{StatusIdle, StatusRunning, true},
		{StatusRunning, StatusCompleted, true},
		{StatusRunning, StatusError, true},
		{StatusRunning, StatusWaitingApproval, true},
		{StatusRunning, StatusCanceled, true},
		{StatusIdle, StatusCompleted, false},
		{StatusCompleted, StatusRunning, true},
		{StatusWaitingApproval, StatusRunning, true},
		{StatusWaitingApproval, StatusCanceled, true},
		{StatusError, StatusRunning, true},
	}
	for _, tt := range tests {
		if got := CanTransition(tt.from, tt.to); got != tt.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.want)
		}
	}
}
