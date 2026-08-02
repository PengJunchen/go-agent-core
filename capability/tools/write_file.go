package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// NewWriteFileTool builds a write_file ToolDefinition scoped to workDir.
// The returned handler resolves relative paths against workDir for sandboxing,
// creates parent directories automatically, and overwrites existing files.
func NewWriteFileTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "write_file",
		Description: "Write content to a file. Creates parent directories automatically and overwrites existing files.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type": "string",
					"description": "Path to the file to write",
				},
				"content": map[string]any{
					"type": "string",
					"description": "Content to write to the file",
				},
			},
			"required": []any{"file_path", "content"},
		},
		Handler: writeFileHandler(workDir),
		ParallelSafe: false,
		ValidateArgs: true,
	}
}

// writeFileHandler returns a ToolHandler closure capturing workDir for sandboxing.
func writeFileHandler(workDir string) registry.ToolHandler {
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

		rawContent, ok := args["content"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: content",
				IsError: true,
			}, nil
		}
		content, ok := rawContent.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("content must be a string, got %T", rawContent),
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

		// Create parent directories automatically.
		parentDir := filepath.Dir(resolved)
		if err := os.MkdirAll(parentDir, 0o755); err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to create parent directories %s: %v", parentDir, err),
				IsError: true,
			}, nil
		}

		// Write the file under per-path lock (truncate/overwrite if exists).
		writeErr := defaultMutationQueue.WithLock(resolved, func() error {
			return os.WriteFile(resolved, []byte(content), 0o644)
		})
		if writeErr != nil {
			if os.IsPermission(writeErr) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("permission denied: %s", resolved),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to write file %s: %v", resolved, writeErr),
				IsError: true,
			}, nil
		}

		return &registry.ToolResult{
			Content: fmt.Sprintf("Successfully wrote to %s", resolved),
			Details: map[string]any{
				"path": resolved,
				"bytes_written": len(content),
			},
		}, nil
	}
}
