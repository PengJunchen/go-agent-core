package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RF-001: NewReadFileTool returns a ToolDefinition with the expected metadata.
func TestReadFile_Definition(t *testing.T) {
	tool := NewReadFileTool(t.TempDir())

	if tool.Name != "read_file" {
		t.Errorf("Name = %q, want %q", tool.Name, "read_file")
	}
	if tool.Description == "" {
		t.Error("Description should not be empty")
	}
	if tool.Handler == nil {
		t.Error("Handler should not be nil")
	}
	if !tool.ParallelSafe {
		t.Error("ParallelSafe should be true for read_file")
	}
	if !tool.ValidateArgs {
		t.Error("ValidateArgs should be true")
	}

	props, ok := tool.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters.properties missing or wrong type")
	}
	for _, key := range []string{"file_path", "offset", "limit"} {
		if _, ok := props[key]; !ok {
			t.Errorf("properties missing key %q", key)
		}
	}
	required, ok := tool.Parameters["required"].([]any)
	if !ok {
		t.Fatal("Parameters.required missing or wrong type")
	}
	if len(required) != 1 || required[0] != "file_path" {
		t.Errorf("required = %v, want [file_path]", required)
	}
}

// RF-002: Reading an existing file returns content with line-number prefixes.
func TestReadFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %s", res.Content)
	}
	if !strings.Contains(res.Content, "line one") {
		t.Errorf("content should contain 'line one', got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "line two") {
		t.Errorf("content should contain 'line two', got: %q", res.Content)
	}
	// Line numbers should be prefixed (cat -n style: 6-width right-aligned + tab).
	if !strings.Contains(res.Content, " 1\tline one") {
		t.Errorf("expected line-number prefix ' 1\\tline one', got: %q", res.Content)
	}
	if res.Details["lines_read"] != 3 {
		t.Errorf("lines_read = %v, want 3", res.Details["lines_read"])
	}
}

// RF-003: Reading a non-existent file returns IsError=true with a helpful message.
func TestReadFile_NotExist(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": filepath.Join(dir, "missing.txt"),
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for missing file")
	}
	if !strings.Contains(res.Content, "not found") {
		t.Errorf("error message should mention 'not found', got: %q", res.Content)
	}
}

// RF-004: offset starts reading from the given 1-based line number.
func TestReadFile_Offset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "offset.txt")
	content := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"offset": 3,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if strings.Contains(res.Content, "alpha") || strings.Contains(res.Content, "beta") {
		t.Errorf("content should not contain lines before offset, got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "gamma") {
		t.Errorf("content should contain 'gamma', got: %q", res.Content)
	}
	if !strings.Contains(res.Content, "delta") {
		t.Errorf("content should contain 'delta', got: %q", res.Content)
	}
	// Line numbers should reflect the original file positions.
	if !strings.Contains(res.Content, " 3\tgamma") {
		t.Errorf("expected original line number 3 for 'gamma', got: %q", res.Content)
	}
	if res.Details["lines_read"] != 3 {
		t.Errorf("lines_read = %v, want 3", res.Details["lines_read"])
	}
}

// RF-005: limit restricts the maximum number of lines returned.
func TestReadFile_Limit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "limit.txt")
	content := "one\ntwo\nthree\nfour\nfive\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"limit": 2,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "one") || !strings.Contains(res.Content, "two") {
		t.Errorf("content should contain 'one' and 'two', got: %q", res.Content)
	}
	if strings.Contains(res.Content, "three") {
		t.Errorf("content should not contain 'three' (limited to 2 lines), got: %q", res.Content)
	}
	if res.Details["lines_read"] != 2 {
		t.Errorf("lines_read = %v, want 2", res.Details["lines_read"])
	}
}

// RF-006: Combining offset and limit returns the expected window.
func TestReadFile_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "window.txt")
	content := "l1\nl2\nl3\nl4\nl5\nl6\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"offset": 2,
		"limit": 2,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "l2") || !strings.Contains(res.Content, "l3") {
		t.Errorf("content should contain 'l2' and 'l3', got: %q", res.Content)
	}
	if strings.Contains(res.Content, "l1") || strings.Contains(res.Content, "l4") {
		t.Errorf("content should not contain 'l1' or 'l4', got: %q", res.Content)
	}
	if res.Details["lines_read"] != 2 {
		t.Errorf("lines_read = %v, want 2", res.Details["lines_read"])
	}
}

// RF-007: Relative paths are resolved against workDir.
func TestReadFile_RelativePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rel.txt")
	if err := os.WriteFile(path, []byte("relative content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "rel.txt",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "relative content") {
		t.Errorf("content should contain 'relative content', got: %q", res.Content)
	}
}

// RF-008: Missing file_path parameter returns IsError=true.
func TestReadFile_MissingFilePath(t *testing.T) {
	tool := NewReadFileTool(t.TempDir())
	res, err := tool.Handler(context.Background(), map[string]any{})
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

// RF-009: Empty file_path returns IsError=true.
func TestReadFile_EmptyFilePath(t *testing.T) {
	tool := NewReadFileTool(t.TempDir())
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": "",
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for empty file_path")
	}
}

// RF-010: Non-string file_path returns IsError=true.
func TestReadFile_InvalidFilePathType(t *testing.T) {
	tool := NewReadFileTool(t.TempDir())
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": 123,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true for non-string file_path")
	}
}

// RF-011: Reading an empty file succeeds with empty content.
func TestReadFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error for empty file: %s", res.Content)
	}
	if res.Content != "" {
		t.Errorf("content should be empty, got: %q", res.Content)
	}
	if empty, _ := res.Details["empty"].(bool); !empty {
		t.Error("Details.empty should be true for empty file")
	}
}

// RF-012: Offset beyond file end returns empty content (no error).
func TestReadFile_OffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(path, []byte("only line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"offset": 100,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "" {
		t.Errorf("content should be empty when offset beyond end, got: %q", res.Content)
	}
	if res.Details["lines_read"] != 0 {
		t.Errorf("lines_read = %v, want 0", res.Details["lines_read"])
	}
}

// RF-013: Negative offset is normalized to 1.
func TestReadFile_NegativeOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neg.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"offset": -5,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "first") {
		t.Errorf("negative offset should normalize to 1 and read from start, got: %q", res.Content)
	}
}

// RF-014: Numeric args decoded as float64 (JSON style) are accepted.
func TestReadFile_Float64Args(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	content := "a\nb\nc\nd\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": path,
		"offset": float64(2),
		"limit": float64(1),
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "b") {
		t.Errorf("content should contain 'b', got: %q", res.Content)
	}
	if strings.Contains(res.Content, "c") {
		t.Errorf("content should not contain 'c' (limit 1), got: %q", res.Content)
	}
}

// RF-015: Context cancellation propagates.
func TestReadFile_ContextCanceled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ctx.txt")
	if err := os.WriteFile(path, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	tool := NewReadFileTool(dir)
	_, err := tool.Handler(ctx, map[string]any{
		"file_path": path,
	})
	if err == nil {
		t.Error("expected error from canceled context")
	}
}

// RF-016: Reading a directory returns IsError=true (not a panic).
func TestReadFile_Directory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	res, err := tool.Handler(context.Background(), map[string]any{
		"file_path": sub,
	})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if !res.IsError {
		t.Error("expected IsError=true when reading a directory")
	}
}

// RF-017: toInt helper covers supported types.
func TestReadFile_ToInt(t *testing.T) {
	cases := []struct {
		in any
		want int
		ok bool
	}{
		{int(5), 5, true},
		{int64(7), 7, true},
		{float64(3), 3, true},
		{float64(3.5), 0, false},
		{"9", 9, true},
		{"abc", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, err := toInt(c.in)
		if c.ok {
			if err != nil {
				t.Errorf("toInt(%v) unexpected error: %v", c.in, err)
				continue
			}
			if got != c.want {
				t.Errorf("toInt(%v) = %d, want %d", c.in, got, c.want)
			}
		} else {
			if err == nil {
				t.Errorf("toInt(%v) expected error, got %d", c.in, got)
			}
		}
	}
}
