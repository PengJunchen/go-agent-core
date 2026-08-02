package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// EF-001: edit_file replaces a single occurrence.
func TestEditFile_SingleReplace(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello world\nfoo bar\nhello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": fp,
		"old_str": "foo bar",
		"new_str": "baz qux",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	expected := "hello world\nbaz qux\nhello world\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}

// EF-002: edit_file returns error when old_str not found.
func TestEditFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": fp,
		"old_str": "not found",
		"new_str": "replacement",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true when old_str not found")
	}
	if result.Content == "" {
		t.Error("expected context message when old_str not found")
	}
}

// EF-003: edit_file returns error for multiple matches without replace_all.
func TestEditFile_MultipleMatches(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello\nhello\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": fp,
		"old_str": "hello",
		"new_str": "world",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for multiple matches without replace_all")
	}
}

// EF-004: edit_file replaces all with replace_all=true.
func TestEditFile_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello\nhello\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": fp,
		"old_str": "hello",
		"new_str": "world",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	expected := "world\nworld\nworld\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}

// EF-005: edit_file returns error for file not found.
func TestEditFile_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": filepath.Join(dir, "nonexistent.txt"),
		"old_str": "a",
		"new_str": "b",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for nonexistent file")
	}
}

// EF-006: edit_file returns error for missing old_str parameter.
func TestEditFile_MissingOldStr(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "test.txt",
		"new_str": "b",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing old_str")
	}
}

// EF-007: edit_file returns error for missing new_str parameter.
func TestEditFile_MissingNewStr(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "test.txt",
		"old_str": "a",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing new_str")
	}
}

// EF-008: edit_file works with relative paths.
func TestEditFile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "test.txt",
		"old_str": "hello",
		"new_str": "goodbye",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	if string(data) != "goodbye world\n" {
		t.Errorf("file content = %q, want %q", string(data), "goodbye world\n")
	}
}

// EF-009: edit_file provides context for partial matches.
func TestEditFile_PartialMatchContext(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("line one\nline two\nline three\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	// Search for a string that partially matches "line two" but is not exact.
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": fp,
		"old_str": "line too", // close to "line two" but not exact
		"new_str": "replacement",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	// Should contain line numbers and context.
	if result.Content == "" {
		t.Error("expected context message with line numbers")
	}
}

// EF-010: edit_file respects context cancellation.
func TestEditFile_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"file_path": "test.txt",
		"old_str": "a",
		"new_str": "b",
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// EF-011: edit_file returns error for missing file_path.
func TestEditFile_MissingFilePath(t *testing.T) {
	dir := t.TempDir()
	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"old_str": "a",
		"new_str": "b",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing file_path")
	}
}

// EF-012: edit_file multiline replacement.
func TestEditFile_MultilineReplace(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(fp, []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": fp,
		"old_str": "\tfmt.Println(\"hello\")",
		"new_str": "\tfmt.Println(\"world\")",
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}

	data, _ := os.ReadFile(fp)
	expected := "func main() {\n\tfmt.Println(\"world\")\n}\n"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}
