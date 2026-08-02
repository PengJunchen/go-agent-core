# go-agent-core

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

> **实验性软件** — 本项目处于活跃的实验性开发阶段。由于时间和经费有限，尚未经过充分测试。风险自负，欢迎贡献和反馈。

接口驱动的 AI Agent 核心 Go SDK。所有组件均通过接口定义——替换任意组件，其余保持不变。基于严格分层架构，日志永驻 JSONL。

**[English](README.md)**

---

## 与 go-cli 的关系

本项目是为 [go-cli](https://github.com/PengJunchen/go-cli)（一个功能完整的 AI Agent CLI 框架）提供动力的**核心 SDK**。两者定位不同：

| 维度 | go-agent-core（本项目） | [go-cli](https://github.com/PengJunchen/go-cli) |
|---|---|---|
| **定位** | Agent 核心 SDK / 库 | 完整 AI Agent CLI 框架 |
| **使用方式** | `go get` 导入为依赖 | 独立 CLI 二进制 (`./bin/go-cli`) |
| **LLM 集成** | Eino 框架适配器 | 零外部依赖 (`net/http` 原生) |
| **架构** | 5 层严格单向 (L0→L4) | Harness → Agent → AgentLoop (ReAct) |
| **运行时** | 嵌入宿主程序的库 | 掌控进程：信号、配置、追踪、分发 |
| **入口** | SDK API (`loop.NewBuilder().Build()`) | CLI (`cmd/cli`) + 完整 TUI |
| **侧重点** | 接口设计、可组合性、可测试性 | 端到端 Agent 体验、TUI、扩展系统 |

简言之：**go-agent-core** 是引擎——你导入它。**go-cli** 是整车——你驾驶它。

---

## 三原则

1. **接口驱动** — 所有核心能力通过 `interface` 定义，可替换
2. **分层架构** — L0→L1→L2→L3→L4 严格单向依赖，禁止反向
3. **日志永驻** — `ExecLogger` 不可关闭，用户通过 `LogExtractor` 选择性取走

---

## 架构概览

```
L0 cmd/ — 应用入口层（CLI/HTTP/A2A/ACP）
L1 agent/ — Agent 引擎层（LoopAgent/Turn/Middleware/HITL/Event）
L2 capability/ — 能力系统层（ToolRegistry/Skill/MCP/ToolHook）
L3 memory/ — 记忆与存储层（Session/Context/Compactor/Store/Log）
L4 llm/ — LLM 协议层（Provider/Stream/Transform/Registry/Adapter）
横切 production/ — 生产化组件（熔断/循环检测/幂等/安全/审计/遥测）
横切 verify/ — 校验框架（AST 扫描/日志验证/泄漏检测/接口覆盖率）
```

```
┌──────────────────────────────────────────────────────────┐
│  L0  cmd/                                                │
│  go-agent-core (版本号 + Provider 列表)                   │
│  go-coding-agent (REPL / --print 单轮)                   │
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
  │Skill  │ │Session│ │Eino Ad│ │ 横切组件    │
  │MCP    │ │Comp  │ │Proto   │ │             │
  └───────┘ └──────┘ └────────┘ └─────────────┘
```

### 各层职责与核心接口

| 层 | 目录 | 核心接口 | 说明 |
|----|------|----------|------|
| L0 | `cmd/` | — | 应用入口（CLI/HTTP/A2A/ACP） |
| L1 | `agent/` | `LoopAgent`, `Middleware` | Agent 引擎：Turn 循环、事件流、状态机、HITL |
| L2 | `capability/` | `ToolRegistry`, `ToolHook`, `MCPProvider`, `SkillProvider` | 能力系统：工具注册/钩子/MCP/Skill |
| L3 | `memory/` | `ContextManager`, `Compactor`, `SessionManager`, `ExecLogger` | 记忆存储：上下文/压缩/会话/日志 |
| L4 | `llm/` | `ModelProvider`, `TransformPipeline`, `ProviderRegistry` | LLM 协议：Provider/Stream/转换/注册 |
| 横切 | `production/` | `ProductionBundle` | 熔断/循环检测/幂等/安全/审计/遥测 |
| 横切 | `verify/` | AST Scanner | 校验：依赖规则/日志完整性/泄漏检测 |

**依赖约束**：`llm/` 零 Eino 具体类型依赖（IFACE-001/002/003 AST 规则），`memory/` 可依赖 `llm/provider`，`capability/` 可依赖 `memory/`，`agent/` 可依赖 L2/L3/L4。

---

## 快速开始

### 安装

```bash
go get github.com/pengjunchen/go-agent-core
```

### 最小示例

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
    // 1. 创建 LLM Provider（通过 Eino 适配 OpenAI）
    chatModel, err := openai.NewChatModel(context.Background(), openai.Config{
        Model:   "gpt-4o",
        BaseURL: "https://api.openai.com/v1",
        APIKey:  os.Getenv("OPENAI_API_KEY"),
    })
    if err != nil {
        log.Fatal(err)
    }
    provider := eino.NewEinoProvider(chatModel, "openai", "gpt-4o", 128000)

    // 2. 创建上下文管理器
    ctxMgr := ctxpkg.NewHeuristicContextManager()

    // 3. 创建工具注册表（可选）
    toolReg := registry.NewDefaultToolRegistry()

    // 4. 构建 Agent
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

    // 5. 提交查询并消费事件流
    eventCh, err := agent.Query(context.Background(), loop.AgentInput{
        Prompt: "你好，介绍一下你自己",
    })
    if err != nil {
        log.Fatal(err)
    }

    for evt := range eventCh {
        switch evt.Type {
        case event.EventTextDelta:
            fmt.Print(evt.Payload.(string))
        case event.EventCompleted:
            fmt.Println("\n--- 完成 ---")
        case event.EventError:
            fmt.Printf("\n错误: %v\n", evt.Error)
        }
    }
}
```

---

## 核心概念

### LoopAgent

`LoopAgent` 是核心调度接口，维护串行 Turn 循环：query → LLM 推理 → 工具调用 → 下一轮，直到模型不再调用工具或达到 MaxTurns。

```go
type LoopAgent interface {
    Query(ctx context.Context, input AgentInput) (<-chan event.AgentEvent, error)
    Interrupt(ctx context.Context) error   // 中断当前 Turn
    Steer(ctx context.Context, msg string) error  // 中途调整方向
    FollowUp(ctx context.Context, content string) error  // 完成后追加
    Status() event.AgentStatus
    Close() error
}
```

### Agent 状态机

```
Idle ──────────────────────→ Running
Running ──→ Completed / Error / WaitingApproval / Canceled
WaitingApproval ──→ Running / Canceled
Completed ──→ Running / Idle
Error ──→ Running / Idle
Canceled ──→ Idle / Running
```

### 事件系统

通过 `Query()` 返回的 `<-chan AgentEvent` 消费事件流：

| 事件类型 | 含义 |
|----------|------|
| `EventTurnStart` | Turn 开始 |
| `EventTextDelta` | 文本增量 |
| `EventThinkingDelta` | 思维增量 |
| `EventToolCallStart` | 工具调用开始 |
| `EventToolCallResult` | 工具执行结果 |
| `EventTurnEnd` | Turn 结束 |
| `EventCompleted` | 查询完成 |
| `EventMaxTurnsReached` | 达到最大轮次 |
| `EventToolLoopDetected` | 工具循环检测 |
| `EventError` | 错误 |
| `EventCompactStart/End` | 上下文压缩 |
| `EventApprovalRequest` | HITL 审批请求 |

## Builder 配置

`LoopAgentBuilder` 提供链式 API：

```go
agent, err := loop.NewBuilder().
    WithProvider(provider).           // 必填：LLM Provider
    WithContextManager(ctxMgr).       // 必填：上下文管理器
    WithToolRegistry(toolReg).        // 必填：工具注册表
    WithHookPipeline(hookPipeline).   // 可选：ToolHook 管道
    WithMiddlewareChain(mwChain).     // 可选：中间件链
    WithLogger(execLogger).           // 可选：执行日志
    WithMaxTurns(30).                 // 可选：默认 20
    WithRetryConfig(retryCfg).        // 可选：重试配置
    WithCompactThreshold(80000).      // 可选：自动压缩阈值
    WithPrepareNextTurn(swapFn).      // 可选：运行时 Provider 切换
    WithProduction(prodBundle).       // 可选：生产化组件
    Build()
```

## 自定义工具

```go
toolReg := registry.NewDefaultToolRegistry()

err = toolReg.RegisterTool(context.Background(), registry.ToolDefinition{
    Name:        "search",
    Description: "搜索知识库",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "query": map[string]any{
                "type":        "string",
                "description": "搜索关键词",
            },
        },
        "required": []string{"query"},
    },
    Handler: func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
        q := args["query"].(string)
        return &registry.ToolResult{
            Content: fmt.Sprintf("搜索结果: %s", q),
        }, nil
    },
})
```

## ToolHook 管道

`ToolHook` 提供 Before/After 双钩子，可拦截、修改或阻止工具调用：

```go
hookPipeline := toolhook.NewHookPipeline()

// 注册钩子（priority 越小越先执行 Before）
hookPipeline.Register(myHook, 10)

// 使用自定义管道构建 Agent
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithHookPipeline(hookPipeline).
    Build()
```

### HITL 审批

`ApprovalHook` 实现人机协同审批，高风险工具调用前等待人工决策：

```go
hitl := loop.NewHITLManager(loop.ApprovalHandlerFunc(func(
    ctx context.Context,
    req *loop.ApprovalRequest,
) (loop.ApprovalDecision, error) {
    // 你的审批逻辑：展示工具名和参数，等待用户确认
    fmt.Printf("工具 %s 需要审批，参数: %v\n", req.ToolName, req.Arguments)
    // 返回 ApprovalApprove / ApprovalDeny / ApprovalTimeout
    return loop.ApprovalApprove, nil
}), 30*time.Second) // 30秒超时

// 审批钩子需要通过 HookPipeline 注入
// ApprovalHook 在工具调用前自动发射 EventApprovalRequest 事件
```

审批等待期间，Agent 会通过 `OnSuspend/OnResume` 回调转换到 `WaitingApproval` 状态，提升可观测性。

## 运行时 Provider 切换

通过 `PrepareNextTurn` 回调实现动态模型切换，无需重启 Agent：

```go
swapable := loop.NewSwapableProvider(initialProvider)

agent, _ := loop.NewBuilder().
    WithProvider(swapable).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithPrepareNextTurn(func(ctx context.Context, turnCount int) provider.ModelProvider {
        if turnCount > 5 {
            // 切换到更强的模型处理复杂任务
            return strongerProvider
        }
        return nil // nil 表示不切换
    }).
    Build()
```

## 生产化组件 (ProductionBundle)

`ProductionBundle` 将六大生产化能力聚合为单一注入点，所有字段可选（nil = 禁用）：

```go
bundle := production.NewProductionBundle(
    production.WithLoopDetector(production.NewDefaultLoopDetector(production.LoopDetectorConfig{
        ConsecutiveThreshold: 3,   // 连续相同调用 >=3 次判定为循环
        WindowSize:           10,   // 滑动窗口大小
        ArgumentComparison:   true, // 比较参数
    })),
    production.WithCircuitBreaker(production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
        FailureThreshold: 5,        // 5次失败后熔断
        SuccessThreshold: 3,        // 3次成功后恢复
        Timeout:          30 * time.Second, // 熔断等待时间
        HalfOpenMaxReqs:  1,        // 半开状态最大请求数
    })),
    production.WithSecurityGuard(production.NewConfigSecurityGuard(production.SecurityGuardConfig{
        BlockedTools: map[string]bool{"exec": true, "delete": true}, // 黑名单
        // 或使用白名单模式：
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

### 各组件作用

| 组件 | 作用 | 集成点 |
|------|------|--------|
| **LoopDetector** | 检测连续相同工具调用，防止死循环 | 工具执行后记录并检测，触发 `EventToolLoopDetected` |
| **CircuitBreaker** | 三态机（Closed→Open→HalfOpen），防止 LLM 调用雪崩 | 包裹 LLM StreamChat 调用 |
| **SecurityGuard** | 基于允许/阻止列表校验工具调用 | 工具执行前校验，阻止时返回错误事件 |
| **AuditLogger** | 记录工具调用审计事件，支持查询 | 工具执行后记录 |
| **IdempotencyKey** | 幂等检查，重复调用返回缓存结果 | 工具执行前检查，执行后记录 |
| **TelemetryCollector** | 采集指标和链路追踪 Span | 写入 stderr（可替换为其他 Writer） |

### nil 安全

不配置 ProductionBundle 或仅配置部分组件时，Agent 保持默认行为不变。所有检查点均通过 nil 检查保护：

```go
// 不使用生产化组件——完全等价于默认行为
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    Build()

// 仅使用 LoopDetector——其他组件禁用
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithProduction(production.NewProductionBundle(
        production.WithLoopDetector(production.NewDefaultLoopDetector(production.DefaultLoopDetectorConfig())),
    )).
    Build()
```

## 执行日志

`ExecLogger` 永远写入 JSONL 文件，不可关闭（P3 原则）。通过 `LogExtractor` 选择性提取：

```go
// 创建日志记录器
execLogger, err := log.NewJSONLExecLogger("./logs", log.DefaultLogConfig())
if err != nil {
    log.Fatal(err)
}

// 注入 Agent
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithLogger(execLogger).
    Build()

// 提取日志
extractor := log.NewJSONLLogExtractor("./logs")
entries, err := extractor.Extract(log.LogFilter{
    Categories: []log.LogCategory{log.CategoryLLM, log.CategoryTool},
    SessionID:  "session-xxx",
    Limit:      100,
})
```

### 三轨 JSONL 日志

ExecLogger 将日志分为三条轨道，各有明确用途：

```
<logDir>/
├── sessions/<ts>_<uuid>.jsonl   — 会话树（可分支、compaction 检查点）
├── runs/<sessionID>.jsonl       — 执行轨迹（turn/item 级审计）
└── events/<sessionID>.jsonl     — 事件流原样（UI 重放、观测）
```

| 轨道 | 写入方法 | 记录内容 | 用途 |
|------|----------|----------|------|
| **sessions** | `LogSession()` | 会话 entry（message/branch/compaction/label） | kill-9 后可重建会话树 |
| **runs** | `LogTurn()` / `LogItem()` | turn_start/turn_end + llm_call/tool_call/tool_result/interrupt/steer | 审计每次推理与工具调用的输入输出和耗时 |
| **events** | `LogEvent()` | text_delta/thinking_delta/tool_call_start/tool_call_result/done/error | 事件流重放（UI 重绘、观测） |

信封机制（`LogEnvelope`）支持按轨道和类别过滤提取，并提供 `ParseAsTurnRecord()` / `ParseAsItemRecord()` / `ParseAsEventRecord()` / `ParseAsSessionRecord()` 方法链反序列化。

## 自动上下文压缩

当对话 token 数超过阈值时自动触发压缩：

```go
agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithCompactThreshold(80000). // token 超过 80K 时自动压缩
    Build()
```

压缩策略优先使用 SummaryCompactor（调用 LLM 生成摘要），失败时回退到 TruncatingCompactor（截断旧消息）。

## 校验框架

```bash
# 完整验证流水线
make verify

# 包含：
# 1. go build ./...
# 2. go test -race ./...
# 3. 日志完整性测试
# 4. Goroutine 泄漏检测
# 5. V* 规则验证测试
# 6. E2E 场景测试（S-01 ~ S-16）

# 仅运行 AST 扫描
make scan

# 运行单个 E2E 场景
make test-e2e-scenario SCENARIO=S-01

# 覆盖率检查（阈值 73%）
make test-cov-check
```

### 全部 Make 目标

| 目标 | 说明 |
|------|------|
| `make build` | 编译 |
| `make test` | 基础单元测试 |
| `make test-race` | 竞态检测测试 |
| `make test-cov` | 覆盖率测试 |
| `make test-cov-check` | 覆盖率检查（低于 73% 失败） |
| `make test-vrules` | V* 规则专项测试 |
| `make test-integration` | 集成测试（无 LLM） |
| `make test-e2e` | E2E 测试（S-01~S-15） |
| `make test-e2e-verbose` | E2E 详细输出 |
| `make test-e2e-scenario` | 单场景 E2E（需 SCENARIO= 参数） |
| `make test-log` | 日志完整性测试 |
| `make test-leak` | Goroutine 泄漏检测 |
| `make scan` | AST 扫描 |
| `make verify` | 全量校验 |
| `make report` | 生成报告 |
| `make clean` | 清理 |

### AST 扫描规则

| 规则 | 检测内容 | 严重度 |
|------|----------|--------|
| IFACE-001 | 接口层不得 import llm/adapter/ | Error |
| IFACE-002 | 接口层不得 import eino | Error |
| IFACE-003 | agent/ 不得 import llm/adapter/ | Error |
| SCAN-010 | Provider 路由硬编码 | Error |
| SCAN-011 | 工具事件泄露 | Error |
| SCAN-012 | 日志绕过检测 | Error |
| SCAN-013 | 接口实现完整性 | Warning |

## Session 管理

`Session` 是顶层门面，实现 `LoopAgent` 接口并额外暴露内部组件：

```go
// 从 Settings 自动组装（推荐）
agent, err := session.NewBuilderFromSettings(settings).
    Build()

// 手动组装
agent, err := session.NewBuilder().
    WithProvider(provider).
    WithContextManager(ctxMgr).
    WithToolRegistry(toolReg).
    WithMCPServers([]mcp.MCPServerConfig{...}).
    Build()
```

Session 额外方法：
- `ContextManager()` — 获取上下文管理器
- `ToolRegistry()` — 获取工具注册表
- `Provider()` — 获取当前 Provider
- `MCPServers()` — 获取 MCP 服务器配置

会话持久化通过 `SessionManager`（热路径 CRUD）和 `SessionSink`（冷路径 append-only + kill-9 恢复）协作：

```go
store := session.NewJSONLSessionStore("./data/sessions")
sink := session.NewJSONLSessionSink("./data/sessions")
store.SetSink(sink) // 委托持久化
```

## MCP 远程工具

`MCPProvider` 支持 stdio / SSE / Streamable HTTP 三种传输方式：

```go
mcpProvider := mcp.NewMCPProvider()
refs, cleanups, errs := mcpProvider.Connect(ctx, []mcp.MCPServerConfig{
    {Name: "my-server", Type: "stdio", Command: "my-mcp-server"},
    {Name: "remote", Type: "sse", URL: "http://localhost:8080/sse"},
})
defer func() { _ = mcpProvider.Disconnect() }()

// 调用远程工具
result, err := mcpProvider.Call(ctx, "my-server", "search", json.RawMessage(`{"q":"hello"}`))
```

## E2E 测试场景

项目包含 16 个端到端测试场景，通过 Mock LLM Server 和 Mock MCP Server 驱动：

| ID | 场景 | 验证功能 |
|----|------|----------|
| S-01 | 简单文本对话 | StreamChat, 事件流, 状态机 |
| S-02 | 单工具调用 | ToolCall, ToolRegistry, Turn 循环 |
| S-03 | 多轮工具调用 | 多 Turn 工具循环, ContextManager |
| S-04 | Thinking 思维模式 | ThinkingDelta, ThinkingConfig |
| S-05 | HookPipeline 拦截 | ApprovalHook, Middleware |
| S-06 | 429 错误重试 | 瞬态错误重试 |
| S-07 | 500 非瞬态错误 | 非瞬态不重试 |
| S-08 | Interrupt/MaxTurns | 干预机制 |
| S-09 | 上下文压缩 | Compactor, TokenEstimator |
| S-10 | MCP 远程工具与 Skill | MCPProvider, SkillProvider |
| S-11~S-15 | 扩展场景 | 并行工具、多 Provider、子 Agent 等 |

## LLM 协议层

### ModelProvider

`ModelProvider` 是 LLM 调用的核心抽象，接口层零 Eino 依赖：

```go
type ModelProvider interface {
    StreamChat(ctx context.Context, messages []message.Message, opts *ChatOptions) (<-chan stream.StreamEvent, error)
    Generate(ctx context.Context, messages []message.Message, opts *ChatOptions) (*message.Message, error)
    ModelInfo() *ModelInfo
}
```

`ChatOptions` 支持 Temperature / MaxTokens / StopSequences / ThinkingMode / ToolChoice / Tools / ResponseFormat 等完整参数。`ModelInfo` 返回 Provider 名称、模型名、Token 限制、能力标记（Streaming/Thinking/Vision）和成本信息。

### Provider 注册表

`ProviderRegistry` 替代硬编码 if-else 路由，支持运行时注册和替换：

```go
registry := llm.NewProviderRegistry()

// 注册工厂
registry.RegisterProvider("openai", func(cfg *registry.ProviderConfig) (provider.ModelProvider, error) {
    chatModel, _ := openai.NewChatModel(ctx, openai.Config{
        Model:   cfg.Model,
        BaseURL: cfg.BaseURL,
        APIKey:  cfg.APIKey,
    })
    return eino.NewEinoProvider(chatModel, "openai", cfg.Model, 128000), nil
})

// 按名获取实例
p, err := registry.GetProvider("openai", &registry.ProviderConfig{
    Model:  "gpt-4o",
    APIKey: os.Getenv("OPENAI_API_KEY"),
})
```

### 消息转换管道

`TransformPipeline` 支持跨 Provider 消息格式适配：

```go
pipeline := transform.NewPipeline()
pipeline.Add(transform.NormalizeToolCallIDs)    // 清理/截断 ToolCall ID
pipeline.Add(transform.ImageDowngrade)          // 不支持视觉时降级图片为文本
pipeline.Add(transform.ThinkingBlockAdapter)    // OpenAI/Anthropic 思维块互转

msgs, err := pipeline.Execute(ctx, messages, "anthropic")
```

内置转换函数：`NormalizeToolCallIDs` / `ImageDowngrade` / `ThinkingBlockAdapter` / `ToolCallIDNormalizer` / `ImageFormatAdapter` / `SystemMessageAdapter`

### 认证抽象

`TokenSource` 统一认证，支持静态 API Key、OAuth2 刷新和回退：

```go
// 静态 API Key
ts := auth.NewStaticTokenSource("sk-xxx")

// OAuth2 自动刷新
ts := auth.NewOAuthTokenSource(oauthConfig, refreshToken)

// 主源失败时回退
ts := auth.NewFallbackTokenSource(primary, fallback)
```

## 接口替换

所有核心组件均通过 interface 定义，可自定义替换：

```go
// 自定义 ContextManager
type MyContextManager struct{}
func (m *MyContextManager) GetMessages(ctx context.Context) ([]ctxpkg.TurnItem, error) { ... }
func (m *MyContextManager) RecordItem(ctx context.Context, item ctxpkg.TurnItem) error { ... }
// ... 实现其他方法

agent, _ := loop.NewBuilder().
    WithProvider(provider).
    WithContextManager(&MyContextManager{}).
    WithToolRegistry(toolReg).
    Build()
```

## 项目结构

```
go-agent-core/
├── agent/                    # L1 Agent 引擎层
│   ├── event/                # 事件类型（17种）与状态机（6种状态）
│   ├── loop/                 # LoopAgent / Builder / Generator / HITL / Approval
│   ├── middleware/            # BeforeTurn/AfterTurn/BeforeCompact/AfterCompact 链
│   ├── orchestrate/          # 编排器（多 Agent 协调）
│   ├── session/              # Session 门面 + Builder（含 NewBuilderFromSettings）
│   ├── sharedctx/            # 跨组件共享上下文
│   └── subagent/             # 子 Agent 注册表
├── capability/               # L2 能力系统层
│   ├── registry/             # ToolRegistry + ParallelExecutor + DeferredLoader
│   ├── toolhook/             # HookPipeline（Before/After 双钩子 + ArgumentsPreparer）
│   ├── skill/                # SkillProvider（SKILL.md 加载 + gitignore 过滤）
│   ├── mcp/                  # MCPProvider（stdio/SSE/HTTP 三种传输）
│   ├── extension/            # ExtensionRunner（BeforeProviderRequest/AfterProviderResponse）
│   └── tools/                # 内置工具（read/write/edit/grep/glob/ls/execute/web_fetch/image_view）
├── memory/                   # L3 记忆与存储层
│   ├── context/              # ContextManager + HeuristicContextManager + Compactor
│   ├── compactor/            # Truncating/Summary/MicroCompactor + TokenEstimator
│   ├── session/              # SessionManager + JSONLSessionStore + JSONLSessionSink
│   └── log/                  # ExecLogger + JSONLExecLogger + LogExtractor + LogSelector
├── llm/                      # L4 LLM 协议层
│   ├── provider/             # ModelProvider 接口 + SwapableProvider + LazyProvider
│   ├── stream/               # StreamEvent 类型 + BoundedChannel
│   ├── transform/            # TransformPipeline + 6 种内置转换
│   ├── registry/             # ProviderRegistry 工厂注册表
│   ├── adapter/eino/         # Eino 适配器（OpenAI/Anthropic/Gemini/DeepSeek）
│   ├── auth/                 # TokenSource（Static/OAuth2/Fallback）+ CredentialStore
│   ├── catalog/              # 模型目录（catalog.json）
│   └── message/              # Message/Content/ToolCall/Usage 类型
├── production/               # 横切：生产化组件
│   ├── CircuitBreaker        # 三态熔断（Closed→Open→HalfOpen）
│   ├── LoopDetector          # 连续相同工具调用检测
│   ├── SecurityGuard         # 工具调用安全校验（白名单/黑名单）
│   ├── AuditLogger           # 审计日志（工具调用/审批/数据访问）
│   ├── IdempotencyKey        # 幂等检查
│   └── TelemetryCollector    # 指标 + 链路追踪 Span
├── verify/                   # 横切：校验框架
│   └── scanner               # AST 扫描（IFACE-001~003, SCAN-010~013）
├── config/                   # 配置加载与合并
├── prompt/                   # PromptBuilder + ToolRegistryReader
├── cmd/                      # L0 应用入口
├── examples/sdk/             # SDK 示例（basic_usage/custom_tool/streaming/full_agent）
└── e2e_testing/              # E2E 测试基础设施
```

## Commit 规范

```
<type>(<scope>): <description>
```

type: feat / fix / refactor / test / docs / chore
scope: llm / memory / capability / agent / production / verify / cmd

## License

[MIT](LICENSE)
