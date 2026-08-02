package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// ST-001: SkillTool 仅暴露 name 与 description，不包含完整内容。
func TestSkillTool_ExposesOnlyNameAndDescription(t *testing.T) {
	fullContent := "# My Skill\n\nThis is a very long skill body that should NOT appear in the tool description."
	path := writeTempSkillFile(t, fullContent)
	defer func() { _ = os.Remove(path) }()

	st := NewSkillTool("my-skill", "Short description for the skill", path)
	def := st.ToToolDefinition()

	if def.Name != "my-skill" {
		t.Errorf("Name = %q, want %q", def.Name, "my-skill")
	}
	if def.Description != "Short description for the skill" {
		t.Errorf("Description = %q, want short description", def.Description)
	}

	// 关键断言：完整内容不应出现在注册阶段的 Description 中
	if strings.Contains(def.Description, "very long skill body") {
		t.Errorf("Description should NOT contain full skill content, got %q", def.Description)
	}
}

// ST-002: Handler 被调用时从磁盘加载完整 SKILL.md 内容。
func TestSkillTool_HandlerLoadsFullContent(t *testing.T) {
	fullContent := "# Code Review Skill\n\nDetailed instructions for reviewing code."
	path := writeTempSkillFile(t, fullContent)
	defer func() { _ = os.Remove(path) }()

	st := NewSkillTool("code-review", "Run code review", path)
	def := st.ToToolDefinition()

	if def.Handler == nil {
		t.Fatal("Handler should not be nil")
	}

	result, err := def.Handler(context.Background(), map[string]any{"input": "review this PR"})
	if err != nil {
		t.Fatalf("Handler returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("Handler returned error result: %s", result.Content)
	}

	if !strings.Contains(result.Content, "Code Review Skill") {
		t.Errorf("result should contain full skill content, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "Detailed instructions for reviewing code") {
		t.Errorf("result should contain full body, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "review this PR") {
		t.Errorf("result should echo user input, got %q", result.Content)
	}
}

// ST-003: Handler 在文件缺失时优雅降级，返回 IsError=true 而非 Go error。
func TestSkillTool_HandlerMissingFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.md")

	st := NewSkillTool("missing-skill", "A missing skill", missingPath)
	def := st.ToToolDefinition()

	result, err := def.Handler(context.Background(), map[string]any{"input": "anything"})
	if err != nil {
		t.Fatalf("Handler should not return Go error on missing file, got: %v", err)
	}
	if !result.IsError {
		t.Error("IsError should be true when skill file is missing")
	}
	if !strings.Contains(result.Content, "Failed to load skill") {
		t.Errorf("Content should mention load failure, got %q", result.Content)
	}
	if !strings.Contains(result.Content, "missing-skill") {
		t.Errorf("Content should mention skill name, got %q", result.Content)
	}
}

// ST-004: SkillTool 可以注册到 ToolRegistry 中并可查询。
func TestSkillTool_RegisterInToolRegistry(t *testing.T) {
	fullContent := "# Deploy Skill\n\nSteps to deploy."
	path := writeTempSkillFile(t, fullContent)
	defer func() { _ = os.Remove(path) }()

	st := NewSkillTool("deploy", "Deploy the application", path)
	def := st.ToToolDefinition()

	reg := registry.NewDefaultToolRegistry()
	if err := reg.RegisterTool(context.Background(), def); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	got, err := reg.GetTool(context.Background(), "deploy")
	if err != nil {
		t.Fatalf("GetTool: %v", err)
	}
	if got.Name != "deploy" {
		t.Errorf("GetTool Name = %q, want %q", got.Name, "deploy")
	}
	if got.Description != "Deploy the application" {
		t.Errorf("GetTool Description = %q, want short description", got.Description)
	}
	if got.Handler == nil {
		t.Error("registered Handler should not be nil")
	}

	// 通过 registry 调用 handler，验证延迟加载链路完整
	result, err := got.Handler(context.Background(), map[string]any{"input": "prod"})
	if err != nil {
		t.Fatalf("Handler via registry error: %v", err)
	}
	if !strings.Contains(result.Content, "Deploy Skill") {
		t.Errorf("Handler via registry should load full content, got %q", result.Content)
	}
}

// writeTempSkillFile 将内容写入临时文件并返回路径。
func writeTempSkillFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp skill file: %v", err)
	}
	return path
}
