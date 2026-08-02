package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WF-001: NewWriteFileTool returns a ToolDefinition with the expected metadata.
func TestWriteFile_Definition(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir())

	if tool.Name != "write_file" {
		t.Errorf("Name = %q, want %q", tool.Name, "write_file")
	}
	if tool.Description == "" {
		t.Error("Description should not be empty")
	}
	if tool.Handler == nil {
		t.Error("Handler should not be nil")
	}
	if tool.ParallelSafe {
		t.Error("ParallelSafe should be false for write_file")
	}
	if !tool.ValidateArgs {
		t.Error("ValidateArgs should be true")
	}

	props, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters.properties missing or wrong type")
	}
	for _, key := range []string{"file_path", "content"} {
		if _, ok := props[key]; !ok {
			t.Errorf("properties missing key %q", key)
		}
	}
	required, ok := tool.Parameters["required"].([]any)
	if !ok {
		t.Fatal("Parameters.required missing or wrong type")
	}
	if len(required) != 2 {
		t.Fatalf("required len = %d, want 2", len(required))
	}
}

// WF-002: Writing a new file succeeds and stores the content.
func TestWriteFile_NewFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	path := filepath.Join(dir, "new.txt")

	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"content": "hello world\n",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back file: %v", err)
	}
	if string(data) != "hello world\n" {
		t.Errorf("file content = %q, want %q", string(data), "hello world\n")
	}
	if res.Details["bytes_written"] != len("hello world\n") {
		t.Errorf("bytes_written = %v, want %d", res.Details["bytes_written"], len("hello world\n"))
	}
}

// WF-003: Parent directories are created automatically.
func TestWriteFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	path := filepath.Join(dir, "sub", "deep", "nested", "file.txt")

	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"content": "nested content",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back nested file: %v", err)
	}
	if string(data) != "nested content" {
		t.Errorf("file content = %q, want %q", string(data), "nested content")
	}
}

// WF-004: Existing files are overwritten.
func TestWriteFile_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overwrite.txt")
	if err := os.WriteFile(path, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewWriteFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"content": "new content",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back file: %v", err)
	}
	if string(data) != "new content" {
		t.Errorf("file content = %q, want %q", string(data), "new content")
	}
}

// WF-005: Relative paths are resolved against workDir.
func TestWriteFile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)

	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "rel.txt",
		"content": "relative",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	data, err := os.ReadFile(filepath.Join(dir, "rel.txt"))
	if err != nil {
		t.Fatalf("reading back file: %v", err)
	}
	if string(data) != "relative" {
		t.Errorf("file content = %q, want %q", string(data), "relative")
	}
}

// WF-006: Missing file_path returns IsError=true.
func TestWriteFile_MissingFilePath(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir())
	res, err := tool.Handler(context.Background(), map[string]any{
		"content": "data",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for missing file_path")
	}
	if !strings.Contains(res.Content, "file_path") {
		t.Errorf("error should mention file_path, got: %q", res.Content)
	}
}

// WF-007: Missing content returns IsError=true.
func TestWriteFile_MissingContent(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": filepath.Join(dir, "x.txt"),
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for missing content")
	}
	if !strings.Contains(res.Content, "content") {
		t.Errorf("error should mention content, got: %q", res.Content)
	}
}

// WF-008: Empty file_path returns IsError=true.
func TestWriteFile_EmptyFilePath(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir())
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "",
		"content": "data",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for empty file_path")
	}
}

// WF-009: Non-string file_path returns IsError=true.
func TestWriteFile_InvalidFilePathType(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir())
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": 42,
		"content": "data",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for non-string file_path")
	}
}

// WF-010: Non-string content returns IsError=true.
func TestWriteFile_InvalidContentType(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": filepath.Join(dir, "x.txt"),
		"content": 123,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for non-string content")
	}
}

// WF-011: Empty content is written successfully.
func TestWriteFile_EmptyContent(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	path := filepath.Join(dir, "empty.txt")

	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"content": "",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back file: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("file should be empty, got %d bytes", len(data))
	}
	if res.Details["bytes_written"] != 0 {
		t.Errorf("bytes_written = %v, want 0", res.Details["bytes_written"])
	}
}

// WF-012: Success message contains the resolved file path.
func TestWriteFile_SuccessMessageContainsPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)
	path := filepath.Join(dir, "msg.txt")

	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"content": "x",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, path) {
		t.Errorf("success message should contain path %q, got: %q", path, res.Content)
	}
}

// WF-013: Context cancellation propagates.
func TestWriteFile_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := tool.Handler(ctx, map[string]any{
		"file_path": filepath.Join(dir, "ctx.txt"),
		"content": "data",
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// WF-014: Writing a path whose parent is an existing file returns IsError=true.
func TestWriteFile_ParentIsFile(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file that will act as a blocking parent.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Attempt to write into "blocker/child.txt" — MkdirAll should fail.
	tool := NewWriteFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": filepath.Join(blocker, "child.txt"),
		"content": "data",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true when parent path is a file")
	}
}

// WF-015: Round-trip write then read returns the same content.
func TestWriteFile_RoundTripWithReadFile(t *testing.T) {
	dir := t.TempDir()
	writeTool := NewWriteFileTool(dir)
	readTool := NewReadFileTool(dir)
	path := "roundtrip.txt"
	content := "round\ntrip\ntest\n"

	wres, err := writeTool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"content": content,
	})
	if err != nil {
		t.Fatalf("write Handler returned error: %v", err)
	}
	if wres.IsError {
		t.Fatalf("write unexpected error: %s", wres.Content)
	}

	rres, err := readTool.Handler(context.Background(), map[string]any{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("read Handler returned error: %v", err)
	}
	if rres.IsError {
		t.Fatalf("read unexpected error: %s", rres.Content)
	}
	if !strings.Contains(rres.Content, "round") {
		t.Errorf("read content should contain 'round', got: %q", rres.Content)
	}
	if !strings.Contains(rres.Content, "trip") {
		t.Errorf("read content should contain 'trip', got: %q", rres.Content)
	}
}
