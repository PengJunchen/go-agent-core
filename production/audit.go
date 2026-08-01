package production

import (
	"context"
	"fmt"
	"sync"

	"github.com/pengjunchen/go-agent-core/memory/log"
)

// LogAuditLogger records audit events via ExecLogger and supports querying.
// It maintains an in-memory event store for the Query method while also
// writing structured records to the ExecLogger when one is configured.
type LogAuditLogger struct {
	mu sync.RWMutex
	logger log.ExecLogger
	events []AuditEvent
	nextID int
}

// NewLogAuditLogger creates a LogAuditLogger that writes to the given ExecLogger.
// If logger is nil, audit events are only stored in memory (no log output).
func NewLogAuditLogger(logger log.ExecLogger) *LogAuditLogger {
	return &LogAuditLogger{
		logger: logger,
		events: make([]AuditEvent, 0),
	}
}

// LogToolCall records a tool call audit event.
func (l *LogAuditLogger) LogToolCall(ctx context.Context, event AuditToolCallEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	auditEvent := AuditEvent{
		ID: l.generateID(),
		Timestamp: event.Timestamp,
		Type: "tool_call",
		Data: event,
	}
	l.events = append(l.events, auditEvent)

	if l.logger != nil {
		rec := log.NewItemRecord("audit_tool_call", event.SessionID, "")
		rec.ToolName = event.ToolName
		rec.Input = event.Arguments
		rec.Output = event.Result
		_ = l.logger.LogItem(ctx, rec) // audit logging best-effort, error does not fail the call
	}
	return nil
}

// LogApproval records an approval decision.
func (l *LogAuditLogger) LogApproval(ctx context.Context, event AuditApprovalEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	auditEvent := AuditEvent{
		ID: l.generateID(),
		Timestamp: event.Timestamp,
		Type: "approval",
		Data: event,
	}
	l.events = append(l.events, auditEvent)

	if l.logger != nil {
		rec := log.NewItemRecord("audit_approval", event.SessionID, "")
		rec.Input = map[string]any{
			"tool_call_id": event.ToolCallID,
			"approved": event.Approved,
			"reason": event.Reason,
			"approved_by": event.ApprovedBy,
		}
		_ = l.logger.LogItem(ctx, rec) // audit logging best-effort
	}
	return nil
}

// LogDataAccess records a data access event.
func (l *LogAuditLogger) LogDataAccess(ctx context.Context, event AuditDataAccessEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	auditEvent := AuditEvent{
		ID: l.generateID(),
		Timestamp: event.Timestamp,
		Type: "data_access",
		Data: event,
	}
	l.events = append(l.events, auditEvent)

	if l.logger != nil {
		rec := log.NewItemRecord("audit_data_access", event.SessionID, "")
		rec.Input = map[string]any{
			"data_type": event.DataType,
			"action": event.Action,
			"resource": event.Resource,
		}
		_ = l.logger.LogItem(ctx, rec) // audit logging best-effort
	}
	return nil
}

// Query queries audit events matching the given filter.
func (l *LogAuditLogger) Query(_ context.Context, filter AuditFilter) ([]AuditEvent, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var result []AuditEvent
	for _, evt := range l.events {
		if filter.EventType != "" && evt.Type != filter.EventType {
			continue
		}
		if filter.SessionID != "" {
			// Extract SessionID from Data
			var eventSessionID string
			switch data := evt.Data.(type) {
			case AuditToolCallEvent:
				eventSessionID = data.SessionID
			case AuditApprovalEvent:
				eventSessionID = data.SessionID
			case AuditDataAccessEvent:
				eventSessionID = data.SessionID
			default:
				continue // skip events with unknown types when filtering by SessionID
			}
			if eventSessionID != filter.SessionID {
				continue
			}
		}
		if filter.StartTime != nil && evt.Timestamp.Before(*filter.StartTime) {
			continue
		}
		if filter.EndTime != nil && evt.Timestamp.After(*filter.EndTime) {
			continue
		}
		result = append(result, evt)
		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}
	return result, nil
}

func (l *LogAuditLogger) generateID() string {
	l.nextID++
	return fmt.Sprintf("audit-%d", l.nextID)
}
