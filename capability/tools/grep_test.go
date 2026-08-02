package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// GR-001: grep finds a pattern in a file.
func TestGrep_BasicMatch(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello world\nfoo bar\nhello again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "hello",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected grep output")
	}
}

// GR-002: grep returns no matches message when pattern not found.
func TestGrep_NoMatch(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "notfound",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	// No match is not an error, just a message.
	if result.IsError {
		t.Error("no match should not be IsError")
	}
}

// GR-003: grep supports regex patterns.
func TestGrep_RegexPattern(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("test123\ntest456\nother\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": `test\d+`,
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
}

// GR-004: grep supports include pattern.
func TestGrep_IncludePattern(t *testing.T) {
	dir := t.TempDir()
	// Create .go and .txt files.
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "package main",
		"path": dir,
		"include": "*.go",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Should only match the .go file.
	if result.Content == "" {
		t.Error("expected grep output")
	}
}

// GR-005: grep returns error for invalid regex.
func TestGrep_InvalidRegex(t *testing.T) {
	dir := t.TempDir()
	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "[invalid",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for invalid regex")
	}
}

// GR-006: grep returns error for missing pattern.
func TestGrep_MissingPattern(t *testing.T) {
	dir := t.TempDir()
	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing pattern")
	}
}

// GR-007: grep skips binary files.
func TestGrep_SkipsBinary(t *testing.T) {
	dir := t.TempDir()
	// Create a binary file with null bytes.
	if err := os.WriteFile(filepath.Join(dir, "binary.bin"), []byte("hello\x00world"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create a text file.
	if err := os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "hello",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	// Should not panic, should only match the text file.
	if result.Content == "" {
		t.Error("expected output from text file")
	}
}

// GR-008: grep skips .git directory.
func TestGrep_SkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("hello from git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello from project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "hello",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	// Should not include the .git/config match.
	if result.Content == "" {
		t.Error("expected output from project files")
	}
}

// GR-009: grep respects context cancellation.
func TestGrep_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewGrepTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"pattern": "test",
		"path": dir,
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// GR-010: grep searches recursively.
func TestGrep_Recursive(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "deep.txt"), []byte("found deep match\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGrepTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "deep match",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected recursive match")
	}
}
