package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// webFetchDefaultMaxLength is the default maximum content length for web_fetch (100KB).
const webFetchDefaultMaxLength = 100 * 1024

// webFetchTimeout is the HTTP request timeout.
const webFetchTimeout = 30 * time.Second

// textContentPrefixes lists Content-Type prefixes that are considered text.
var textContentPrefixes = []string{
	"text/",
	"application/json",
	"application/xml",
	"application/javascript",
	"application/xhtml",
}

// NewWebFetchTool builds a web_fetch ToolDefinition.
// The returned handler fetches content from a URL and returns text content.
func NewWebFetchTool(workDir string) registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "web_fetch",
		Description: "Fetch content from a URL. Returns text content only (rejects binary). Supports optional max_length for truncation.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type": "string",
					"description": "URL to fetch content from",
				},
				"max_length": map[string]any{
					"type": "integer",
					"description": "Maximum content length in characters (default: 102400, i.e. 100KB)",
				},
			},
			"required": []any{"url"},
		},
		Handler: webFetchHandler(),
		ParallelSafe: true,
		ValidateArgs: true,
	}
}

// webFetchHandler returns a ToolHandler for fetching URL content.
func webFetchHandler() registry.ToolHandler {
	return func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rawURL, ok := args["url"]
		if !ok {
			return &registry.ToolResult{
				Content: "missing required parameter: url",
				IsError: true,
			}, nil
		}
		url, ok := rawURL.(string)
		if !ok {
			return &registry.ToolResult{
				Content: fmt.Sprintf("url must be a string, got %T", rawURL),
				IsError: true,
			}, nil
		}
		if strings.TrimSpace(url) == "" {
			return &registry.ToolResult{
				Content: "url must not be empty",
				IsError: true,
			}, nil
		}

		// Parse optional max_length.
		maxLength := webFetchDefaultMaxLength
		if rawMaxLength, exists := args["max_length"]; exists && rawMaxLength != nil {
			n, err := toInt(rawMaxLength)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("max_length must be an integer: %v", err),
					IsError: true,
				}, nil
			}
			if n > 0 {
				maxLength = n
			}
		}

		// Create HTTP request with timeout.
		reqCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("invalid URL %q: %v", url, err),
				IsError: true,
			}, nil
		}
		req.Header.Set("User-Agent", "go-agent-core/web_fetch")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to fetch URL %q: %v", url, err),
				IsError: true,
			}, nil
		}
		defer func() { _ = resp.Body.Close() }() // read-only, close error irrelevant

		// Check status code.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &registry.ToolResult{
				Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status),
				IsError: true,
				Details: map[string]any{
					"status_code": resp.StatusCode,
					"url": url,
				},
			}, nil
		}

		// Check Content-Type — reject binary.
		contentType := resp.Header.Get("Content-Type")
		if !isTextContentType(contentType) {
			return &registry.ToolResult{
				Content: fmt.Sprintf("binary content type %q not supported; only text content is allowed", contentType),
				IsError: true,
				Details: map[string]any{
					"content_type": contentType,
					"url": url,
				},
			}, nil
		}

		// Read response body.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return &registry.ToolResult{
				Content: fmt.Sprintf("failed to read response body: %v", err),
				IsError: true,
			}, nil
		}

		content := string(body)

		// Truncate if needed using TruncateContent.
		tr := TruncateContent(content, maxLength)

		return &registry.ToolResult{
			Content: tr.Content,
			Details: map[string]any{
				"url": url,
				"status_code": resp.StatusCode,
				"content_type": contentType,
				"was_truncated": tr.WasTruncated,
				"original_length": tr.OriginalLength,
			},
		}, nil
	}
}

// isTextContentType checks if a Content-Type header indicates text content.
func isTextContentType(contentType string) bool {
	ct := strings.ToLower(contentType)
	// Remove parameters like charset.
	if idx := strings.Index(ct, ";"); idx != -1 {
		ct = strings.TrimSpace(ct[:idx])
	}

	for _, prefix := range textContentPrefixes {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}
	return false
}
