package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// lsMaxOutputLines is the default maximum number of output lines for ls.
// Outputs exceeding this are truncated.
const lsMaxOutputLines = 500

// NewLsTool builds an ls ToolDefinition scoped to workDir.
// The returned handler lists directory contents with name, type, size, and mod_time.
func NewLsTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "ls",
		Description: "List directory contents. Returns entries with name, type (file/dir), size, and modification time.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string",
					"description": "Path to the directory to list",
				},
			},
			"required": []any{"path"},
		},
		Handler: lsHandler(workDir),
		ParallelSafe: true,
		ValidateArgs: true,
	}
}

// lsHandler returns a ToolHandler closure capturing workDir for sandboxing.
func lsHandler(workDir string) registry.ToolHandler {
	resolver := NewPathResolver(workDir)
	return func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rawPath, ok := args["path"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: path",
				IsError: true,
			}, nil
		}
		path, ok := rawPath.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("path must be a string, got %T", rawPath),
				IsError: true,
			}, nil
		}
		if strings.TrimSpace(path) == "" {
			return &registry.ToolResult{
				Content: "path must not be empty",
				IsError: true,
			}, nil
		}

		// Resolve path with NFC normalization, tilde expansion, and sandbox check.
		resolved, err := resolver.Resolve(path)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to resolve path: %v", err),
				IsError: true,
			}, nil
		}

		// Check that the path is a directory.
		info, err := os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("directory not found: %s", resolved),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to stat path %s: %v", resolved, err),
				IsError: true,
			}, nil
		}
		if !info.IsDir() {
			return &registry.ToolResult{
				Content: fmt.Sprintf("path is not a directory: %s", resolved),
				IsError: true,
			}, nil
		}

		// Read directory entries.
		entries, err := os.ReadDir(resolved)
		if err != nil {
			if os.IsPermission(err) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("permission denied: %s", resolved),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to read directory %s: %v", resolved, err),
				IsError: true,
			}, nil
		}

		var lines []string
		for _, entry := range entries {
			entryType := "file"
			if entry.IsDir() {
				entryType = "dir"
			}

			entryInfo, err := entry.Info()
			if err != nil {
				// Skip entries we can't stat.
				continue
			}

			line := fmt.Sprintf("%s\t%s\t%d\t%s",
				entryType,
				entry.Name(),
				entryInfo.Size(),
				entryInfo.ModTime().Format(time.RFC3339),
			)
			lines = append(lines, line)
		}

		output := strings.Join(lines, "\n")
		truncated := TruncateLines(output, lsMaxOutputLines)

		return &registry.ToolResult{
			Content: truncated.Content,
			Details: map[string]any{
				"path": resolved,
				"entry_count": len(entries),
				"entries": len(lines),
				"truncated": truncated.WasTruncated,
			},
		}, nil
	}
}
