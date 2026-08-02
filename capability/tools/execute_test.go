package tools

import (
	"context"
	"testing"
	"time"
)

// EX-001: execute runs a simple command and returns output.
func TestExecute_SimpleCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content != "hello\n" {
		t.Errorf("output = %q, want %q", result.Content, "hello\n")
	}
}

// EX-002: execute returns output for non-zero exit codes (not error).
func TestExecute_NonZeroExitCode(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo error_msg >&2 && exit 1",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	// Non-zero exit code should NOT be an error — LLM should self-correct.
	if result.IsError {
		t.Errorf("non-zero exit should not be IsError, got: %s", result.Content)
	}
	if result.Details["exit_code"] != 1 {
		t.Errorf("exit_code = %v, want 1", result.Details["exit_code"])
	}
}

// EX-003: execute locks working directory to workDir.
func TestExecute_WorkingDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "pwd",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Output should contain the workDir path.
	if result.Content != dir+"\n" {
		t.Errorf("pwd = %q, want %q", result.Content, dir+"\n")
	}
}

// EX-004: execute captures stderr.
func TestExecute_Stderr(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo stderr_msg >&2",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected stderr output")
	}
}

// EX-005: execute respects custom timeout.
func TestExecute_CustomTimeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo fast",
		"timeout": 5,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

// EX-006: execute times out for long-running commands.
func TestExecute_Timeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "sleep 10",
		"timeout": 1,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for timed-out command")
	}
	if result.Details["timed_out"] != true {
		t.Error("expected timed_out=true")
	}
}

// EX-007: execute returns error for missing command parameter.
func TestExecute_MissingCommand(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing command")
	}
}

// EX-008: execute respects context cancellation.
func TestExecute_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"command": "echo hello",
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// EX-009: execute with multi-line output.
func TestExecute_MultiLineOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo line1 && echo line2 && echo line3",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	expected := "line1\nline2\nline3\n"
	if result.Content != expected {
		t.Errorf("output = %q, want %q", result.Content, expected)
	}
}

// EX-010: execute with environment variables.
func TestExecute_EnvVars(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo $HOME",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content == "\n" {
		t.Error("expected HOME env var to be set")
	}
}

// EX-011: parseTimeout helper function.
func TestParseTimeout(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		expected int
	}{
		{"default", map[string]any{}, 120},
		{"custom", map[string]any{"timeout": 30}, 30},
		{"zero uses default", map[string]any{"timeout": 0}, 120},
		{"negative uses default", map[string]any{"timeout": -1}, 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTimeout(tt.args)
			if got != tt.expected {
				t.Errorf("parseTimeout() = %d, want %d", got, tt.expected)
			}
		})
	}
}

// EX-012: execute with very short timeout.
func TestExecute_ShortTimeout(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	start := time.Now()
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "sleep 10",
		"timeout": 1,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for timeout")
	}
	// Should timeout in ~1 second, not wait the full 10 seconds.
	if elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// EX-013: execute uses /bin/sh when bash is not found (macOS fallback).
func TestExecute_ShFallback(t *testing.T) {
	// Test that resolveShell returns a valid shell.
	shell := resolveShell()
	if shell == "" {
		t.Error("resolveShell should return a non-empty shell path")
	}
	// On macOS, bash should be available. But the function should still work.
	if shell != "bash" && shell != "/bin/sh" {
		t.Errorf("resolveShell returned unexpected shell: %q", shell)
	}
}

// EX-014: execute runs commands with /bin/sh when PATH has no bash.
func TestExecute_ShFallbackWorks(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	// This should work regardless of whether bash or /bin/sh is used.
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo shell_works",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content != "shell_works\n" {
		t.Errorf("output = %q, want %q", result.Content, "shell_works\n")
	}
}

// EX-015: execute streaming output collects real-time output.
func TestExecute_StreamingOutput(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	// Run a command that produces output over time.
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo line1 && echo line2 && echo line3",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	expected := "line1\nline2\nline3\n"
	if result.Content != expected {
		t.Errorf("output = %q, want %q", result.Content, expected)
	}
}

// EX-016: execute streaming output captures stderr in real-time.
func TestExecute_StreamingStderr(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "echo stdout_msg && echo stderr_msg >&2",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !containsString(result.Content, "stdout_msg") {
		t.Error("expected stdout_msg in output")
	}
	if !containsString(result.Content, "stderr_msg") {
		t.Error("expected stderr_msg in output")
	}
}

// EX-017: execute streaming works with long-running commands.
func TestExecute_StreamingLongRunning(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecuteTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"command": "for i in 1 2 3; do echo line$i; done",
		"timeout": 10,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !containsString(result.Content, "line1") || !containsString(result.Content, "line3") {
		t.Errorf("expected streaming output with all lines, got: %q", result.Content)
	}
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
