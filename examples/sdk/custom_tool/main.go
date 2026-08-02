// Package main demonstrates custom tool registration with go-agent-core.
//
// This example shows how to:
// 1. Define a custom tool with a ToolDefinition
// 2. Register it in a ToolRegistry
// 3. Execute the tool handler
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// wordCountTool is a custom tool that counts words in a string.
func wordCountTool() registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "word_count",
		Description: "Count the number of words in a text string",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type": "string",
					"description": "The text to count words in",
				},
			},
			"required": []string{"text"},
		},
		PromptGuidelines: "Use word_count to count words in any text before reporting length.",
		Handler: func(_ context.Context, args map[string]any) (*registry.ToolResult, error) {
			text, ok := args["text"].(string)
			if !ok {
				return &registry.ToolResult{
					Content: "error: 'text' argument is required and must be a string",
					IsError: true,
				}, nil
			}
			words := strings.Fields(text)
			return &registry.ToolResult{
				Content: fmt.Sprintf("Word count: %d", len(words)),
				Details: map[string]any{
					"count": len(words),
					"length": len(text),
				},
			}, nil
		},
		ParallelSafe: true,
	}
}

func main() {
	ctx := context.Background()

	// 1. Create the custom tool definition.
	tool := wordCountTool()

	fmt.Println("=== Custom Tool Registration ===")
	fmt.Printf("Name: %s\n", tool.Name)
	fmt.Printf("Description: %s\n", tool.Description)
	fmt.Printf("PromptGuidelines: %s\n", tool.PromptGuidelines)
	fmt.Printf("ParallelSafe: %v\n", tool.ParallelSafe)
	fmt.Println()

	// 2. Execute the tool handler directly.
	fmt.Println("=== Tool Execution ===")
	testText := "The quick brown fox jumps over the lazy dog"
	result, err := tool.Handler(ctx, map[string]any{
		"text": testText,
	})
	if err != nil {
		log.Fatalf("Tool execution failed: %v", err)
	}

	fmt.Printf("Input: %q\n", testText)
	fmt.Printf("Result: %s\n", result.Content)
	fmt.Printf("Details: %v\n", result.Details)
	fmt.Println()

	// 3. Test error handling.
	fmt.Println("=== Error Handling ===")
	errResult, err := tool.Handler(ctx, map[string]any{
		"wrong_key": "test",
	})
	if err != nil {
		log.Fatalf("Unexpected error: %v", err)
	}
	fmt.Printf("Error result: %s (isError=%v)\n", errResult.Content, errResult.IsError)

	// 4. Demonstrate that the tool definition can be used with a registry.
	fmt.Println("\n=== Integration with ToolRegistry ===")
	fmt.Printf("ToolDefinition is compatible with ToolRegistry: %T\n", tool)
	fmt.Println("RegisterTool / GetTool / ListTools can use this definition directly.")
}
