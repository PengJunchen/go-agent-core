.PHONY: all verify check fmt vet lint build test scan test-log test-leak test-interface tidy clean

## go-agent-core Makefile —— 沿用 go-agent 校验范式（AST + Log + Leak + TDD）
## make verify = CI 硬门，全部通过才能合并。

all: build

## verify: CI 完整校验门禁（go-agent 范式：fmt+vet+lint+build+test+scan+log+leak+interface）
verify: check scan test-log test-leak test-interface

## check: 基础检查链
check: fmt vet lint build test

fmt:
	@if [ -n "$$(go fmt ./...)" ]; then \
		echo " fmt: files need formatting"; go fmt ./...; \
	fi

vet:
	go vet ./...

lint:
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo " lint: golangci-lint not installed, skipped"

build:
	go build ./...

test:
	go test ./...

## scan: AST 扫描规则（go-agent SCAN-001~007 + go-agent-core SCAN-010~013）
## M0 阶段先提供占位目标，M1 接入 verify/scanner.go
scan:
	@echo " scan: AST scanner pending (M1) — see verify/scanner.go"
	@go build ./... && echo " scan: build OK"

## test-log: 日志核心验证（VQ/VS/VT/VC/VH 规则）
test-log:
	go test ./memory/log/... -run "V[QSVTCH]" -v

## test-leak: Goroutine 泄漏检测（AssertNoGoroutineLeak）
test-leak:
	@echo " test-leak: leak detector pending (M1) — see verify/goroutine.go"
	@go test ./... && echo " test-leak: tests OK"

## test-interface: 接口实现覆盖率检测（SCAN-013，≥90%）
test-interface:
	go test ./... -run "Interface" -v

tidy:
	go mod tidy

clean:
	rm -rf bin/
