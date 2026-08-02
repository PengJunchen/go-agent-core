package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// imageDefaultMaxSize is the default maximum file size for image_view (10MB).
const imageDefaultMaxSize = 10 * 1024 * 1024

// imageMaxOutputLength is the default maximum output length for image_view
// in UTF-16 code units (base64 data URI). Outputs exceeding this are truncated.
const imageMaxOutputLength = 20000

// supportedImageExts maps file extensions to MIME types.
var supportedImageExts = map[string]string{
	".png": "image/png",
	".jpg": "image/jpeg",
	".jpeg": "image/jpeg",
	".gif": "image/gif",
	".webp": "image/webp",
}

// NewImageViewTool builds an image_view ToolDefinition scoped to workDir.
// The returned handler reads an image file and returns its base64-encoded
// content with a data URI prefix based on the file extension.
func NewImageViewTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "image_view",
		Description: "Read an image file and return its base64-encoded content with data URI prefix. Supports PNG, JPEG, GIF, and WebP formats.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string",
					"description": "Path to the image file to read",
				},
				"max_size": map[string]any{
					"type": "integer",
					"description": "Maximum file size in bytes (default: 10485760, i.e. 10MB)",
				},
			},
			"required": []any{"path"},
		},
		Handler: imageViewHandler(workDir),
		ParallelSafe: true,
		ValidateArgs: true,
	}
}

// imageViewHandler returns a ToolHandler closure capturing workDir for sandboxing.
func imageViewHandler(workDir string) registry.ToolHandler {
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

		// Check extension.
		ext := strings.ToLower(filepath.Ext(resolved))
		mime, supported := supportedImageExts[ext]
		if !supported {
			supportedExts := make([]string, 0, len(supportedImageExts))
			for k := range supportedImageExts {
				supportedExts = append(supportedExts, k)
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("unsupported image extension %q; supported: %s", ext, strings.Join(supportedExts, ", ")),
				IsError: true,
			}, nil
		}

		// Parse optional max_size.
		maxSize := imageDefaultMaxSize
		if rawMaxSize, exists := args["max_size"]; exists && rawMaxSize != nil {
			n, err := toInt(rawMaxSize)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("max_size must be an integer: %v", err),
					IsError: true,
				}, nil
			}
			if n > 0 {
				maxSize = n
			}
		}

		// Check file size before reading.
		info, err := os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("file not found: %s", resolved),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to stat file %s: %v", resolved, err),
				IsError: true,
			}, nil
		}

		if info.Size() > int64(maxSize) {
			return &registry.ToolResult{
				Content: fmt.Sprintf("file size %d bytes exceeds max_size %d bytes", info.Size(), maxSize),
				IsError: true,
			}, nil
		}

		// Read the file.
		data, err := os.ReadFile(resolved)
		if err != nil {
			if os.IsPermission(err) {
				return &registry.ToolResult{
					Content: fmt.Sprintf("permission denied: %s", resolved),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to read file %s: %v", resolved, err),
				IsError: true,
			}, nil
		}

		// Encode as base64 with data URI prefix.
		encoded := base64.StdEncoding.EncodeToString(data)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mime, encoded)
		truncated := TruncateContent(dataURI, imageMaxOutputLength)

		return &registry.ToolResult{
			Content: truncated.Content,
			Details: map[string]any{
				"path": resolved,
				"mime_type": mime,
				"size": len(data),
				"truncated": truncated.WasTruncated,
			},
		}, nil
	}
}
