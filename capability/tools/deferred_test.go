package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// AC-3: Deferred tools loaded on demand.
func TestSplitDeferredTools_BasicSplit(t *testing.T) {
	tool1 := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return &registry.ToolResult{Content: "tool1"}, nil
	})
	tool2 := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return &registry.ToolResult{Content: "tool2"}, nil
	})
	tool3 := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return &registry.ToolResult{Content: "tool3"}, nil
	})

	tools := []registry.ToolHandler{tool1, tool2, tool3}
	active, deferred := SplitDeferredTools(tools, 1)

	if len(active) != 1 {
		t.Fatalf("active count = %d, want 1", len(active))
	}
	if len(deferred) != 2 {
		t.Fatalf("deferred count = %d, want 2", len(deferred))
	}
}

// AC-3: All tools active when count <= maxActive.
func TestSplitDeferredTools_AllActive(t *testing.T) {
	tool1 := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return &registry.ToolResult{Content: "tool1"}, nil
	})
	tool2 := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return &registry.ToolResult{Content: "tool2"}, nil
	})

	tools := []registry.ToolHandler{tool1, tool2}
	active, deferred := SplitDeferredTools(tools, 5)

	if len(active) != 2 {
		t.Fatalf("active count = %d, want 2", len(active))
	}
	if len(deferred) != 0 {
		t.Fatalf("deferred count = %d, want 0", len(deferred))
	}
}

// AC-3: Empty tools list returns empty active and deferred.
func TestSplitDeferredTools_Empty(t *testing.T) {
	active, deferred := SplitDeferredTools(nil, 3)
	if len(active) != 0 {
		t.Errorf("active count = %d, want 0", len(active))
	}
	if len(deferred) != 0 {
		t.Errorf("deferred count = %d, want 0", len(deferred))
	}
}

// AC-3: Deferred tool loader returns the handler.
func TestDeferredTool_Load(t *testing.T) {
	expectedHandler := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return &registry.ToolResult{Content: "loaded"}, nil
	})

	dt := DeferredTool{
		Definition: registry.ToolDefinition{
			Name: "deferred-tool",
			Description: "A deferred tool",
		},
		Loader: func() (registry.ToolHandler, error) {
			return expectedHandler, nil
		},
	}

	handler, err := dt.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if handler == nil {
		t.Fatal("handler should not be nil")
	}

	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("handler call: %v", err)
	}
	if result.Content != "loaded" {
		t.Errorf("content = %q, want %q", result.Content, "loaded")
	}
}

// AC-3: Deferred tool loader returns error.
func TestDeferredTool_LoadError(t *testing.T) {
	dt := DeferredTool{
		Definition: registry.ToolDefinition{
			Name: "error-tool",
		},
		Loader: func() (registry.ToolHandler, error) {
			return nil, errors.New("load failed")
		},
	}

	_, err := dt.Load()
	if err == nil {
		t.Fatal("expected error from Load")
	}
}

// AC-3: Deferred tool can only be loaded once (cached).
func TestDeferredTool_LoadOnce(t *testing.T) {
	loadCount := 0
	dt := DeferredTool{
		Definition: registry.ToolDefinition{
			Name: "cache-tool",
		},
		Loader: func() (registry.ToolHandler, error) {
			loadCount++
			return func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
				return &registry.ToolResult{Content: "ok"}, nil
			}, nil
		},
	}

	_, err := dt.Load()
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	_, err = dt.Load()
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}

	if loadCount != 1 {
		t.Errorf("loadCount = %d, want 1 (should be cached)", loadCount)
	}
}

// AC-3: maxActive of 0 means all tools are deferred.
func TestSplitDeferredTools_MaxActiveZero(t *testing.T) {
	tool1 := registry.ToolHandler(func(_ context.Context, _ map[string]any) (*registry.ToolResult, error) {
		return nil, nil
	})

	active, deferred := SplitDeferredTools([]registry.ToolHandler{tool1}, 0)
	if len(active) != 0 {
		t.Errorf("active count = %d, want 0", len(active))
	}
	if len(deferred) != 1 {
		t.Errorf("deferred count = %d, want 1", len(deferred))
	}
}
