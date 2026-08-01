package production

import (
	"context"
	"testing"
	"time"
)

func TestLogAuditLogger_Interface(t *testing.T) {
	var _ AuditLogger = (*LogAuditLogger)(nil)
}

func TestLogAuditLogger_LogToolCall(t *testing.T) {
	al := NewLogAuditLogger(nil)
	err := al.LogToolCall(context.Background(), AuditToolCallEvent{
		Timestamp: time.Now(), SessionID: "s-1", ToolName: "search",
		Arguments: map[string]any{"q": "test"}, Result: "ok", Approved: true, DecisionBy: "auto",
	})
	if err != nil {
		t.Errorf("LogToolCall err = %v", err)
	}
}

func TestLogAuditLogger_LogApproval(t *testing.T) {
	al := NewLogAuditLogger(nil)
	err := al.LogApproval(context.Background(), AuditApprovalEvent{
		Timestamp: time.Now(), SessionID: "s-1", ToolCallID: "tc-1",
		Approved: true, Reason: "safe", ApprovedBy: "user-1",
	})
	if err != nil {
		t.Errorf("LogApproval err = %v", err)
	}
}

func TestLogAuditLogger_LogDataAccess(t *testing.T) {
	al := NewLogAuditLogger(nil)
	err := al.LogDataAccess(context.Background(), AuditDataAccessEvent{
		Timestamp: time.Now(), SessionID: "s-1", DataType: "user", Action: "read", Resource: "/api/users",
	})
	if err != nil {
		t.Errorf("LogDataAccess err = %v", err)
	}
}

func TestLogAuditLogger_QueryByType(t *testing.T) {
	al := NewLogAuditLogger(nil)
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: time.Now(), SessionID: "s-1", ToolName: "search"})
	_ = al.LogApproval(context.Background(), AuditApprovalEvent{Timestamp: time.Now(), SessionID: "s-1"})
	_ = al.LogDataAccess(context.Background(), AuditDataAccessEvent{Timestamp: time.Now(), SessionID: "s-1"})

	events, err := al.Query(context.Background(), AuditFilter{EventType: "tool_call"})
	if err != nil {
		t.Errorf("Query err = %v", err)
	}
	if len(events) != 1 {
		t.Errorf("Query returned %d events, want 1", len(events))
	}
	if events[0].Type != "tool_call" {
		t.Errorf("Type = %q, want tool_call", events[0].Type)
	}
}

func TestLogAuditLogger_QueryBySessionID(t *testing.T) {
	al := NewLogAuditLogger(nil)
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: time.Now(), SessionID: "s-1", ToolName: "search"})
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: time.Now(), SessionID: "s-2", ToolName: "read"})

	events, _ := al.Query(context.Background(), AuditFilter{SessionID: "s-1"})
	if len(events) != 1 {
		t.Errorf("Query returned %d events, want 1", len(events))
	}
}

func TestLogAuditLogger_QueryWithLimit(t *testing.T) {
	al := NewLogAuditLogger(nil)
	for i := 0; i < 5; i++ {
		_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: time.Now(), SessionID: "s-1", ToolName: "search"})
	}
	events, _ := al.Query(context.Background(), AuditFilter{EventType: "tool_call", Limit: 3})
	if len(events) != 3 {
		t.Errorf("Query returned %d events, want 3", len(events))
	}
}

func TestLogAuditLogger_QueryByTimeRange(t *testing.T) {
	al := NewLogAuditLogger(nil)
	start := time.Now()
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: start, SessionID: "s-1", ToolName: "search"})
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: start.Add(2 * time.Hour), SessionID: "s-1", ToolName: "read"})

	end := start.Add(time.Hour)
	events, _ := al.Query(context.Background(), AuditFilter{StartTime: &start, EndTime: &end})
	if len(events) != 1 {
		t.Errorf("Query returned %d events, want 1", len(events))
	}
}

func TestLogAuditLogger_QueryEmpty(t *testing.T) {
	al := NewLogAuditLogger(nil)
	events, err := al.Query(context.Background(), AuditFilter{})
	if err != nil {
		t.Errorf("Query err = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("Query returned %d events, want 0", len(events))
	}
}

func TestLogAuditLogger_WithExecLogger(t *testing.T) {
	// Verify nil-safe: LogAuditLogger with nil ExecLogger should not panic.
	al := NewLogAuditLogger(nil)
	err := al.LogToolCall(context.Background(), AuditToolCallEvent{
		Timestamp: time.Now(), SessionID: "s-1", ToolName: "search",
	})
	if err != nil {
		t.Errorf("LogToolCall with nil logger err = %v", err)
	}
}

func TestLogAuditLogger_QueryCombinedFilter(t *testing.T) {
	al := NewLogAuditLogger(nil)
	t1 := time.Now()
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: t1, SessionID: "s-1", ToolName: "search"})
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: t1.Add(time.Hour), SessionID: "s-2", ToolName: "read"})
	_ = al.LogApproval(context.Background(), AuditApprovalEvent{Timestamp: t1, SessionID: "s-1"})

	// Filter: type=tool_call + session=s-1
	events, _ := al.Query(context.Background(), AuditFilter{EventType: "tool_call", SessionID: "s-1"})
	if len(events) != 1 {
		t.Errorf("Query returned %d events, want 1", len(events))
	}
	if events[0].Type != "tool_call" {
		t.Errorf("Type = %q, want tool_call", events[0].Type)
	}
}

func TestLogAuditLogger_GenerateID(t *testing.T) {
	al := NewLogAuditLogger(nil)
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: time.Now(), SessionID: "s-1", ToolName: "a"})
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{Timestamp: time.Now(), SessionID: "s-1", ToolName: "b"})

	events, _ := al.Query(context.Background(), AuditFilter{})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID == events[1].ID {
		t.Error("IDs should be unique")
	}
}

func TestLogAuditLogger_QueryDataIntegrity(t *testing.T) {
	al := NewLogAuditLogger(nil)
	ts := time.Now()
	_ = al.LogToolCall(context.Background(), AuditToolCallEvent{
		Timestamp: ts, SessionID: "s-1", ToolName: "search",
		Arguments: map[string]any{"q": "hello"}, Result: "found", Approved: true, DecisionBy: "auto",
	})

	events, _ := al.Query(context.Background(), AuditFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	data, ok := events[0].Data.(AuditToolCallEvent)
	if !ok {
		t.Fatal("Data should be AuditToolCallEvent")
	}
	if data.ToolName != "search" {
		t.Errorf("ToolName = %q, want search", data.ToolName)
	}
	if data.SessionID != "s-1" {
		t.Errorf("SessionID = %q, want s-1", data.SessionID)
	}
}
