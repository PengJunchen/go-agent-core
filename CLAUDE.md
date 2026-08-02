# go-agent-core-v1

接口驱动、分层清晰、日志永驻的智能体核心框架。

> 完整行为约束、设计决策、验收规范等详见 [go-agent-core-v1-infra](../go-agent-core-v1-infra/) 仓库：
> - [CLAUDE.md](../go-agent-core-v1-infra/CLAUDE.md) — 完整 LLM 行为约束
> - [AGENTS.md](../go-agent-core-v1-infra/AGENTS.md) — 完整跨工具标准
> - [docs/](../go-agent-core-v1-infra/docs/) — 架构文档、ADR、里程碑、矩阵
> - [infra/](../go-agent-core-v1-infra/infra/) — 任务规划与追踪
> - [e2e_testing/](../go-agent-core-v1-infra/e2e_testing/) — E2E 测试框架

## 三原则

1. **接口驱动** — 所有核心能力通过 interface 定义，可替换
2. **分层架构** — L0→L1→L2→L3→L4 严格单向依赖，禁止反向
3. **日志永驻** — ExecLogger 不可关闭，用户通过 LogExtractor 选择性取走

## 依赖约束

- `llm/` 零 Eino 具体类型依赖（IFACE-001/002/003 AST 规则）
- `memory/` 可依赖 `llm/provider`
- `capability/` 可依赖 `memory/`
- `agent/` 可依赖 L2/L3/L4

→ 详见 [ARCHITECTURE.md](../go-agent-core-v1-infra/docs/ARCHITECTURE.md)

## 快速命令

```bash
make build # 编译
make test # 测试（含 race 检测）
make check # 提交前检查
make verify # 全量校验（check+scan+test-log+test-leak）
make scan # AST 扫描
```

## Commit 规范

```
<type>(<scope>): <desc>
```
type: feat/fix/refactor/test/docs/chore | scope: llm/memory/capability/agent/production/verify/cmd
