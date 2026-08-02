// Package main demonstrates a complete end-to-end agent using go-agent-core.
//
// This example wires together every layer of the framework:
// 1. A mock streaming ModelProvider (LLM layer L4) that emits text deltas
// and requests tool calls across two turns.
// 2. A ContextManager (memory layer L3) for conversation history.
// 3. A ToolRegistry (capability layer L2) with both built-in tools and a
// custom tool, plus a ToolHook pipeline for observability.
// 4. A Session (agent layer L1) assembled via the fluent builder, which
// runs the LoopAgent and streams AgentEvents back to the caller.
//
// The example is fully self-contained: no real LLM API key is required.
//
// Build: go build ./examples/sdk/full_agent/...
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/pengjunchen/go-agent-core/agent/event"
	"github.com/pengjunchen/go-agent-core/agent/loop"
	"github.com/pengjunchen/go-agent-core/agent/session"
	"github.com/pengjunchen/go-agent-core/capability/registry"
	"github.com/pengjunchen/go-agent-core/capability/tools"
	"github.com/pengjunchen/go-agent-core/capability/toolhook"
	ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
	"github.com/pengjunchen/go-agent-core/llm/message"
	"github.com/pengjunchen/go-agent-core/llm/provider"
	"github.com/pengjunchen/go-agent-core/llm/stream"
)

// ─── Mock Streaming Provider ────────────────────────────────────────

// mockProvider is a self-contained ModelProvider that simulates a two-turn
// ReAct conversation:
//
//	Turn 1: the model emits a text delta, then requests a tool call
//	 (get_time) to gather information.
//	Turn 2: after the tool result is fed back, the model emits a final
//	 text response and stops.
//
// A call counter advances through the canned responses so each StreamChat
// invocation returns the next turn's events.
type mockProvider struct {
	mu sync.Mutex
	callCount int
}

func newMockProvider() *mockProvider { return &mockProvider{} }

func (m *mockProvider) StreamChat(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (<-chan stream.StreamEvent, error) {
	m.mu.Lock()
	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

	ch := make(chan stream.StreamEvent, 16)

	go func() {
		defer close(ch)

		switch idx {
		case 0:
			// Turn 1: narrate intent, then request a tool call.
			ch <- stream.StreamEvent{Type: stream.StreamTextDelta, Content: "Let me check the current time."}
			ch <- stream.StreamEvent{
				Type: stream.StreamToolCallStart,
				ToolCall: &stream.ToolCall{
					ID: "call-001",
					Name: "get_time",
					Arguments: map[string]any{"timezone": "UTC"},
				},
			}
			ch <- stream.StreamEvent{Type: stream.StreamDone, FinishReason: "tool_calls"}
		default:
			// Turn 2: produce the final answer.
			ch <- stream.StreamEvent{Type: stream.StreamTextDelta, Content: "Based on the tool result, the time is now available."}
			ch <- stream.StreamEvent{Type: stream.StreamDone, FinishReason: "stop"}
		}
	}()

	return ch, nil
}

func (m *mockProvider) Generate(_ context.Context, _ []message.Message, _ *provider.ChatOptions) (*message.Message, error) {
	return &message.Message{
		Role: message.RoleAssistant,
		Content: []message.Content{
			{Type: message.ContentText, Text: "Generate is not used in this example."},
		},
	}, nil
}

func (m *mockProvider) ModelInfo() *provider.ModelInfo {
	return &provider.ModelInfo{
		Provider: "mock",
		ModelName: "mock-model",
		SupportsStreaming: true,
		SupportsThinking: false,
	}
}

// ─── Observability ToolHook ─────────────────────────────────────────

// loggingHook is a ToolHook that prints every tool invocation, demonstrating
// the ToolHook Before/After pipeline (L2 capability layer). It never blocks
// execution; it only observes.
type loggingHook struct{}

func (loggingHook) Before(_ context.Context, call *toolhook.ToolCall) (*toolhook.BeforeResult, error) {
	fmt.Printf(" [hook] BEFORE tool=%s args=%v\n", call.Name, call.Arguments)
	return &toolhook.BeforeResult{}, nil
}

func (loggingHook) After(_ context.Context, call *toolhook.ToolCall, result *toolhook.ToolResult) (*toolhook.AfterResult, error) {
	status := "ok"
	if result.IsError {
		status = "error"
	}
	fmt.Printf(" [hook] AFTER tool=%s status=%s content=%q\n", call.Name, status, truncate(result.Content, 60))
	return &toolhook.AfterResult{}, nil
}

// truncate shortens a string for display, appending an ellipsis when needed.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ─── Custom Tool ────────────────────────────────────────────────────

// newGetTimeTool returns a custom tool definition that reports the current
// time for a given timezone. This mirrors how a user would add domain-specific
// tools to the registry alongside the built-in coding tools.
func newGetTimeTool() registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: "get_time",
		Description: "Get the current time for a given timezone (e.g. UTC, Asia/Shanghai).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"timezone": map[string]any{
					"type": "string",
					"description": "IANA timezone name, e.g. UTC or Asia/Shanghai",
				},
			},
			"required": []string{"timezone"},
		},
		PromptGuidelines: "Use get_time when the user asks about the current time.",
		ParallelSafe: true,
		Handler: func(_ context.Context, args map[string]any) (*registry.ToolResult, error) {
			tz, _ := args["timezone"].(string)
			if tz == "" {
				tz = "UTC"
			}
			loc, err := time.LoadLocation(tz)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("unknown timezone: %s", tz),
					IsError: true,
				}, nil
			}
			return &registry.ToolResult{
				Content: fmt.Sprintf("Current time in %s: %s", tz, time.Now().In(loc).Format(time.RFC3339)),
				Details: map[string]any{"timezone": tz},
			}, nil
		},
	}
}

// ─── Main ───────────────────────────────────────────────────────────

func main() {
	ctx := context.Background()

	fmt.Println("========================================")
	fmt.Println(" go-agent-core Full Agent SDK Example")
	fmt.Println("========================================")
	fmt.Println()

	// 1. Build the ToolRegistry (L2 capability layer) and register tools.
	//
	// We register the built-in coding tools (read_file, execute, grep, etc.)
	// scoped to the current working directory, plus a custom domain tool
	// (get_time) to show how user-defined tools integrate.
	toolReg := registry.NewDefaultToolRegistry()
	if err := tools.RegisterBuiltinTools(ctx, toolReg, "."); err != nil {
		log.Fatalf("register builtin tools: %v", err)
	}
	if err := toolReg.RegisterTool(ctx, newGetTimeTool()); err != nil {
		log.Fatalf("register get_time tool: %v", err)
	}

	listed, _ := toolReg.ListTools(ctx)
	fmt.Printf("[1] ToolRegistry: registered %d tools (built-in + custom)\n", len(listed))
	fmt.Println()

	// 2. Build the ContextManager (L3 memory layer).
	//
	// HeuristicContextManager keeps an ordered history of TurnItems and
	// estimates token usage with a char/4 heuristic. Here we disable
	// auto-compaction (maxTokens=0) for a simple demo.
	contextMgr := ctxpkg.NewHeuristicContextManager(ctxpkg.WithMaxTokens(0))

	// 3. Build the ToolHook pipeline (L2 capability layer).
	//
	// The loggingHook prints every tool invocation, demonstrating the
	// Before/After interception point. Hooks are ordered by priority
	// (lower = earlier in Before).
	hookPipeline := toolhook.NewHookPipeline()
	hookPipeline.Register(loggingHook{}, 10)

	// 4. Create the Session (L1 agent layer) via the fluent builder.
	//
	// Session is the top-level facade: it assembles the provider, context
	// manager, tool registry, and hook pipeline into a LoopAgent and
	// exposes a simple Query API. MaxTurns caps the agent loop iterations.
	prov := newMockProvider()
	sess, err := session.NewBuilder().
		WithProvider(prov).
		WithContextManager(contextMgr).
		WithToolRegistry(toolReg).
		WithHookPipeline(hookPipeline).
		WithMaxTurns(5).
		Build()
	if err != nil {
		log.Fatalf("build session: %v", err)
	}
	defer func() { _ = sess.Close() }()

	fmt.Println("[2] Session built (provider + context + tools + hooks)")
	fmt.Printf(" MaxTurns: 5 | Tools: %d | Provider: %s/%s\n", len(listed), prov.ModelInfo().Provider, prov.ModelInfo().ModelName)
	fmt.Println()

	// 5. Submit a query and consume the streaming AgentEvent channel.
	//
	// Session.Query returns a channel of AgentEvent. The agent loop runs
	// in a background goroutine: it calls the provider, dispatches tool
	// calls through the registry+hook pipeline, feeds results back, and
	// repeats until the model stops calling tools or MaxTurns is hit.
	fmt.Println("[3] Query: \"What time is it?\"")
	fmt.Println("--------------------------------------------------------")

	eventCh, err := sess.Query(ctx, loop.AgentInput{
		Prompt: "What time is it?",
		SessionID: "demo-session",
	})
	if err != nil {
		log.Fatalf("query: %v", err)
	}

	// 6. Drain the event channel, reacting to each event type.
	//
	// The event lifecycle for this example:
	// EventTurnStart -> a new turn began
	// EventTextDelta -> incremental assistant text
	// EventToolCallStart -> the model requested a tool call
	// EventToolCallResult -> the tool returned a result
	// EventTurnEnd -> the turn finished
	// EventCompleted -> the agent loop completed normally
	var fullText string
	var toolCalls, toolResults int
	for evt := range eventCh {
		switch evt.Type {
		case event.EventTurnStart:
			fmt.Printf(" >> Turn %s started\n", evt.TurnID)

		case event.EventTextDelta:
			// Payload is a string with the text increment.
			if delta, ok := evt.Payload.(string); ok {
				fullText += delta
				fmt.Print(delta)
			}

		case event.EventToolCallStart:
			toolCalls++
			// Payload is a *registry.ToolCall (ParallelToolExecutor form) or
			// *stream.ToolCall depending on the dispatch path; both carry a
			// Name field. We handle the common shapes.
			fmt.Printf("\n >> ToolCall started: %s\n", describeToolCall(evt.Payload))

		case event.EventToolCallResult:
			toolResults++
			// Payload is a *registry.ToolResult.
			if tr, ok := evt.Payload.(*registry.ToolResult); ok {
				fmt.Printf(" >> ToolResult: %s\n", truncate(tr.Content, 80))
			}

		case event.EventTurnEnd:
			fmt.Printf("\n >> Turn %s ended\n", evt.TurnID)

		case event.EventCompleted:
			fmt.Println("\n >> Agent completed")

		case event.EventError:
			fmt.Printf("\n >> ERROR: %v\n", evt.Error)

		case event.EventMaxTurnsReached:
			fmt.Println("\n >> MaxTurns reached")

		case event.EventCompactStart:
			fmt.Println(" >> Compaction started")

		case event.EventCompactEnd:
			fmt.Println(" >> Compaction ended")
		}
	}
	fmt.Println("--------------------------------------------------------")

	// 7. Print a summary of what happened.
	fmt.Println()
	fmt.Println("[4] Summary")
	fmt.Printf(" Final text : %q\n", fullText)
	fmt.Printf(" Tool calls : %d\n", toolCalls)
	fmt.Printf(" Tool results: %d\n", toolResults)
	fmt.Printf(" Agent status: %s\n", sess.Status())
	fmt.Println()
	fmt.Println("Done.")
}

// describeToolCall extracts a human-readable name from a tool-call event
// payload. The agent loop may emit either a *registry.ToolCall or a
// *stream.ToolCall depending on the dispatch path.
func describeToolCall(payload any) string {
	switch v := payload.(type) {
	case *registry.ToolCall:
		return fmt.Sprintf("%s(%v)", v.Name, v.Arguments)
	case *stream.ToolCall:
		return fmt.Sprintf("%s(%v)", v.Name, v.Arguments)
	case registry.ToolCall:
		return fmt.Sprintf("%s(%v)", v.Name, v.Arguments)
	case stream.ToolCall:
		return fmt.Sprintf("%s(%v)", v.Name, v.Arguments)
	default:
		return fmt.Sprintf("%v", payload)
	}
}
