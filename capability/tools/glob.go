package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// globMaxResults limits output to avoid overwhelming the LLM context.
const globMaxResults = 1000

// NewGlobTool builds a glob ToolDefinition scoped to workDir.
// The returned handler finds files matching a glob pattern,
// supporting ** for recursive matching.
func NewGlobTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "glob",
		Description: "Find files matching a glob pattern. Supports ** for recursive directory matching (e.g., '**/*.go'). Returns matched file paths, one per line.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type": "string",
					"description": "Glob pattern to match files (e.g., '**/*.go')",
				},
				"path": map[string]any{
					"type": "string",
					"description": "Directory to search in (defaults to project root)",
				},
			},
			"required": []any{"pattern"},
		},
		Handler: globHandler(workDir),
		ParallelSafe: true,
		ValidateArgs: true,
	}
}

// globHandler returns a ToolHandler closure capturing workDir for sandboxing.
func globHandler(workDir string) registry.ToolHandler {
	resolver := NewPathResolver(workDir)
	return func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rawPattern, ok := args["pattern"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: pattern",
				IsError: true,
			}, nil
		}
		pattern, ok := rawPattern.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("pattern must be a string, got %T", rawPattern),
				IsError: true,
			}, nil
		}

		// Resolve search path.
		searchPath := workDir
		if rawPath, exists := args["path"]; exists && rawPath != nil {
			if p, ok := rawPath.(string); ok && p != "" {
				resolved, err := resolver.Resolve(p)
				if err != nil {
					return &registry.ToolResult{
						Content: fmt.Sprintf("failed to resolve path: %v", err),
						IsError: true,
					}, nil
				}
				searchPath = resolved
			}
		}

		var matches []string

		err := filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip inaccessible entries
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if d.IsDir() {
				if skipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}

			// Get the relative path from the search root for pattern matching.
			relPath, relErr := filepath.Rel(searchPath, path)
			if relErr != nil {
				return nil
			}

			if matchGlobPattern(pattern, relPath) {
				matches = append(matches, path)
				if len(matches) >= globMaxResults {
					return fmt.Errorf("result limit reached")
				}
			}

			return nil
		})

		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if len(matches) == 0 {
			return &registry.ToolResult{
				Content: fmt.Sprintf("No files matched pattern %q in %s", pattern, searchPath),
				Details: map[string]any{
					"pattern": pattern,
					"path": searchPath,
				},
			}, nil
		}

		output := strings.Join(matches, "\n")
		if len(matches) >= globMaxResults {
			// Use unified truncation system: keep one fewer line for the marker.
			output = TruncateLines(output, globMaxResults-1).Content
		}

		return &registry.ToolResult{
			Content: output,
			Details: map[string]any{
				"pattern": pattern,
				"path": searchPath,
				"match_count": len(matches),
			},
		}, nil
	}
}

// matchGlobPattern matches a glob pattern against a relative file path.
// Supports ** for recursive matching by splitting the pattern and path.
func matchGlobPattern(pattern, relPath string) bool {
	// Normalize separators.
	pattern = filepath.ToSlash(pattern)
	relPath = filepath.ToSlash(relPath)

	// Handle ** patterns by converting to a recursive walk approach.
	if strings.Contains(pattern, "**") {
		return matchDoubleStar(pattern, relPath)
	}

	// Simple pattern: match against the filename or relative path.
	matched, err := filepath.Match(pattern, relPath)
	if err != nil {
		// Invalid pattern — try matching just the filename.
		matched, _ = filepath.Match(pattern, filepath.Base(relPath))
		return matched
	}

	if matched {
		return true
	}

	// Also try matching just the filename.
	matched, _ = filepath.Match(pattern, filepath.Base(relPath))
	return matched
}

// matchDoubleStar handles ** glob patterns by splitting into segments.
func matchDoubleStar(pattern, relPath string) bool {
	// Split pattern by /**/ to get prefix and suffix.
	// e.g., "**/*.go" -> prefix="", suffix="*.go"
	// e.g., "src/**/*.go" -> prefix="src", suffix="*.go"
	parts := strings.SplitN(pattern, "**", 2)
	prefix := strings.TrimSuffix(parts[0], "/")
	suffix := strings.TrimPrefix(parts[1], "/")

	pathParts := strings.Split(relPath, "/")

	// Find where the prefix matches.
	startIdx := 0
	if prefix != "" {
		prefixParts := strings.Split(prefix, "/")
		found := false
		for i := 0; i <= len(pathParts)-len(prefixParts); i++ {
			match := true
			for j, pp := range prefixParts {
				m, err := filepath.Match(pp, pathParts[i+j])
				if err != nil || !m {
					match = false
					break
				}
			}
			if match {
				startIdx = i + len(prefixParts)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Match the suffix against the remaining path.
	if suffix == "" {
		return true
	}

	// Try matching suffix against progressively shorter tails of the path.
	for i := startIdx; i < len(pathParts); i++ {
		tail := strings.Join(pathParts[i:], "/")
		matched, err := filepath.Match(suffix, tail)
		if err == nil && matched {
			return true
		}
		// Also try matching suffix against just the filename.
		if len(pathParts) > 0 {
			matched, _ = filepath.Match(suffix, pathParts[len(pathParts)-1])
			if matched {
				return true
			}
		}
	}

	// Special case: suffix matches the full remaining path.
	remaining := strings.Join(pathParts[startIdx:], "/")
	matched, err := filepath.Match(suffix, remaining)
	if err == nil && matched {
		return true
	}

	// Final fallback: match suffix against just the filename.
	matched, _ = filepath.Match(suffix, filepath.Base(relPath))
	return matched
}
