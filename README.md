# go-agent-core

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **Experimental Software** — This project is under active, experimental development. Due to limited time and budget, it has not been thoroughly tested. Use at your own risk. Contributions and feedback are welcome.

An interface-driven AI Agent core SDK for Go. Every component is behind an interface — swap anything, keep the rest. Built on a strict layered architecture with permanent JSONL logging.

**[中文文档](README.zh-CN.md)**

---

## Relationship with go-cli

This project is the **core SDK** powering [go-cli](https://github.com/PengJunchen/go-cli), a full-featured AI Agent CLI framework. They serve different roles:

| Aspect | go-agent-core (this project) | [go-cli](https://github.com/PengJunchen/go-cli) |
|---|---|---|
| **Positioning** | Agent core SDK / library | Complete AI Agent CLI framework |
| **Usage** | `go get` import as dependency | Standalone CLI binary (`./bin/go-cli`) |
| **LLM Integration** | Eino framework adapter | Zero external dependencies (`net/http` native) |
| **Architecture** | 5-layer strict unidirectional (L0→L4) | Harness → Agent → AgentLoop (ReAct) |
| **Runtime** | Library embedded in host app | Owns the process: signal, config, tracing, dispatch |
| **Entry** | SDK API (`loop.NewBuilder().Build()`) | CLI (`cmd/cli`) with full TUI |
| **Focus** | Interface design, composability, testability | End-to-end agent experience, TUI, extensions |

In short: **go-agent-core** is the engine — you import it. **go-cli** is the car — you drive it.

---

## Three Principles

1. **Interface-Driven** — Every core capability is defined as an `interface`, fully replaceable
2. **Layered Architecture** — L0→L1→L2→L3→L4, strict unidirectional dependency, no reverse
3. **Permanent Logging** — `ExecLogger` cannot be disabled; users selectively extract via `LogExtractor`

---

## Architecture Overview

```
L0 cmd/ — Application entry (CLI/HTTP/A2A/ACP)
L1 agent/ — Agent engine (LoopAgent/Turn/Middleware/HITL/Event)
L2 capability/ — Capability system (ToolRegistry/Skill/MCP/ToolHook)
L3 memory/ — Memory & storage (Session/Context/Compactor/Store/Log)
L4 llm/ — LLM protocol (Provider/Stream/Transform/Registry/Adapter)
Cross-cutting production/ — Resilience (circuit breaker/loop detection/idempotency/security/audit/telemetry)
Cross-cutting verify/ — Verification (AST scan/log validation/leak detection/interface coverage)
```

```
┌──────────────────────────────────────────────────────────┐
│  L0  cmd/                                                │
│  go-agent-core (version + provider list)                 │
│  go-coding-agent (REPL / --print one-shot)               │
└──────────────┬───────────────────────────────────────────┘
               │
┌──────────────▼───────────────────────────────────────────┐
│  L1  agent/                                              │
│  LoopAgent ── Builder ── EventStream ── HITL             │
│  Session ── Middleware ── Orchestrate ── SubAgent        │
└──────┬───────┬──────────┬──────────┬─────────────────────┘
       │       │          │          │
  ┌────▼──┐ ┌──▼───┐ ┌───▼────┐ ┌──▼──────────┐
  │  L2   │ │  L3  │ │  L4    │ │ Production  │
  │Tools  │ │Memory│ │  LLM   │ │ + Verify    │
  │Skill  │ │Session│ │Eino Ad│ │ cross-cut   │
  │MCP    │ │Comp  │ │Proto   │ │             │
  └───────┘ └──────┘ └────────┘ └─────────────┘
```

### Layer Responsibilities & Key Interfaces

| Layer | Directory | Key Interfaces | Description |
|---|---|---|---|
| L0 | `cmd/` | — | Application entry (CLI/HTTP/A2A/ACP) |
| L1 | `agent/` | `LoopAgent`, `Middleware` | Agent engine: Turn loop, event stream, state machine, HITL |
| L2 | `capability/` | `ToolRegistry`, `ToolHook`, `MCPProvider`, `SkillProvider` | Capability system: tool registration/hooks/MCP/Skill |
| L3 | `memory/` | `ContextManager`, `Compactor`, `SessionManager`, `ExecLogger` | Memory & storage: context/compaction/session/logging |
| L4 | `llm/` | `ModelProvider`, `TransformPipeline`, `ProviderRegistry` | LLM protocol: Provider/Stream/Transform/Registry |
| Cross | `production/` | `ProductionBundle` | Circuit breaker, loop detection, idempotency, security, audit, telemetry |
| Cross | `verify/` | AST Scanner | Verification: dependency rules, log integrity, leak detection |

**Dependency constraint**: `llm/` has zero Eino concrete type imports (enforced by IFACE-001/002/003 AST rules), `memory/` may depend on `llm/provider`, `capability/` may depend on `memory/`, `agent/` may depend on L2/L3/L4.

---

## Quick Start

### Installation

```bash
go get github.com/pengjunchen/go-agent-core
```

### Minimal Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/cloudwego/eino-ext/components/model/openai"
    "github.com/pengjunchen/go-agent-core/agent/event"
    "github.com/pengjunchen/go-agent-core/agent/loop"
    "github.com/pengjunchen/go-agent-core/capability/registry"
    "github.com/pengjunchen/go-agent-core/llm/adapter/eino"
    ctxpkg "github.com/pengjunchen/go-agent-core/memory/context"
    "github.com/pengjunchen/go-agent-core/memory/log"
)

func main() {
    // 1. Create LLM Provider (via Eino adapter for OpenAI)
    chatModel, err := openai.NewChatModel(context.Background(), openai.Config{
        Model:   "gpt-4o",
        BaseURL: "https://api.openai.com/v1",
        APIKey:  os.Getenv("OPENAI_API_KEY"),
    })
    if err != nil {
        log.Fatal(err)
    }
    provider := eino.NewEinoProvider(chatModel, "openai", "gpt-4o", 128000)

    // 2. Create context manager
    ctxMgr := ctxpkg.NewHeuristicContextManager()

    // 3. Create tool registry (optional)
    toolReg := registry.NewDefaultToolRegistry()

    // 4. Build Agent
    agent, err := loop.NewBuilder().
        WithProvider(provider).
        WithContextManager(ctxMgr).
        WithToolRegistry(toolReg).
        WithMaxTurns(20).
        Build()
    if err != nil {
        log.Fatal(err)
    }
    defer func() { _ = agent.Close() }()

    // 5. Submit query and consume event stream
    eventCh, err := agent.Query(context.Background(), loop.AgentInput{
        Prompt: "Hello, introduce yourself",
    })
    if err != nil {
        log.Fatal(err)
    }

    for evt := range eventCh {
        switch evt.Type {
        case event.EventTextDelta:
            fmt.Print(evt.Payload.(string))
        case event.EventCompleted:
            fmt.Println("\n--- Done ---")
        case event.EventError:
            fmt.Printf("\nError: %v\n", evt.Error)
        }
    }
}
```

---

## Core Concepts

### LoopAgent

`LoopAgent` is the core scheduling interface, maintaining a sequential Turn loop: query → LLM inference → tool call → next turn, until the model stops calling tools or `MaxTurns` is reached.

```go
type LoopAgent interface {
    Query(ctx context.Context, input AgentInput) (<-chan event.AgentEvent, error)
    Interrupt(ctx context.Context) error   // Interrupt current Turn
    Steer(ctx context.Context, msg string) error  // Mid-turn direction change
    FollowUp(ctx context.Context, content string) error  // Post-completion append
    Status() event.AgentStatus
    Close() error
}
```

### Agent State Machine

```
Idle ──────────────────────→ Running
Running ──→ Completed / Error / WaitingApproval / Canceled
WaitingApproval ──→ Running / Canceled
Completed ──→ Running / Idle
Error ──→ Running / Idle
Canceled ──→ Idle / Running
```

### Event System

Consume events via `<-chan AgentEvent` returned by `Query()`:

| Event Type | Meaning |
|----------|------|
| `EventTurnStart` | Turn start |
| `EventTextDelta` | Text delta |
| `EventThinkingDelta` | Thinking delta |
| `EventToolCallStart` | Tool call start |
| `EventToolCallResult` | Tool execution result |
| `EventTurnEnd` | Turn end |
| `EventCompleted` | Query completed |
| `EventMaxTurnsReached` | Max turns reached |
| `EventToolLoopDetected` | Tool loop detected |
| `EventError` | Error |
| `EventCompactStart/End` | Context compaction |
| `EventApprovalRequest` | HITL approval request |

## Builder Configuration

`LoopAgentBuilder` provides a chainable API:

```go
agent, err := loop.NewBuilder().
    WithProvider(provider).           // Required: LLM Provider
    WithContextManager(ctxMgr).       // Required: context manager
    WithToolRegistry(toolReg).        // Required: tool registry
    WithHookPipeline(hookPipeline).   // Optional: ToolHook pipeline
    WithMiddlewareChain(mwChain).     // Optional: middleware chain
    WithLogger(execLogger).           // Optional: execution logger
    WithMaxTurns(30).                 // Optional: default 20
    WithRetryConfig(retryCfg).        // Optional: retry config
    WithCompactThreshold(80000).      // Optional: auto-compaction threshold
    WithPrepareNextTurn(swapFn).      // Optional: runtime Provider swap
    WithProduction(prodBundle).       // Optional: production bundle
    Build()
```

## Custom Tools

```go
toolReg := registry.NewDefaultToolRegistry()

err = toolReg.RegisterTool(context.Background(), registry.ToolDefinition{
    Name:        "search",
    Description: "Search knowledge base",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "query": map[string]any{
                "type":        "string",
                "description": "Search keyword",
            },
        },
        "required": []string{"query"},
    },
    Handler: func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
        q := args["query"].(string)
        return &registry.ToolResult{
            Content: fmt.Sprintf("Search result: %s", q),
        }, nil
    },
})
```

## ToolHook Pipeline

`ToolHook` provides Before/After dual hooks to intercept, modify, or block tool calls:

```go
hookPipeline := toolhook.NewHookPipeline()

// Register hook (lower priority runs Before first)
hookPipeline.Register(myHook, 10)

// Build Agent with custom pipeline
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithHookPipeline(hookPipeline).
    Build()
```

### HITL Approval

`ApprovalHook` enables human-in-the-loop approval, waiting for human decision before high-risk tool calls:

```go
hitl := loop.NewHITLManager(loop.ApprovalHandlerFunc(func(
    ctx context.Context,
    req *loop.ApprovalRequest,
) (loop.ApprovalDecision, error) {
    // Your approval logic: show tool name and params, wait for user confirmation
    fmt.Printf("Tool %s needs approval, args: %v\n", req.ToolName, req.Arguments)
    // Return ApprovalApprove / ApprovalDeny / ApprovalTimeout
    return loop.ApprovalApprove, nil
}), 30*time.Second) // 30s timeout

// Approval hooks are injected via HookPipeline
// ApprovalHook emits EventApprovalRequest before tool execution
```

During approval wait, Agent transitions to `WaitingApproval` state via `OnSuspend/OnResume` callbacks for observability.

## Runtime Provider Swap

Dynamically switch models during execution via `PrepareNextTurn` callback, no Agent restart needed:

```go
swapable := loop.NewSwapableProvider(initialProvider)

agent, _ := loop.NewBuilder().
    WithProvider(swapable).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithPrepareNextTurn(func(ctx context.Context, turnCount int) provider.ModelProvider {
        if turnCount > 5 {
            // Switch to stronger model for complex tasks
            return strongerProvider
        }
        return nil // nil means no swap
    }).
    Build()
```

## Production Bundle (ProductionBundle)

`ProductionBundle` aggregates six production capabilities into a single injection point. All fields are optional (nil = disabled):

```go
bundle := production.NewProductionBundle(
    production.WithLoopDetector(production.NewDefaultLoopDetector(production.LoopDetectorConfig{
        ConsecutiveThreshold: 3,   // >= 3 consecutive identical calls = loop
        WindowSize:           10,   // sliding window size
        ArgumentComparison:   true, // compare arguments
    })),
    production.WithCircuitBreaker(production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
        FailureThreshold: 5,        // trip after 5 failures
        SuccessThreshold: 3,        // recover after 3 successes
        Timeout:          30 * time.Second, // open circuit wait
        HalfOpenMaxReqs:  1,        // max requests in half-open
    })),
    production.WithSecurityGuard(production.NewConfigSecurityGuard(production.SecurityGuardConfig{
        BlockedTools: map[string]bool{"exec": true, "delete": true}, // blocklist
        // Or use allowlist mode:
        // AllowedTools: map[string]bool{"search": true, "read": true},
    })),
    production.WithAuditLogger(production.NewLogAuditLogger(execLogger)),
    production.WithIdempotencyKey(production.NewMemoryIdempotencyKey()),
    production.WithTelemetryCollector(production.NewStdoutTelemetryCollector(nil)),
)

agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithProduction(bundle).
    Build()
```

### Component Summary

| Component | Purpose | Integration Point |
|------|------|--------|
| **LoopDetector** | Detect consecutive identical tool calls, prevent infinite loops | Records and detects after tool execution, triggers `EventToolLoopDetected` |
| **CircuitBreaker** | Three-state machine (Closed→Open→HalfOpen), prevent LLM call cascading failures | Wraps LLM StreamChat calls |
| **SecurityGuard** | Validate tool calls against allow/block lists | Validates before tool execution, returns error event when blocked |
| **AuditLogger** | Record tool call audit events, support querying | Records after tool execution |
| **IdempotencyKey** | Idempotency check, return cached result for duplicates | Checks before execution, records after |
| **TelemetryCollector** | Collect metrics and tracing spans | Writes to stderr (replaceable Writer) |

### nil Safety

When ProductionBundle is not configured or only partially configured, Agent keeps default behavior unchanged. All checkpoints are nil-guarded:

```go
// No production components — fully equivalent to default behavior
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    Build()

// Only LoopDetector — other components disabled
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithProduction(production.NewProductionBundle(
        production.WithLoopDetector(production.NewDefaultLoopDetector(production.DefaultLoopDetectorConfig())),
    )).
    Build()
```

## Execution Logging

`ExecLogger` always writes to JSONL files and cannot be disabled (principle 3). Use `LogExtractor` for selective extraction:

```go
// Create logger
execLogger, err := log.NewJSONLExecLogger("./logs", log.DefaultLogConfig())
if err != nil {
    log.Fatal(err)
}

// Inject into Agent
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithLogger(execLogger).
    Build()

// Extract logs
extractor := log.NewJSONLLogExtractor("./logs")
entries, err := extractor.Extract(log.LogFilter{
    Categories: []log.LogCategory{log.CategoryLLM, log.CategoryTool},
    SessionID:  "session-xxx",
    Limit:      100,
})
```

### Three-Track JSONL Logging

ExecLogger splits logs into three tracks, each with a clear purpose:

```
<logDir>/
├── sessions/<ts>_<uuid>.jsonl   — session tree (branchable, compaction checkpoints)
├── runs/<sessionID>.jsonl       — execution trace (turn/item-level audit)
└── events/<sessionID>.jsonl     — event stream raw (UI replay, observability)
```

| Track | Write Method | Content | Purpose |
|------|----------|----------|------|
| **sessions** | `LogSession()` | Session entries (message/branch/compaction/label) | Rebuild session tree after kill-9 |
| **runs** | `LogTurn()` / `LogItem()` | turn_start/turn_end + llm_call/tool_call/tool_result/interrupt/steer | Audit every inference & tool call with input/output and latency |
| **events** | `LogEvent()` | text_delta/thinking_delta/tool_call_start/tool_call_result/done/error | Event stream replay (UI redraw, observability) |

The envelope mechanism (`LogEnvelope`) supports track and category-filtered extraction, with `ParseAsTurnRecord()` / `ParseAsItemRecord()` / `ParseAsEventRecord()` / `ParseAsSessionRecord()` chained deserialization.

## Automatic Context Compaction

Auto-triggers when conversation token count exceeds threshold:

```go
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithCompactThreshold(80000). // auto-compact when tokens > 80K
    Build()
```

Compaction strategy prefers SummaryCompactor (calls LLM to generate summary), falls back to TruncatingCompactor (truncate old messages) on failure.

## Verification Framework

```bash
# Full verification pipeline
make verify

# Includes:
# 1. go build ./...
# 2. go test -race ./...
# 3. Log integrity tests
# 4. Goroutine leak detection
# 5. V* rule verification tests
# 6. E2E scenario tests (S-01 ~ S-16)

# AST scan only
make scan

# Single E2E scenario
make test-e2e-scenario SCENARIO=S-01

# Coverage check (threshold 73%)
make test-cov-check
```

### All Make Targets

| Target | Description |
|------|------|
| `make build` | Compile |
| `make test` | Basic unit tests |
| `make test-race` | Race detection tests |
| `make test-cov` | Coverage tests |
| `make test-cov-check` | Coverage check (fail below 73%) |
| `make test-vrules` | V* rule specific tests |
| `make test-integration` | Integration tests (no LLM) |
| `make test-e2e` | E2E tests (S-01~S-15) |
| `make test-e2e-verbose` | E2E verbose output |
| `make test-e2e-scenario` | Single scenario E2E (requires SCENARIO=) |
| `make test-log` | Log integrity tests |
| `make test-leak` | Goroutine leak detection |
| `make scan` | AST scan |
| `make verify` | Full verification |
| `make report` | Generate report |
| `make clean` | Clean |

### AST Scan Rules

| Rule | Detection | Severity |
|------|----------|--------|
| IFACE-001 | Interface layer must not import llm/adapter/ | Error |
| IFACE-002 | Interface layer must not import eino | Error |
| IFACE-003 | agent/ must not import llm/adapter/ | Error |
| SCAN-010 | Hardcoded Provider routing | Error |
| SCAN-011 | Tool event leakage | Error |
| SCAN-012 | Logger bypass detection | Error |
| SCAN-013 | Interface implementation completeness | Warning |

## Session Management

`Session` is the top-level facade implementing `LoopAgent` and additionally exposing internal components:

```go
// Auto-assemble from Settings (recommended)
agent, err := session.NewBuilderFromSettings(settings).
    Build()

// Manual assembly
agent, err := session.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithMCPServers([]mcp.MCPServerConfig{...}).
    Build()
```

Session extra methods:
- `ContextManager()` — get context manager
- `ToolRegistry()` — get tool registry
- `Provider()` — get current Provider
- `MCPServers()` — get MCP server configs

Session persistence via `SessionManager` (hot-path CRUD) and `SessionSink` (cold-path append-only + kill-9 recovery):

```go
store := session.NewJSONLSessionStore("./data/sessions")
sink := session.NewJSONLSessionSink("./data/sessions")
store.SetSink(sink) // delegate persistence
```

## MCP Remote Tools

`MCPProvider` supports stdio / SSE / Streamable HTTP transports:

```go
mcpProvider := mcp.NewMCPProvider()
refs, cleanups, errs := mcpProvider.Connect(ctx, []mcp.MCPServerConfig{
    {Name: "my-server", Type: "stdio", Command: "my-mcp-server"},
    {Name: "remote", Type: "sse", URL: "http://localhost:8080/sse"},
})
defer func() { _ = mcpProvider.Disconnect() }()

// Call remote tool
result, err := mcpProvider.Call(ctx, "my-server", "search", json.RawMessage(`{"q":"hello"}`))
```

## E2E Test Scenarios

The project includes 16 end-to-end test scenarios driven by Mock LLM Server and Mock MCP Server:

| ID | Scenario | Verified Features |
|----|------|----------|
| S-01 | Simple text conversation | StreamChat, event stream, state machine |
| S-02 | Single tool call | ToolCall, ToolRegistry, Turn loop |
| S-03 | Multi-turn tool calls | Multi-turn tool loop, ContextManager |
| S-04 | Thinking mode | ThinkingDelta, ThinkingConfig |
| S-05 | HookPipeline interception | ApprovalHook, Middleware |
| S-06 | 429 error retry | Transient error retry |
| S-07 | 500 non-transient error | Non-transient no retry |
| S-08 | Interrupt/MaxTurns | Intervention mechanism |
| S-09 | Context compaction | Compactor, TokenEstimator |
| S-10 | MCP remote tools & Skill | MCPProvider, SkillProvider |
| S-11~S-15 | Extended scenarios | Parallel tools, multi-Provider, sub-Agent, etc. |

## LLM Protocol Layer

### ModelProvider

`ModelProvider` is the core abstraction for LLM calls, with zero Eino dependencies at the interface level:

```go
type ModelProvider interface {
    StreamChat(ctx context.Context, messages []message.Message, opts *ChatOptions) (<-chan stream.StreamEvent, error)
    Generate(ctx context.Context, messages []message.Message, opts *ChatOptions) (*message.Message, error)
    ModelInfo() *ModelInfo
}
```

`ChatOptions` supports Temperature / MaxTokens / StopSequences / ThinkingMode / ToolChoice / Tools / ResponseFormat etc. `ModelInfo` returns provider name, model name, token limits, capability flags (Streaming/Thinking/Vision), and cost info.

### Provider Registry

`ProviderRegistry` replaces hardcoded if-else routing, supports runtime registration and replacement:

```go
registry := llm.NewProviderRegistry()

// Register factory
registry.RegisterProvider("openai", func(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
    chatModel, _ := openai.NewChatModel(ctx, openai.Config{
        Model:   cfg.Model,
        BaseURL: cfg.BaseURL,
        APIKey:  cfg.APIKey,
    })
    return eino.NewEinoProvider(chatModel, "openai", cfg.Model, 128000), nil
})

// Get instance by name
p, err := registry.GetProvider("openai", &registry.ProviderConfig{
    Model:  "gpt-4o",
    APIKey: os.Getenv("OPENAI_API_KEY"),
})
```

### Message Transform Pipeline

`TransformPipeline` supports cross-Provider message format adaptation:

```go
pipeline := transform.NewPipeline()
pipeline.Add(transform.NormalizeToolCallIDs)    // Clean/truncate ToolCall IDs
pipeline.Add(transform.ImageDowngrade)          // Downgrade images to text when vision unsupported
pipeline.Add(transform.ThinkingBlockAdapter)    // OpenAI/Anthropic thinking block interop

msgs, err := pipeline.Execute(ctx, messages, "anthropic")
```

Built-in transforms: `NormalizeToolCallIDs` / `ImageDowngrade` / `ThinkingBlockAdapter` / `ToolCallIDNormalizer` / `ImageFormatAdapter` / `SystemMessageAdapter`

### Auth Abstraction

`TokenSource` unified authentication, supporting static API key, OAuth2 refresh, and fallback:

```go
// Static API key
ts := auth.NewStaticTokenSource("sk-xxx")

// OAuth2 auto-refresh
ts := auth.NewOAuthTokenSource(oauthConfig, refreshToken)

// Fallback when primary fails
ts := auth.NewFallbackTokenSource(primary, fallback)
```

## Interface Replacement

All core components are defined via interfaces, freely replaceable:

```go
// Custom ContextManager
type MyContextManager struct{}
func (m *MyContextManager) GetMessages(ctx context.Context) ([]ctxpkg.TurnItem, error) { ... }
func (m *MyContextManager) RecordItem(ctx context.Context, item ctxpkg.TurnItem) error { ... }
// ... implement other methods

agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(&MyContextManager{}).
    WithToolRegistry(toolReg).
    Build()
```

## Project Structure

```
go-agent-core/
├── agent/                    # L1 Agent engine layer
│   ├── event/                # Event types (17) & state machine (6 states)
│   ├── loop/                 # LoopAgent / Builder / Generator / HITL / Approval
│   ├── middleware/            # BeforeTurn/AfterTurn/BeforeCompact/AfterCompact chain
│   ├── orchestrate/          # Orchestrator (multi-Agent coordination)
│   ├── session/              # Session facade + Builder (incl. NewBuilderFromSettings)
│   ├── sharedctx/            # Cross-component shared context
│   └── subagent/             # Sub-Agent registry
├── capability/               # L2 Capability system layer
│   ├── registry/             # ToolRegistry + ParallelExecutor + DeferredLoader
│   ├── toolhook/             # HookPipeline (Before/After dual hooks + ArgumentsPreparer)
│   ├── skill/                # SkillProvider (SKILL.md loading + gitignore filtering)
│   ├── mcp/                  # MCPProvider (stdio/SSE/HTTP transports)
│   ├── extension/            # ExtensionRunner (BeforeProviderRequest/AfterProviderResponse)
│   └── tools/                # Built-in tools (read/write/edit/grep/glob/ls/execute/web_fetch/image_view)
├── memory/                   # L3 Memory & storage layer
│   ├── context/              # ContextManager + HeuristicContextManager + Compactor
│   ├── compactor/            # Truncating/Summary/MicroCompactor + TokenEstimator
│   ├── session/              # SessionManager + JSONLSessionStore + JSONLSessionSink
│   └── log/                  # ExecLogger + JSONLExecLogger + LogExtractor + LogSelector
├── llm/                      # L4 LLM protocol layer
│   ├── provider/             # ModelProvider interface + SwapableProvider + LazyProvider
│   ├── stream/               # StreamEvent types + BoundedChannel
│   ├── transform/            # TransformPipeline + 6 built-in transforms
│   ├── registry/             # ProviderRegistry factory registry
│   ├── adapter/eino/         # Eino adapter (OpenAI/Anthropic/Gemini/DeepSeek)
│   ├── auth/                 # TokenSource (Static/OAuth2/Fallback) + CredentialStore
│   ├── catalog/              # Model catalog (catalog.json)
│   └── message/              # Message/Content/ToolCall/Usage types
├── production/               # Cross-cutting: production components
│   ├── CircuitBreaker        # Three-state circuit breaker (Closed→Open→HalfOpen)
│   ├── LoopDetector          # Consecutive identical tool call detection
│   ├── SecurityGuard         # Tool call security validation (allowlist/blocklist)
│   ├── AuditLogger           # Audit logging (tool calls/approval/data access)
│   ├── IdempotencyKey        # Idempotency check
│   └── TelemetryCollector    # Metrics + tracing spans
├── verify/                   # Cross-cutting: verification framework
│   └── scanner               # AST scan (IFACE-001~003, SCAN-010~013)
├── config/                   # Config loading & merging
├── prompt/                   # PromptBuilder + ToolRegistryReader
├── cmd/                      # L0 Application entry
├── examples/sdk/             # SDK examples (basic_usage/custom_tool/streaming/full_agent)
└── e2e_testing/              # E2E test infrastructure
```

## Commit Convention

```
<type>(<scope>): <description>
```

type: feat / fix / refactor / test / docs / chore
scope: llm / memory / capability / agent / production / verify / cmd

## License

[MIT](LICENSE)
