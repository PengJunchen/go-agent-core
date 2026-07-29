# AGENTS.md

> Cross-tool open standard (Codex / Cursor / Gemini CLI / Claude Code compatible).
> Full specs: [go-agent-core-v1-infra](../go-agent-core-v1-infra/) repo.

## Setup

```bash
go mod download # deps
make build # compile
make check # pre-commit check
make verify # full verification
```

Go version: 1.26+.

## Core Principles

- Interface-driven: all core capabilities defined via interface
- Layered architecture: strict L0-L4 separation
- Persistent logging: ExecLogger always writes JSONL

## Code Style

- Dependency direction: L0 -> L1 -> L2 -> L3 -> L4
- No Eino concrete types in llm/ (IFACE-001/002/003)
- Error returns must be checked or explicitly ignored with comment
- Goroutine lifecycle must be controllable

## Testing

```bash
make test # unit tests (with race)
make verify # full check (check+scan+test-log+test-leak)
make scan # AST scan
```

## PR

- Commit: `<type>(<scope>): <description>`
- scope: llm / memory / capability / agent / production / verify / cmd

## Project Structure

```
go-agent-core-v1/ # This repo - code implementation
+-- cmd/ # L0 Application entry
+-- agent/ # L1 Agent engine
+-- capability/ # L2 Capability system
+-- memory/ # L3 Memory & storage
+-- llm/ # L4 LLM protocol
+-- production/ # Cross-cutting: production
+-- verify/ # Cross-cutting: verification
+-- CLAUDE.md
+-- AGENTS.md

go-agent-core-v1-infra/ # Infra repo - full constraints, rules, tasks
+-- docs/ # Design docs, architecture, ADR
+-- infra/ # Task planning & tracking
+-- e2e_testing/ # E2E test framework
+-- reports/ # Audit & evaluation reports
+-- CLAUDE.md # Full LLM behavior constraints
+-- AGENTS.md # Full cross-tool standard
```
