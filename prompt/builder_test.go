package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PB-001: Builder produces non-empty prompt with default instructions.
func TestBuilder_DefaultInstructions(t *testing.T) {
	b := NewBuilder()
	prompt := b.Build()

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "software engineer") {
		t.Error("expected default coding instruction in prompt")
	}
}

// PB-002: Builder includes custom default instruction.
func TestBuilder_CustomInstruction(t *testing.T) {
	b := NewBuilder(WithDefaultInstruction("You are a code reviewer."))
	prompt := b.Build()

	if !strings.Contains(prompt, "code reviewer") {
		t.Error("expected custom instruction in prompt")
	}
}

// PB-003: Builder loads AGENTS.md from work directory.
func TestBuilder_LoadsAgentsMD(t *testing.T) {
	dir := t.TempDir()
	agentsContent := "# Project Rules\n\nAlways use tabs for indentation."
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(agentsContent), 0o644); err != nil {
		t.Fatal(err)
	}

	b := NewBuilder(WithWorkDir(dir))
	prompt := b.Build()

	if !strings.Contains(prompt, "Project Rules") {
		t.Error("expected AGENTS.md content in prompt")
	}
	if !strings.Contains(prompt, "<project_instructions>") {
		t.Error("expected project_instructions XML tag")
	}
}

// PB-004: Builder works without AGENTS.md.
func TestBuilder_NoAgentsMD(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(WithWorkDir(dir))
	prompt := b.Build()

	if strings.Contains(prompt, "<project_instructions>") {
		t.Error("should not contain project_instructions when no AGENTS.md")
	}
}

// PB-005: Builder injects environment information.
func TestBuilder_EnvironmentContext(t *testing.T) {
	dir := t.TempDir()
	b := NewBuilder(WithWorkDir(dir))
	prompt := b.Build()

	if !strings.Contains(prompt, "<environment>") {
		t.Error("expected environment XML tag")
	}
	if !strings.Contains(prompt, "cwd:") {
		t.Error("expected cwd in environment")
	}
	if !strings.Contains(prompt, "os:") {
		t.Error("expected os in environment")
	}
	if !strings.Contains(prompt, "time:") {
		t.Error("expected time in environment")
	}
}

// PB-006: Builder includes git branch when in a git repo.
func TestBuilder_GitBranch(t *testing.T) {
	// This test runs in the actual project directory which is a git repo.
	cwd, _ := os.Getwd()
	// Walk up to find the project root (has .git).
	dir := cwd
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	b := NewBuilder(WithWorkDir(dir))
	branch := b.gitBranch()
	// In CI or local, the branch may vary, but should be non-empty in a git repo.
	if branch == "" {
		// Skip if not in a git repo.
		t.Skip("not in a git repo, skipping git branch test")
	}

	prompt := b.Build()
	if !strings.Contains(prompt, "git_branch:") {
		t.Error("expected git_branch in environment context")
	}
}

// PB-007: Builder appends extra context.
func TestBuilder_ExtraContext(t *testing.T) {
	b := NewBuilder(WithExtraContext("Additional context: use Go 1.26."))
	prompt := b.Build()

	if !strings.Contains(prompt, "Additional context: use Go 1.26.") {
		t.Error("expected extra context in prompt")
	}
}

// PB-008: Builder with custom AGENTS.md path.
func TestBuilder_CustomAgentsMDPath(t *testing.T) {
	dir := t.TempDir()
	customPath := filepath.Join(dir, "custom-instructions.md")
	content := "# Custom Rules\nFollow these rules."
	if err := os.WriteFile(customPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	b := NewBuilder(WithAgentsMDPath(customPath))
	prompt := b.Build()

	if !strings.Contains(prompt, "Custom Rules") {
		t.Error("expected custom AGENTS.md content")
	}
}

// PB-009: Builder produces layered structure (default + AGENTS.md + env).
func TestBuilder_LayeredStructure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Project\nRules here."), 0o644); err != nil {
		t.Fatal(err)
	}

	b := NewBuilder(WithWorkDir(dir))
	prompt := b.Build()

	// Verify all three layers are present.
	defaultIdx := strings.Index(prompt, "software engineer")
	projectIdx := strings.Index(prompt, "<project_instructions>")
	envIdx := strings.Index(prompt, "<environment>")

	if defaultIdx < 0 {
		t.Fatal("default instruction layer not found")
	}
	if projectIdx < 0 {
		t.Fatal("project instructions layer not found")
	}
	if envIdx < 0 {
		t.Fatal("environment layer not found")
	}

	// Verify order: default < project < environment.
	if !(defaultIdx < projectIdx && projectIdx < envIdx) {
		t.Error("layers should be in order: default, project, environment")
	}
}

// PB-010: Builder handles empty work directory gracefully.
func TestBuilder_EmptyWorkDir(t *testing.T) {
	b := NewBuilder() // No work dir
	prompt := b.Build()

	if prompt == "" {
		t.Fatal("expected non-empty prompt even without work dir")
	}
}

// ---------------------------------------------------------------------------
// AC-1: Tool PromptGuidelines are injected into System Prompt
// ---------------------------------------------------------------------------

// mockToolRegistry implements ToolRegistryReader for testing.
type mockToolRegistry struct {
	guidelines []ToolGuideline
}

func (m *mockToolRegistry) ListGuidelines() []ToolGuideline {
	return m.guidelines
}

// AC-1: Tool PromptGuidelines are injected into System Prompt.
func TestBuilder_ToolPromptGuidelines(t *testing.T) {
	reg := &mockToolRegistry{
		guidelines: []ToolGuideline{
			{Name: "read_file", Guidelines: "Always read a file before editing it. Use line numbers for reference."},
			{Name: "execute", Guidelines: "Prefer targeted commands over broad ones. Check exit codes."},
			{Name: "edit_file", Guidelines: ""}, // empty guideline should be skipped
		},
	}

	b := NewBuilder(WithToolRegistry(reg))
	prompt := b.Build()

	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !strings.Contains(prompt, "Always read a file before editing it") {
		t.Error("expected read_file PromptGuidelines in prompt")
	}
	if !strings.Contains(prompt, "Prefer targeted commands over broad ones") {
		t.Error("expected execute PromptGuidelines in prompt")
	}
	if !strings.Contains(prompt, "<tool_guidelines>") {
		t.Error("expected <tool_guidelines> XML tag")
	}
}

// Builder without ToolRegistry still works.
func TestBuilder_NoToolRegistry(t *testing.T) {
	b := NewBuilder()
	prompt := b.Build()

	if strings.Contains(prompt, "<tool_guidelines>") {
		t.Error("should not contain tool_guidelines when no registry")
	}
}

// ToolGuidelines appear after default instructions but before environment.
func TestBuilder_ToolGuidelinesLayerOrder(t *testing.T) {
	reg := &mockToolRegistry{
		guidelines: []ToolGuideline{
			{Name: "read_file", Guidelines: "Read before edit."},
		},
	}

	b := NewBuilder(WithToolRegistry(reg))
	prompt := b.Build()

	defaultIdx := strings.Index(prompt, "software engineer")
	guidelinesIdx := strings.Index(prompt, "<tool_guidelines>")
	envIdx := strings.Index(prompt, "<environment>")

	if defaultIdx < 0 {
		t.Fatal("default instruction layer not found")
	}
	if guidelinesIdx < 0 {
		t.Fatal("tool_guidelines layer not found")
	}
	if envIdx < 0 {
		t.Fatal("environment layer not found")
	}

	// Verify order: default < guidelines < environment.
	if !(defaultIdx < guidelinesIdx && guidelinesIdx < envIdx) {
		t.Error("layers should be in order: default, tool_guidelines, environment")
	}
}
