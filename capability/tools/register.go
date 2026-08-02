package tools

import (
	"context"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// RegisterBuiltinTools registers all 9 built-in coding tools into the given ToolRegistry.
// Each tool's Handler captures workDir via closure for sandboxing.
// Read-only tools (read_file, grep, glob, image_view, ls, web_fetch) are marked as ParallelSafe;
// write tools (write_file, edit_file, execute) are not.
func RegisterBuiltinTools(ctx context.Context, reg registry.ToolRegistry, workDir string) error {
	tools := []registry.ToolDefinition{
		NewReadFileTool(workDir),
		NewWriteFileTool(workDir),
		NewEditFileTool(workDir),
		NewExecuteTool(workDir),
		NewGrepTool(workDir),
		NewGlobTool(workDir),
		NewImageViewTool(workDir),
		NewLsTool(workDir),
		NewWebFetchTool(workDir),
	}

	for _, tool := range tools {
		if err := reg.RegisterTool(ctx, tool); err != nil {
			return err
		}
	}

	return nil
}
