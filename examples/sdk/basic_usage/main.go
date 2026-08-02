// Package main demonstrates basic agent usage with go-agent-core.
//
// This example shows how to:
// 1. Create a prompt Builder with environment context
// 2. Build a system prompt
// 3. Construct a SummaryCompactor with a mock provider
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
	"github.com/pengjunchen/go-agent-core/memory/compactor"
	promptbuilder "github.com/pengjunchen/go-agent-core/prompt"
)

// demoProvider is a minimal ModelProvider for demonstration.
type demoProvider struct{}

func (d *demoProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	return nil, fmt.Errorf("streaming not supported in demo")
}

func (d *demoProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "Hello from the demo agent!"},
		},
	}, nil
}

func (d *demoProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{
		Provider: "demo",
		ModelName: "demo-model",
	}
}

func main() {
	ctx := context.Background()

	// 1. Build a system prompt with environment context.
	builder := promptbuilder.NewBuilder(
		promptbuilder.WithWorkDir("."),
	)
	systemPrompt := builder.Build()
	fmt.Println("=== System Prompt (truncated) ===")
	if len(systemPrompt) > 200 {
		fmt.Println(systemPrompt[:200], "...")
	} else {
		fmt.Println(systemPrompt)
	}
	fmt.Println()

	// 2. Create a SummaryCompactor (using the demo provider).
	compact := compactor.NewSummaryCompactor(&demoProvider{}, nil)

	// 3. Simulate a conversation.
	items := []struct {
		role string
		content string
	}{
		{"user", "What is Go?"},
		{"assistant", "Go is a statically typed, compiled programming language."},
		{"user", "How do I write tests in Go?"},
		{"assistant", "Use the testing package with test functions prefixed by Test."},
	}

	fmt.Println("=== Conversation ===")
	for _, item := range items {
		fmt.Printf("[%s] %s\n", item.role, item.content)
	}
	fmt.Println()

	// 4. Generate a response using the provider.
	resp, err := (&demoProvider{}).Generate(ctx, nil, nil)
	if err != nil {
		log.Fatalf("Generate: %v", err)
	}
	fmt.Println("=== Agent Response ===")
	for _, c := range resp.Content {
		if c.Type == message.ContentText {
			fmt.Println(c.Text)
		}
	}

	_ = compact // SummaryCompactor is ready for use when a real provider is available
}
