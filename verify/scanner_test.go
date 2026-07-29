// Package verify 的 Scanner 测试。
package verify

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanner_IFACE001_EinoInIface 验证 IFACE-001 规则：接口层禁止 import adapter/eino。
func TestScanner_IFACE001_EinoInIface(t *testing.T) {
	dir := t.TempDir()
	// 模拟 llm/provider 接口层文件违规 import eino
	providerDir := filepath.Join(dir, "llm", "provider")
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	code := `package provider

import "github.com/cloudwego/eino/schema"

type LLMProvider interface {
	Generate() schema.Message
}
`
	if err := os.WriteFile(filepath.Join(providerDir, "provider.go"), []byte(code), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scanner := NewScanner()
	violations, err := scanner.ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(violations) == 0 {
		t.Error("expected IFACE-001/002 violation for eino import in iface layer")
	}
}

// TestScanner_IFACE003_AdapterInAgent 验证 IFACE-003 规则：agent 层禁止 import adapter。
func TestScanner_IFACE003_AdapterInAgent(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	code := `package loop

import "github.com/pengjunchen/go-agent-core/llm/adapter/eino"

type LoopAgent struct{}
`
	if err := os.WriteFile(filepath.Join(agentDir, "loop.go"), []byte(code), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scanner := NewScanner()
	violations, err := scanner.ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(violations) == 0 {
		t.Error("expected IFACE-003 violation for adapter import in agent layer")
	}
}

// TestScanner_SCAN010_HardcodedProvider 验证 SCAN-010 规则：硬编码 provider 名。
func TestScanner_SCAN010_HardcodedProvider(t *testing.T) {
	dir := t.TempDir()
	code := `package main

func getProvider(name string) string {
	switch name {
	case "openai":
		return "gpt-4o"
	}
	return ""
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scanner := NewScanner()
	violations, err := scanner.ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	found := false
	for _, v := range violations {
		if v.RuleID == "SCAN-010" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SCAN-010 violation for hardcoded provider name")
	}
}

// TestScanner_CleanProject 验证无违规项目通过扫描。
func TestScanner_CleanProject(t *testing.T) {
	dir := t.TempDir()
	code := `package provider

import "context"

type ModelProvider interface {
	Generate(ctx context.Context) error
}
`
	if err := os.WriteFile(filepath.Join(dir, "provider.go"), []byte(code), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	scanner := NewScanner()
	violations, err := scanner.ScanDir(dir)
	if err != nil {
		t.Fatalf("ScanDir: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("expected 0 violations, got %d: %v", len(violations), violations)
	}
}
