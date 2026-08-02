# go-agent-core

> 接口驱动、分层清晰、日志永驻的智能体核心框架

go-agent-core 从 所有核心能力（LLM / 记忆 / 工具 / 日志）均通过 interface 定义，用户可替换任何组件。

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

依赖方向严格单向：L0 → L1 → L2 → L3 → L4，禁止反向依赖。

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
 Model: "gpt-4o",
 BaseURL: "https://api.openai.com/v1",
 APIKey: os.Getenv("OPENAI_API_KEY"),
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

## 核心概念

### LoopAgent

LoopAgent 是核心调度接口，维护串行 Turn 循环：query → LLM 推理 → 工具调用 → 下一轮，直到模型不再调用工具或达到 MaxTurns。

```go
type LoopAgent interface {
 Query(ctx context.Context, input AgentInput) (<-chan event.AgentEvent, error)
 Interrupt(ctx context.Context) error // 中断当前 Turn
 Steer(ctx context.Context, msg string) error // 中途调整方向
 FollowUp(ctx context.Context, content string) error // 完成后追加
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
 WithProvider(provider). // 必填：LLM Provider
 WithContextManager(ctxMgr). // 必填：上下文管理器
 WithToolRegistry(toolReg). // 必填：工具注册表
 WithHookPipeline(hookPipeline). // 可选：ToolHook 管道
 WithMiddlewareChain(mwChain). // 可选：中间件链
 WithLogger(execLogger). // 可选：执行日志
 WithMaxTurns(30). // 可选：默认 20
 WithRetryConfig(retryCfg). // 可选：重试配置
 WithCompactThreshold(80000). // 可选：自动压缩阈值
 WithPrepareNextTurn(swapFn). // 可选：运行时 Provider 切换
 WithProduction(prodBundle). // 可选：生产化组件
 Build()
```

## 自定义工具

```go
toolReg := registry.NewDefaultToolRegistry()

err = toolReg.RegisterTool(context.Background(), registry.ToolDefinition{
 Name: "search",
 Description: "搜索知识库",
 Parameters: map[string]any{
 "type": "object",
 "properties": map[string]any{
 "query": map[string]any{
 "type": "string",
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

ToolHook 提供 Before/After 双钩子，可拦截、修改或阻止工具调用：

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

ApprovalHook 实现人机协同审批，高风险工具调用前等待人工决策：

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

审批等待期间，Agent 会通过 OnSuspend/OnResume 回调转换到 `WaitingApproval` 状态，提升可观测性。

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

ProductionBundle 将六大生产化能力聚合为单一注入点，所有字段可选（nil = 禁用）：

```go
bundle := production.NewProductionBundle(
 production.WithLoopDetector(production.NewDefaultLoopDetector(production.LoopDetectorConfig{
 ConsecutiveThreshold: 3, // 连续相同调用 >=3 次判定为循环
 WindowSize: 10, // 滑动窗口大小
 ArgumentComparison: true, // 比较参数
 })),
 production.WithCircuitBreaker(production.NewDefaultCircuitBreaker(production.CircuitBreakerConfig{
 FailureThreshold: 5, // 5次失败后熔断
 SuccessThreshold: 3, // 3次成功后恢复
 Timeout: 30 * time.Second, // 熔断等待时间
 HalfOpenMaxReqs: 1, // 半开状态最大请求数
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

ExecLogger 永远写入 JSONL 文件，不可关闭（P3 原则）。通过 LogExtractor 选择性提取：

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
 SessionID: "session-xxx",
 Limit: 100,
})
```

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
```

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
├── agent/ # L1 Agent 引擎层
│ ├── event/ # 事件类型与状态机
│ ├── loop/ # LoopAgent/Builder/Generator/HITL
│ └── middleware/ # 中间件链
├── capability/ # L2 能力系统层
│ ├── registry/ # ToolRegistry + ParallelExecutor
│ ├── toolhook/ # HookPipeline (Before/After)
│ ├── skill/ # SkillProvider
│ └── mcp/ # MCPProvider
├── memory/ # L3 记忆与存储层
│ ├── context/ # ContextManager + Compactor
│ ├── session/ # SessionManager
│ └── log/ # ExecLogger + LogExtractor
├── llm/ # L4 LLM 协议层
│ ├── provider/ # ModelProvider 接口
│ ├── stream/ # 事件流
│ ├── transform/ # 消息转换
│ ├── registry/ # ProviderRegistry
│ └── adapter/eino # Eino 适配器
├── production/ # 横切：生产化组件
├── verify/ # 横切：校验框架
├── e2e_testing/ # E2E 测试
└── docs/ # 架构文档
```

## License

MIT
