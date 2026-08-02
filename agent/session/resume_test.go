package session

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/loop"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/memory/log"
)

// ─── AC-1: Resume session from JSONL log files ──────────────────

// AC-1: ResumeSession loads session history from JSONL log files and
// reconstructs the conversation context.
func TestResumeSession_FromJSONLLogs(t *testing.T) {
	storeDir := t.TempDir()
	sessionID := "test-resume-session"

	// Use a real JSONLExecLogger to write entries.
	logger, err := log.NewJSONLExecLogger(storeDir, log.DefaultLogConfig())
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	ctx := context.Background()

	// Write a turn_start record.
	turnStart := log.NewTurnRecord("turn_start", sessionID, "turn-1", "running")
	turnStart.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogTurn(ctx, turnStart); err != nil {
		t.Fatalf("LogTurn turn_start: %v", err)
	}

	// Write an llm_call item record.
	llmItem := log.NewItemRecord("llm_call", sessionID, "turn-1")
	llmItem.Model = "gpt-4o"
	llmItem.Output = "Hello! How can I help you?"
	llmItem.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogItem(ctx, llmItem); err != nil {
		t.Fatalf("LogItem llm_call: %v", err)
	}

	// Write a tool_call item record.
	toolItem := log.NewItemRecord("tool_call", sessionID, "turn-1")
	toolItem.ToolName = "execute"
	toolItem.Input = map[string]any{"command": "echo hello"}
	toolItem.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogItem(ctx, toolItem); err != nil {
		t.Fatalf("LogItem tool_call: %v", err)
	}

	// Write a tool_result item record.
	toolResult := log.NewItemRecord("tool_result", sessionID, "turn-1")
	toolResult.ToolName = "execute"
	toolResult.Output = "hello"
	toolResult.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogItem(ctx, toolResult); err != nil {
		t.Fatalf("LogItem tool_result: %v", err)
	}

	// Write a turn_end record.
	turnEnd := log.NewTurnRecord("turn_end", sessionID, "turn-1", "completed")
	turnEnd.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogTurn(ctx, turnEnd); err != nil {
		t.Fatalf("LogTurn turn_end: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close logger: %v", err)
	}

	// Now resume the session.
	resumed, err := ResumeSession(ctx, sessionID, storeDir)
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if resumed.SessionID != sessionID {
		t.Errorf("SessionID = %q, want %q", resumed.SessionID, sessionID)
	}
	if resumed.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1", resumed.TurnCount)
	}
	if len(resumed.Messages) == 0 {
		t.Fatal("expected non-empty Messages")
	}

	// Verify the assistant message was reconstructed.
	foundAssistant := false
	foundToolResult := false
	for _, msg := range resumed.Messages {
		if msg.Role == "assistant" && msg.Content == "Hello! How can I help you?" {
			foundAssistant = true
		}
		if msg.Role == "tool" && msg.ToolName == "execute" {
			foundToolResult = true
		}
	}
	if !foundAssistant {
		t.Error("expected assistant message with content 'Hello! How can I help you?'")
	}
	if !foundToolResult {
		t.Error("expected tool result message")
	}

	if resumed.ModelUsed != "gpt-4o" {
		t.Errorf("ModelUsed = %q, want 'gpt-4o'", resumed.ModelUsed)
	}
}

// AC-1b: ResumeSession with event-only logs (no item records) reconstructs
// messages from text_delta events.
func TestResumeSession_FromEventLogs(t *testing.T) {
	storeDir := t.TempDir()
	sessionID := "event-only-session"

	logger, err := log.NewJSONLExecLogger(storeDir, log.DefaultLogConfig())
	if err != nil {
		t.Fatalf("NewJSONLExecLogger: %v", err)
	}

	ctx := context.Background()

	// Write text_delta events.
	delta1 := log.NewEventRecord("text_delta", sessionID, "turn-1")
	delta1.Content = "Hello "
	delta1.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogEvent(ctx, delta1); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	delta2 := log.NewEventRecord("text_delta", sessionID, "turn-1")
	delta2.Content = "world!"
	delta2.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogEvent(ctx, delta2); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	doneEvt := log.NewEventRecord("done", sessionID, "turn-1")
	doneEvt.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	if err := logger.LogEvent(ctx, doneEvt); err != nil {
		t.Fatalf("LogEvent: %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resumed, err := ResumeSession(ctx, sessionID, storeDir)
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}

	if len(resumed.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(resumed.Messages))
	}
	if resumed.Messages[0].Role != "assistant" {
		t.Errorf("role = %q, want 'assistant'", resumed.Messages[0].Role)
	}
	if resumed.Messages[0].Content != "Hello world!" {
		t.Errorf("content = %q, want 'Hello world!'", resumed.Messages[0].Content)
	}
}

// AC-1c: ResumeSession returns error for non-existent directory.
func TestResumeSession_NonExistentDir(t *testing.T) {
	_, err := ResumeSession(context.Background(), "s1", "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

// AC-1d: ResumeSession returns error for empty session ID.
func TestResumeSession_EmptySessionID(t *testing.T) {
	dir := t.TempDir()
	_, err := ResumeSession(context.Background(), "", dir)
	if err == nil {
		t.Error("expected error for empty session ID")
	}
}

// AC-1e: ResumeSession on empty log dir returns empty but non-nil result.
func TestResumeSession_EmptyLogDir(t *testing.T) {
	dir := t.TempDir()
	resumed, err := ResumeSession(context.Background(), "nonexistent-session", dir)
	if err != nil {
		t.Fatalf("ResumeSession on empty dir: %v", err)
	}
	if resumed == nil {
		t.Fatal("expected non-nil ResumedSession")
	}
	if len(resumed.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(resumed.Messages))
	}
}

// AC-1f: ResumedSession.LoadMessagesIntoContext injects messages into CM.
func TestResumedSession_LoadMessagesIntoContext(t *testing.T) {
	cm := NewDefaultContextManager()
	ctx := context.Background()

	resumed := &ResumedSession{
		SessionID: "test",
		Messages: []ctxpkg.TurnItem{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi there"},
		},
	}

	if err := resumed.LoadMessagesIntoContext(ctx, cm); err != nil {
		t.Fatalf("LoadMessagesIntoContext: %v", err)
	}

	items, err := cm.GetMessages(ctx, nil)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items in context, got %d", len(items))
	}
}

// ─── AC-2: /models command lists available models ───────────────

// AC-2: KnownModels returns a non-empty list with model info.
func TestKnownModels_ReturnsModels(t *testing.T) {
	models := KnownModels()
	if len(models) == 0 {
		t.Fatal("expected non-empty model list")
	}

	// Verify each model has required fields.
	for _, m := range models {
		if m.Name == "" {
			t.Error("model has empty Name")
		}
		if m.Provider == "" {
			t.Errorf("model %q has empty Provider", m.Name)
		}
		if m.ContextWindow <= 0 {
			t.Errorf("model %q has non-positive ContextWindow", m.Name)
		}
	}
}

// AC-2b: FormatModels produces output containing model names.
func TestFormatModels_ContainsModelNames(t *testing.T) {
	models := KnownModels()
	output := FormatModels(models)

	if !strings.Contains(output, "gpt-4o") {
		t.Error("expected 'gpt-4o' in models output")
	}
	if !strings.Contains(output, "claude-sonnet-4-20250514") {
		t.Error("expected 'claude-sonnet-4-20250514' in models output")
	}
	if !strings.Contains(output, "gemini-2.5-pro") {
		t.Error("expected 'gemini-2.5-pro' in models output")
	}
	// Should contain pricing headers.
	if !strings.Contains(output, "IN $/1M") {
		t.Error("expected pricing header in models output")
	}
}

// AC-2c: ExecuteSlashCommand /models returns formatted model list.
func TestExecuteSlashCommand_Models(t *testing.T) {
	sess := buildTestSession(t)
	defer sess.Close()

	cmd := &SlashCommand{Type: SlashModels, Raw: "/models"}
	output, shouldExit, err := ExecuteSlashCommand(context.Background(), sess, cmd)
	if err != nil {
		t.Fatalf("ExecuteSlashCommand: %v", err)
	}
	if shouldExit {
		t.Error("shouldExit should be false for /models")
	}
	if !strings.Contains(output, "gpt-4o") {
		t.Error("expected model names in output")
	}
}

// ─── AC-3: /help command shows help ─────────────────────────────

// AC-3: HelpText contains all command descriptions.
func TestHelpText_ContainsCommands(t *testing.T) {
	help := HelpText()

	expectedCommands := []string{"/help", "/models", "/compact", "/clear", "/quit", ":quit"}
	for _, cmd := range expectedCommands {
		if !strings.Contains(help, cmd) {
			t.Errorf("expected %q in help text", cmd)
		}
	}
}

// AC-3b: ExecuteSlashCommand /help returns help text.
func TestExecuteSlashCommand_Help(t *testing.T) {
	sess := buildTestSession(t)
	defer sess.Close()

	cmd := &SlashCommand{Type: SlashHelp, Raw: "/help"}
	output, shouldExit, err := ExecuteSlashCommand(context.Background(), sess, cmd)
	if err != nil {
		t.Fatalf("ExecuteSlashCommand: %v", err)
	}
	if shouldExit {
		t.Error("shouldExit should be false for /help")
	}
	if !strings.Contains(output, "Available commands") {
		t.Error("expected 'Available commands' in help output")
	}
}

// ─── AC-4: /compact command triggers compaction ─────────────────

// AC-4: ExecuteSlashCommand /compact compacts the context.
func TestExecuteSlashCommand_Compact(t *testing.T) {
	sess := buildTestSession(t)
	defer sess.Close()

	ctx := context.Background()

	// Add some items to the context.
	cm := sess.ContextManager()
	for i := 0; i < 10; i++ {
		_ = cm.RecordItem(ctx, ctxpkg.TurnItem{ // 测试夹具，失败由后续断言暴露
			Role: "user",
			Content: strings.Repeat("some content ", 100),
		})
	}

	cmd := &SlashCommand{Type: SlashCompact, Raw: "/compact"}
	output, shouldExit, err := ExecuteSlashCommand(ctx, sess, cmd)
	if err != nil {
		t.Fatalf("ExecuteSlashCommand /compact: %v", err)
	}
	if shouldExit {
		t.Error("shouldExit should be false for /compact")
	}
	if !strings.Contains(output, "compacted") {
		t.Errorf("expected 'compacted' in output, got: %s", output)
	}
}

// ─── AC-5: /clear command clears context ────────────────────────

// AC-5: ExecuteSlashCommand /clear clears the context manager.
func TestExecuteSlashCommand_Clear(t *testing.T) {
	sess := buildTestSession(t)
	defer sess.Close()

	ctx := context.Background()

	// Add items to the context.
	cm := sess.ContextManager()
	_ = cm.RecordItem(ctx, ctxpkg.TurnItem{Role: "user", Content: "hello"}) // 测试夹具，失败由后续断言暴露
	_ = cm.RecordItem(ctx, ctxpkg.TurnItem{Role: "assistant", Content: "hi"}) // 测试夹具，失败由后续断言暴露

	// Verify items exist.
	items, _ := cm.GetMessages(ctx, nil) // 测试夹具，失败由后续断言暴露
	if len(items) != 2 {
		t.Fatalf("expected 2 items before clear, got %d", len(items))
	}

	cmd := &SlashCommand{Type: SlashClear, Raw: "/clear"}
	output, shouldExit, err := ExecuteSlashCommand(ctx, sess, cmd)
	if err != nil {
		t.Fatalf("ExecuteSlashCommand /clear: %v", err)
	}
	if shouldExit {
		t.Error("shouldExit should be false for /clear")
	}
	if !strings.Contains(output, "cleared") {
		t.Errorf("expected 'cleared' in output, got: %s", output)
	}

	// Verify context is now empty.
	itemsAfter, _ := cm.GetMessages(ctx, nil) // 测试夹具，失败由后续断言暴露
	if len(itemsAfter) != 0 {
		t.Errorf("expected 0 items after clear, got %d", len(itemsAfter))
	}
}

// ─── Slash Command Parsing ──────────────────────────────────────

// TestParseSlashCommand verifies slash command and legacy command parsing.
func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		input string
		wantType SlashCommandType
		wantOk bool
	}{
		{"/help", SlashHelp, true},
		{"/h", SlashHelp, true},
		{"/models", SlashModels, true},
		{"/m", SlashModels, true},
		{"/compact", SlashCompact, true},
		{"/clear", SlashClear, true},
		{"/quit", SlashQuit, true},
		{"/q", SlashQuit, true},
		{":quit", SlashQuit, true},
		{":q", SlashQuit, true},
		{"/unknown", SlashUnknown, true},
		{"hello world", SlashUnknown, false},
		{"", SlashUnknown, false},
		{" /help ", SlashHelp, true}, // trimmed
	}

	for _, tt := range tests {
		cmd, ok := ParseSlashCommand(tt.input)
		if ok != tt.wantOk {
			t.Errorf("ParseSlashCommand(%q): ok = %v, want %v", tt.input, ok, tt.wantOk)
			continue
		}
		if !ok {
			continue
		}
		if cmd.Type != tt.wantType {
			t.Errorf("ParseSlashCommand(%q): Type = %q, want %q", tt.input, cmd.Type, tt.wantType)
		}
	}
}

// TestExecuteSlashCommand_Quit verifies /quit returns shouldExit=true.
func TestExecuteSlashCommand_Quit(t *testing.T) {
	sess := buildTestSession(t)
	defer sess.Close()

	cmd := &SlashCommand{Type: SlashQuit, Raw: "/quit"}
	_, shouldExit, err := ExecuteSlashCommand(context.Background(), sess, cmd)
	if err != nil {
		t.Fatalf("ExecuteSlashCommand: %v", err)
	}
	if !shouldExit {
		t.Error("shouldExit should be true for /quit")
	}
}

// TestExecuteSlashCommand_Unknown verifies unknown commands produce a message.
func TestExecuteSlashCommand_Unknown(t *testing.T) {
	sess := buildTestSession(t)
	defer sess.Close()

	cmd := &SlashCommand{Type: SlashUnknown, Raw: "/foobar"}
	output, shouldExit, err := ExecuteSlashCommand(context.Background(), sess, cmd)
	if err != nil {
		t.Fatalf("ExecuteSlashCommand: %v", err)
	}
	if shouldExit {
		t.Error("shouldExit should be false for unknown command")
	}
	if !strings.Contains(output, "Unknown command") {
		t.Error("expected 'Unknown command' in output")
	}
}

// ─── ListResumableSessions ──────────────────────────────────────

// TestListResumableSessions verifies session listing from log directory.
func TestListResumableSessions(t *testing.T) {
	storeDir := t.TempDir()

	// Create runs/ directory with some session files.
	runsDir := storeDir + "/runs"
	_ = WriteTestLogEntry(runsDir+"/session-1.jsonl", map[string]any{"ts": "2026-01-01T00:00:00Z"}) // 测试夹具，失败由后续断言暴露
	_ = WriteTestLogEntry(runsDir+"/session-2.jsonl", map[string]any{"ts": "2026-01-02T00:00:00Z"}) // 测试夹具，失败由后续断言暴露

	ids, err := ListResumableSessions(storeDir)
	if err != nil {
		t.Fatalf("ListResumableSessions: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(ids))
	}

	// Verify both IDs are present.
	found := make(map[string]bool)
	for _, id := range ids {
		found[id] = true
	}
	if !found["session-1"] || !found["session-2"] {
		t.Errorf("expected session-1 and session-2, got %v", ids)
	}
}

// TestListResumableSessions_EmptyDir verifies empty directory returns empty list.
func TestListResumableSessions_EmptyDir(t *testing.T) {
	storeDir := t.TempDir()
	ids, err := ListResumableSessions(storeDir)
	if err != nil {
		t.Fatalf("ListResumableSessions: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(ids))
	}
}

// ─── Helpers ────────────────────────────────────────────────────

// buildTestSession creates a minimal Session for slash command testing.
func buildTestSession(t *testing.T) *Session {
	t.Helper()
	sess, err := NewBuilder().
		WithProvider(newMockProvider(textResponse("ok"))).
		WithContextManager(NewDefaultContextManager()).
		WithToolRegistry(NewDefaultToolRegistry()).
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return sess
}

// Ensure the test compiles with the loop package import (used in other tests).
var _ = loop.AgentInput{}
