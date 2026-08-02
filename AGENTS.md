# AGENTS.md

> 跨工具开放标准（Codex / Cursor / Gemini CLI / Claude Code 等兼容）。
> 完整规范详见 [go-agent-core-v1-infra](../go-agent-core-v1-infra/) 仓库：
> - [CLAUDE.md](../go-agent-core-v1-infra/CLAUDE.md) — 完整 LLM 行为约束
> - [AGENTS.md](../go-agent-core-v1-infra/AGENTS.md) — 完整跨工具标准

## Setup

```bash
go mod download # 依赖
make build # 编译
make check # 提交前检查
make verify # 全量校验
```

Go 版本：1.26+。

## 核心原则

- **接口驱动**：所有核心能力通过 interface 定义，用户可替换任何组件
- **分层架构**：严格五层分离（L0-L4），每层只依赖下层，无循环依赖
- **日志永驻**：ExecLogger 永远写入 JSONL，不可关闭

详见：[CLAUDE.md](../go-agent-core-v1-infra/CLAUDE.md) 和 [ARCHITECTURE.md](../go-agent-core-v1-infra/docs/ARCHITECTURE.md)

## Code Style

- 依赖方向严格单向：L0 → L1 → L2 → L3 → L4
- `llm/` 零 Eino 具体类型依赖（IFACE-001/002/003）
- 错误返回值必须检查或 `_ =` 显式忽略（附注释）
- goroutine 生命周期须可控

详见：[ARCHITECTURE.md](../go-agent-core-v1-infra/docs/ARCHITECTURE.md) 和 [traceability_matrix.md](../go-agent-core-v1-infra/docs/traceability_matrix.md)

## Testing

```bash
make test # 单元测试（含 race）
make verify # 全量校验（check+scan+test-log+test-leak）
make scan # AST 扫描
```

详见：[testing_strategy.md](../go-agent-core-v1-infra/infra/test_verify/testing_strategy.md)

## PR

- Commit：`<type>(<scope>): <description>`
- scope：llm / memory / capability / agent / production / verify / cmd

## 项目结构

```
go-agent-core-v1/ # 本仓库 — 代码实现
├── cmd/ # L0 应用入口层
├── agent/ # L1 Agent 引擎层
├── capability/ # L2 能力系统层
├── memory/ # L3 记忆与存储层
├── llm/ # L4 LLM 协议层
├── production/ # 横切：生产化组件
├── verify/ # 横切：校验框架
├── CLAUDE.md
└── AGENTS.md

go-agent-core-v1-infra/ # 基础设施仓库 — 完整约束、规则、任务
├── docs/ # 设计文档、架构图、ADR
├── infra/ # 任务规划与追踪
├── e2e_testing/ # E2E 测试框架
├── reports/ # 审计与评估报告
├── CLAUDE.md # 完整 LLM 行为约束
└── AGENTS.md # 完整跨工具标准
```
