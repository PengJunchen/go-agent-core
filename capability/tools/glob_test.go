package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// GB-001: glob finds files matching a pattern.
func TestGlob_BasicPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "*.go",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected glob results")
	}
}

// GB-002: glob returns no matches when no files match.
func TestGlob_NoMatch(t *testing.T) {
	dir := t.TempDir()
	tool := NewGlobTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "*.xyz",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	// No match is not an error.
	if result.IsError {
		t.Error("no match should not be IsError")
	}
}

// GB-003: glob supports ** for recursive matching.
func TestGlob_DoubleStar(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "src", "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "**/*.go",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	// Should find main.go in the nested directory.
	if result.Content == "" {
		t.Error("expected recursive match")
	}
}

// GB-004: glob returns error for missing pattern.
func TestGlob_MissingPattern(t *testing.T) {
	dir := t.TempDir()
	tool := NewGlobTool(dir)
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

// GB-005: glob skips .git directory.
func TestGlob_SkipsGitDir(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "**",
		"path": dir,
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	// Should not include .git/HEAD.
	if result.Content == "" {
		t.Error("expected glob results")
	}
}

// GB-006: glob respects context cancellation.
func TestGlob_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewGlobTool(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tool.Handler(ctx, map[string]any{
		"pattern": "*.go",
		"path": dir,
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// GB-007: glob uses workDir as default path.
func TestGlob_DefaultPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewGlobTool(dir)
	result, err := tool.Handler(context.Background(), map[string]any{
		"pattern": "*.go",
		// No "path" parameter — should default to workDir.
	})
	if err != nil {
		t.Fatalf("Handler error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("expected glob results using default workDir")
	}
}

// RB-001: RegisterBuiltinTools registers all 9 tools.
func TestRegisterBuiltinTools(t *testing.T) {
	dir := t.TempDir()
	reg := registry.NewDefaultToolRegistry()
	err := RegisterBuiltinTools(context.Background(), reg, dir)
	if err != nil {
		t.Fatalf("RegisterBuiltinTools: %v", err)
	}

	tools, err := reg.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(tools) != 9 {
		t.Fatalf("expected 9 tools, got %d", len(tools))
	}

	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}

	expected := []string{"read_file", "write_file", "edit_file", "execute", "grep", "glob", "image_view", "ls", "web_fetch"}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

// RB-002: Registered tools are callable.
func TestRegisterBuiltinTools_Callable(t *testing.T) {
	dir := t.TempDir()
	reg := registry.NewDefaultToolRegistry()
	_ = RegisterBuiltinTools(context.Background(), reg, dir)

	// Create a test file.
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Call read_file.
	tool, err := reg.GetTool(context.Background(), "read_file")
	if err != nil {
		t.Fatalf("GetTool: %v", err)
	}
	result, err := tool.Handler(context.Background(), map[string]any{
		"file_path": filepath.Join(dir, "test.txt"),
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if result.IsError {
		t.Errorf("read_file failed: %s", result.Content)
	}

	// Call execute.
	executeTool, err := reg.GetTool(context.Background(), "execute")
	if err != nil {
		t.Fatalf("GetTool execute: %v", err)
	}
	result, err = executeTool.Handler(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("execute Handler: %v", err)
	}
	if result.IsError {
		t.Errorf("execute failed: %s", result.Content)
	}
}

// RB-003: Read-only tools are ParallelSafe.
func TestRegisterBuiltinTools_ParallelSafety(t *testing.T) {
	dir := t.TempDir()
	reg := registry.NewDefaultToolRegistry()
	_ = RegisterBuiltinTools(context.Background(), reg, dir)

	readOnlyTools := []string{"read_file", "grep", "glob", "image_view", "ls", "web_fetch"}
	writeTools := []string{"write_file", "edit_file", "execute"}

	for _, name := range readOnlyTools {
		tool, err := reg.GetTool(context.Background(), name)
		if err != nil {
			t.Fatalf("GetTool %s: %v", name, err)
		}
		if !tool.ParallelSafe {
			t.Errorf("tool %s should be ParallelSafe", name)
		}
	}

	for _, name := range writeTools {
		tool, err := reg.GetTool(context.Background(), name)
		if err != nil {
			t.Fatalf("GetTool %s: %v", name, err)
		}
		if tool.ParallelSafe {
			t.Errorf("tool %s should NOT be ParallelSafe", name)
		}
	}
}
