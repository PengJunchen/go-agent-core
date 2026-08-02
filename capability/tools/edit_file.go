package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// NewEditFileTool builds an edit_file ToolDefinition scoped to workDir.
// The returned handler performs exact string replacement within a file,
// returning context about surrounding content when the target string is
// not found to help the LLM self-correct.
func NewEditFileTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "edit_file",
		Description: "Edit a file by replacing an exact string match. Supports replacing a single occurrence (default) or all occurrences. Returns context about the file content when the target string is not found to help self-correct.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type": "string",
					"description": "Path to the file to edit",
				},
				"old_str": map[string]any{
					"type": "string",
					"description": "Exact string to find and replace",
				},
				"new_str": map[string]any{
					"type": "string",
					"description": "String to replace old_str with",
				},
				"replace_all": map[string]any{
					"type": "boolean",
					"description": "Replace all occurrences of old_str (default: false)",
				},
			},
			"required": []any{"file_path", "old_str", "new_str"},
		},
		Handler: editFileHandler(workDir),
		ParallelSafe: false,
		ValidateArgs: true,
	}
}

// editFileHandler returns a ToolHandler closure capturing workDir for sandboxing.
func editFileHandler(workDir string) registry.ToolHandler {
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

		rawOld, ok := args["old_str"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: old_str",
				IsError: true,
			}, nil
		}
		oldStr, ok := rawOld.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("old_str must be a string, got %T", rawOld),
				IsError: true,
			}, nil
		}

		rawNew, ok := args["new_str"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: new_str",
				IsError: true,
			}, nil
		}
		newStr, ok := rawNew.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("new_str must be a string, got %T", rawNew),
				IsError: true,
			}, nil
		}

		replaceAll := false
		if rawReplaceAll, exists := args["replace_all"]; exists && rawReplaceAll != nil {
			if b, ok := rawReplaceAll.(bool); ok {
				replaceAll = b
			}
		}

		// Resolve path with NFC normalization, tilde expansion, and sandbox check.
		resolved, err := resolver.Resolve(filePath)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to resolve path: %v", err),
				IsError: true,
			}, nil
		}

		// Read-modify-write under per-path lock to prevent race conditions.
		var result *registry.ToolResult
		lockErr := defaultMutationQueue.WithLock(resolved, func() error {
			data, err := os.ReadFile(resolved)
			if err != nil {
				if os.IsNotExist(err) {
					result = &registry.ToolResult{
						Content: fmt.Sprintf("file not found: %s", resolved),
						IsError: true,
					}
					return nil
				}
				result = &registry.ToolResult{
					Content: fmt.Sprintf("failed to read file %s: %v", resolved, err),
					IsError: true,
				}
				return nil
			}

			content := string(data)

			// Check if old_str exists in the file.
			if !strings.Contains(content, oldStr) {
				// Provide context to help LLM self-correct.
				contextMsg := buildEditContext(content, oldStr)
				result = &registry.ToolResult{
					Content: fmt.Sprintf("old_str not found in %s.\n%s", resolved, contextMsg),
					IsError: true,
				}
				return nil
			}

			// Check for multiple occurrences when replace_all is false.
			occurrences := strings.Count(content, oldStr)
			if occurrences > 1 && !replaceAll {
				result = &registry.ToolResult{
					Content: fmt.Sprintf("old_str found %d times in %s. Use replace_all=true to replace all occurrences, or provide more context in old_str to uniquely identify the match.", occurrences, resolved),
					IsError: true,
				}
				return nil
			}

			// Perform the replacement.
			var newContent string
			if replaceAll {
				newContent = strings.ReplaceAll(content, oldStr, newStr)
			} else {
				newContent = strings.Replace(content, oldStr, newStr, 1)
			}

			if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
				result = &registry.ToolResult{
					Content: fmt.Sprintf("failed to write file %s: %v", resolved, err),
					IsError: true,
				}
				return nil
			}

			replaced := occurrences
			if !replaceAll && occurrences > 0 {
				replaced = 1
			}

			result = &registry.ToolResult{
				Content: fmt.Sprintf("Successfully replaced %d occurrence(s) in %s", replaced, resolved),
				Details: map[string]any{
					"path": resolved,
					"replaced": replaced,
					"replace_all": replaceAll,
				},
			}
			return nil
		})

		if lockErr != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to acquire file lock: %v", lockErr),
				IsError: true,
			}, nil
		}

		return result, nil
	}
}

// buildEditContext generates helpful context when old_str is not found.
// It shows the file content with line numbers and highlights lines that
// partially match the target string.
func buildEditContext(content string, oldStr string) string {
	lines := strings.Split(content, "\n")
	// Take first line of oldStr to check for partial matches.
	firstLine := oldStr
	if idx := strings.Index(oldStr, "\n"); idx >= 0 {
		firstLine = oldStr[:idx]
	}

	var b strings.Builder
	b.WriteString("File content (with line numbers):\n")

	matched := false
	maxLines := 50
	if len(lines) < maxLines {
		maxLines = len(lines)
	}
	for i := 0; i < maxLines; i++ {
		prefix := " "
		if strings.Contains(lines[i], firstLine) || strings.TrimSpace(lines[i]) == strings.TrimSpace(firstLine) {
			prefix = "> " // Highlight partial matches.
			matched = true
		}
		fmt.Fprintf(&b, "%s%6d\t%s\n", prefix, i+1, lines[i])
	}
	if len(lines) > maxLines {
		fmt.Fprintf(&b, " ... (%d more lines)\n", len(lines)-maxLines)
	}

	if !matched {
		b.WriteString("\nNo partial matches found. The exact string may have different whitespace or indentation.\n")
	}

	return b.String()
}
