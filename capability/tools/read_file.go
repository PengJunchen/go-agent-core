// Package tools provides built-in coding tools for file operations.
//
// These tools (read_file, write_file) are registered into the ToolRegistry
// and exposed to the agent loop as callable built-in capabilities.
package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// readFileMaxOutputLength is the default maximum output length for read_file
// in UTF-16 code units. Outputs exceeding this are truncated.
const readFileMaxOutputLength = 20000

// NewReadFileTool builds a read_file ToolDefinition scoped to workDir.
// The returned handler resolves relative paths against workDir for sandboxing.
func NewReadFileTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "read_file",
		Description: "Read the contents of a text file. Returns content with line numbers prefixed (cat -n style). Supports optional offset (1-based start line) and limit (max lines).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type": "string",
					"description": "Path to the file to read",
				},
				"offset": map[string]any{
					"type": "integer",
					"description": "Line number to start reading from (1-based)",
				},
				"limit": map[string]any{
					"type": "integer",
					"description": "Maximum number of lines to read",
				},
			},
			"required": []any{"file_path"},
		},
		Handler: readFileHandler(workDir),
		ParallelSafe: true,
		ValidateArgs: true,
	}
}

// readFileHandler returns a ToolHandler closure capturing workDir for sandboxing.
func readFileHandler(workDir string) registry.ToolHandler {
	resolver := NewPathResolver(workDir)
	return func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rawPath, ok := args["file_path"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: file_path",
				IsError: true,
			}, nil
		}
		filePath, ok := rawPath.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("file_path must be a string, got %T", rawPath),
				IsError: true,
			}, nil
		}
		if strings.TrimSpace(filePath) == "" {
			return &registry.ToolResult{
				Content: "file_path must not be empty",
				IsError: true,
			}, nil
		}

		// Resolve path with NFC normalization, tilde expansion, and sandbox check.
		resolved, err := resolver.Resolve(filePath)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to resolve path: %v", err),
				IsError: true,
			}, nil
		}

		// Parse optional offset (1-based).
		offset := 1
		if rawOffset, exists := args["offset"]; exists && rawOffset != nil {
			n, err := toInt(rawOffset)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("offset must be an integer: %v", err),
					IsError: true,
				}, nil
			}
			offset = n
		}
		if offset < 1 {
			offset = 1
		}

		// Parse optional limit.
		limit := 0 // 0 means unlimited
		if rawLimit, exists := args["limit"]; exists && rawLimit != nil {
			n, err := toInt(rawLimit)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("limit must be an integer: %v", err),
					IsError: true,
				}, nil
			}
			limit = n
		}

		f, err := os.Open(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("file not found: %s", resolved),
					IsError: true,
				}, nil
			}
			if os.IsPermission(err) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("permission denied: %s", resolved),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to open file %s: %v", resolved, err),
				IsError: true,
			}, nil
		}
		defer func() { _ = f.Close() }() // read-only open, close error is irrelevant

		var b strings.Builder
		scanner := bufio.NewScanner(f)
		// Allow longer lines than the default 64KB buffer.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		lineNo := 0
		emitted := 0
		for scanner.Scan() {
			lineNo++
			if lineNo < offset {
				continue
			}
			if limit > 0 && emitted >= limit {
				break
			}
			fmt.Fprintf(&b, "%6d\t%s\n", lineNo, scanner.Text())
			emitted++
		}
		if err := scanner.Err(); err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to read file %s: %v", resolved, err),
				IsError: true,
			}, nil
		}

		if emitted == 0 && lineNo == 0 {
			// Empty file: still report success with empty content.
			return &registry.ToolResult{
				Content: "",
				Details: map[string]any{
					"path": resolved,
					"lines": 0,
					"empty": true,
				},
			}, nil
		}

		output := b.String()
		truncated := TruncateContent(output, readFileMaxOutputLength)

		return &registry.ToolResult{
			Content: truncated.Content,
			Details: map[string]any{
				"path": resolved,
				"lines_read": emitted,
				"offset": offset,
				"truncated": truncated.WasTruncated,
			},
		}, nil
	}
}

// toInt converts a JSON-decoded numeric value to int.
// JSON numbers arrive as float64; native int callers are also supported.
func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != float64(int(n)) {
			return 0, fmt.Errorf("value %v is not an integer", n)
		}
		return int(n), nil
	case string:
		parsed, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("cannot parse %q as integer: %w", n, err)
		}
		return parsed, nil
	}
	return 0, fmt.Errorf("unsupported type %T", v)
}
