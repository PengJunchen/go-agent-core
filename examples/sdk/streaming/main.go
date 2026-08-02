// Package main demonstrates streaming response handling with go-agent-core.
//
// This example shows how to:
// 1. Set up a ModelProvider that supports streaming
// 2. Consume StreamEvents from a StreamChat call
// 3. Handle different event types (text delta, tool call, done, error)
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// streamingDemoProvider demonstrates a provider that produces stream events.
type streamingDemoProvider struct{}

func (d *streamingDemoProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	ch := make(chan stream.StreamEvent, 10)

	go func() {
		defer close(ch)

		// Simulate streaming text response in chunks.
		chunks := []string{"Hello", " ", "from", " ", "streaming", " ", "demo!"}
		for _, chunk := range chunks {
			ch <- stream.StreamEvent{
				Type: stream.StreamTextDelta,
				Content: chunk,
			}
			time.Sleep(50 * time.Millisecond) // simulate network delay
		}

		// Send done event.
		ch <- stream.StreamEvent{
			Type: stream.StreamDone,
			FinishReason: "stop",
		}
	}()

	return ch, nil
}

func (d *streamingDemoProvider) Generate(_ context.Context, msgs []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "Non-streaming response"},
		},
	}, nil
}

func (d *streamingDemoProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{
		Provider: "streaming-demo",
		ModelName: "demo-stream",
		SupportsStreaming: true,
	}
}

func main() {
	ctx := context.Background()
	provider := &streamingDemoProvider{}

	fmt.Println("=== Streaming Response Demo ===")

	// 1. Start a streaming chat.
	eventCh, err := provider.StreamChat(ctx, nil, nil)
	if err != nil {
		fmt.Printf("StreamChat error: %v\n", err)
		return
	}

	// 2. Consume events from the stream.
	var fullText string
	for event := range eventCh {
		switch event.Type {
		case stream.StreamTextDelta:
			fmt.Print(event.Content) // print incrementally
			fullText += event.Content
		case stream.StreamThinkingDelta:
			fmt.Printf("[thinking: %s]", event.Thinking)
		case stream.StreamToolCallStart:
			fmt.Printf("\n[tool call: %s]", event.ToolCall.Name)
		case stream.StreamToolCallResult:
			fmt.Printf("[tool result: %s]", event.Content)
		case stream.StreamDone:
			fmt.Printf("\n[done: reason=%s]\n", event.FinishReason)
		case stream.StreamError:
			fmt.Printf("\n[error: %v]\n", event.Error)
		}
	}

	fmt.Println()
	fmt.Printf("Full text: %s\n", fullText)

	// 3. Compare with non-streaming Generate.
	fmt.Println("\n=== Non-Streaming Response ===")
	resp, err := provider.Generate(ctx, nil, nil)
	if err != nil {
		fmt.Printf("Generate error: %v\n", err)
		return
	}
	for _, c := range resp.Content {
		if c.Type == message.ContentText {
			fmt.Println(c.Text)
		}
	}
}
