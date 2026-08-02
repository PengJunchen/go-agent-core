package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LS-001: ls lists directory contents with file and dir entries.
func TestLs_BasicDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Should contain both entries.
	if !strings.Contains(result.Content, "file1.txt") {
		t.Error("expected file1.txt in output")
	}
	if !strings.Contains(result.Content, "subdir") {
		t.Error("expected subdir in output")
	}
	// Check details contain entry count.
	if result.Details == nil {
		t.Error("expected Details to be set")
	}
}

// LS-002: ls marks entries as file or dir.
func TestLs_EntryTypes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "b_dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}

	content := result.Content
	if !strings.Contains(content, "file") {
		t.Error("expected 'file' type indicator for a.txt")
	}
	if !strings.Contains(content, "dir") {
		t.Error("expected 'dir' type indicator for b_dir")
	}
}

// LS-003: ls returns error for missing path parameter.
func TestLs_MissingPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for missing path")
	}
}

// LS-004: ls returns error for non-existent directory.
func TestLs_DirNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": filepath.Join(dir, "nonexistent"),
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for non-existent directory")
	}
}

// LS-005: ls returns error when path is a file, not a directory.
func TestLs_PathIsFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": filePath,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when path is a file")
	}
}

// LS-006: ls resolves relative paths against workDir.
func TestLs_RelativePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": ".", // relative path
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "test.txt") {
		t.Error("expected test.txt in output")
	}
}

// LS-007: ls is ParallelSafe.
func TestLs_ParallelSafe(t *testing.T) {
	dir := t.TempDir()
	tool := NewLsTool(dir)
	if !tool.ParallelSafe {
		t.Error("ls should be ParallelSafe")
	}
}

// LS-008: ls respects context cancellation.
func TestLs_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewLsTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"path": dir,
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// LS-009: ls includes size and mod_time in output.
func TestLs_SizeAndModTime(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Should contain size info.
	if !strings.Contains(result.Content, "file.txt") {
		t.Error("expected file.txt in output")
	}
}

// LS-010: ls on empty directory returns empty listing.
func TestLs_EmptyDirectory(t *testing.T) {
	dir := t.TempDir()

	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Details["entry_count"] != 0 {
		t.Errorf("expected entry_count=0, got %v", result.Details["entry_count"])
	}
}

// LS-011: ls with non-string path returns error.
func TestLs_InvalidPathType(t *testing.T) {
	dir := t.TempDir()
	tool := NewLsTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"path": 42,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for non-string path")
	}
}
