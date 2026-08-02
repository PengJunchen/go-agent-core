package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// grepMaxLines limits output to avoid overwhelming the LLM context.
const grepMaxLines = 500

// skipDirs are directory names to skip during recursive search.
var skipDirs = map[string]bool{
	".git": true,
	".svn": true,
	".hg": true,
	"node_modules": true,
	"vendor": true,
	"__pycache__": true,
	".tox": true,
	".mypy_cache": true,
	".pytest_cache": true,
}

// NewGrepTool builds a grep ToolDefinition scoped to workDir.
// The returned handler searches for regex pattern matches across files
// with optional file filtering and context lines.
func NewGrepTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "grep",
		Description: "Search for a regex pattern across files. Returns matching lines with file path, line number, and optional context lines. Supports file name filtering with include pattern.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type": "string",
					"description": "Regular expression pattern to search for",
				},
				"path": map[string]any{
					"type": "string",
					"description": "Directory to search in (defaults to project root)",
				},
				"include": map[string]any{
					"type": "string",
					"description": "Glob pattern to filter files (e.g., '*.go')",
				},
				"context_lines": map[string]any{
					"type": "integer",
					"description": "Number of context lines around matches (default: 2)",
				},
			},
			"required": []any{"pattern"},
		},
		Handler: grepHandler(workDir),
		ParallelSafe: true,
		ValidateArgs: true,
	}
}

// grepHandler returns a ToolHandler closure capturing workDir for sandboxing.
func grepHandler(workDir string) registry.ToolHandler {
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

		re, err := regexp.Compile(pattern)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("invalid regex pattern: %v", err),
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

		// Parse optional include pattern.
		var includePattern string
		if rawInclude, exists := args["include"]; exists && rawInclude != nil {
			if inc, ok := rawInclude.(string); ok && inc != "" {
				includePattern = inc
			}
		}

		// Parse optional context lines.
		contextLines := 2
		if rawCtx, exists := args["context_lines"]; exists && rawCtx != nil {
			if n, err := toInt(rawCtx); err == nil && n >= 0 {
				contextLines = n
			}
		}

		var matches []string
		totalLines := 0

		err = filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, err error) error {
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

			// Apply include pattern filter.
			if includePattern != "" {
				matched, matchErr := filepath.Match(includePattern, d.Name())
				if matchErr != nil || !matched {
					return nil
				}
			}

			// Skip binary files.
			if isBinaryFile(path) {
				return nil
			}

			// Read and search the file.
			fileMatches, _ := searchFile(ctx, path, re, contextLines, &totalLines)
			matches = append(matches, fileMatches...)

			if totalLines >= grepMaxLines {
				return fmt.Errorf("output limit reached")
			}

			return nil
		})

		if err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if len(matches) == 0 {
			return &registry.ToolResult{
				Content: fmt.Sprintf("No matches found for pattern %q in %s", pattern, searchPath),
				Details: map[string]any{
					"pattern": pattern,
					"path": searchPath,
				},
			}, nil
		}

		output := strings.Join(matches, "\n")
		if totalLines >= grepMaxLines {
			// Use unified truncation system: keep one fewer line for the marker.
			output = TruncateLines(output, grepMaxLines-1).Content
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

// searchFile searches a single file for pattern matches and returns formatted results.
func searchFile(ctx context.Context, path string, re *regexp.Regexp, contextLines int, totalLines *int) ([]string, int) { //nolint:unused // lineCount used by callers
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer func() { _ = f.Close() }() // read-only open, close error is irrelevant

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, 0
	}

	// Find matching line indices.
	var matchIndices []int
	for i, line := range lines {
		if re.MatchString(line) {
			matchIndices = append(matchIndices, i)
		}
	}

	if len(matchIndices) == 0 {
		return nil, 0
	}

	// Build output with context lines.
	relPath, err := filepath.Rel(filepath.Dir(path), path)
	if err != nil {
		relPath = path
	}
	_ = relPath // use full path for clarity

	var results []string
	emitted := make(map[int]bool)

	for _, idx := range matchIndices {
		start := idx - contextLines
		if start < 0 {
			start = 0
		}
		end := idx + contextLines
		if end >= len(lines) {
			end = len(lines) - 1
		}

		for i := start; i <= end; i++ {
			if emitted[i] {
				continue
			}
			emitted[i] = true

			prefix := " "
			if i == idx {
				prefix = "> "
			}
			results = append(results, fmt.Sprintf("%s%s:%d:%s", prefix, path, i+1, lines[i]))
			*totalLines++
			if *totalLines >= grepMaxLines {
				return results, *totalLines
			}
		}
	}

	return results, *totalLines
}

// isBinaryFile checks if a file appears to be binary by reading its first 512 bytes.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer func() { _ = f.Close() }() // read-only open, close error is irrelevant

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return true
	}

	// Check for null bytes as a simple binary detection heuristic.
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}
