// Package prompt provides layered System Prompt construction for the agent.
//
// The builder assembles the system prompt in three layers:
// 1. Default coding instructions (base layer)
// 2. AGENTS.md content (project-specific instructions, if present)
// 3. Environment awareness (cwd, git branch, OS, time)
//
// This aligns with go-agent's TemplatedPromptLoader and trae-cli's
// InstructionBuilder patterns, adapted to go-agent-core's interface-first
// architecture.
package prompt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Builder constructs a layered System Prompt for the agent.
type Builder struct {
	// WorkDir is the project root directory for environment context.
	WorkDir string
	// DefaultInstruction is the base coding instruction. If empty, a default is used.
	DefaultInstruction string
	// AgentsMDPath overrides the default AGENTS.md search. If empty, searches WorkDir.
	AgentsMDPath string
	// ExtraContext is additional context appended at the end.
	ExtraContext string
	// toolRegistry provides tool PromptGuidelines for injection.
	toolRegistry ToolRegistryReader
}

// ToolGuideline holds a tool's name and its usage guidelines.
type ToolGuideline struct {
	Name string
	Guidelines string
}

// ToolRegistryReader is a minimal read-only interface for collecting tool
// guidelines. This avoids importing capability/registry (which would create
// a circular dependency). The registry package or callers provide an adapter.
type ToolRegistryReader interface {
	ListGuidelines() []ToolGuideline
}

// BuilderOption configures the Builder.
type BuilderOption func(*Builder)

// WithWorkDir sets the working directory for environment context.
func WithWorkDir(dir string) BuilderOption {
	return func(b *Builder) { b.WorkDir = dir }
}

// WithDefaultInstruction sets the base coding instruction.
func WithDefaultInstruction(instruction string) BuilderOption {
	return func(b *Builder) { b.DefaultInstruction = instruction }
}

// WithAgentsMDPath overrides the AGENTS.md file path.
func WithAgentsMDPath(path string) BuilderOption {
	return func(b *Builder) { b.AgentsMDPath = path }
}

// WithExtraContext adds extra context at the end of the prompt.
func WithExtraContext(ctx string) BuilderOption {
	return func(b *Builder) { b.ExtraContext = ctx }
}

// WithToolRegistry sets the tool registry reader for injecting tool
// PromptGuidelines into the system prompt.
func WithToolRegistry(reg ToolRegistryReader) BuilderOption {
	return func(b *Builder) { b.toolRegistry = reg }
}

// NewBuilder creates a new prompt Builder.
func NewBuilder(opts ...BuilderOption) *Builder {
	b := &Builder{
		WorkDir: "",
		DefaultInstruction: "",
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Build constructs the full system prompt.
func (b *Builder) Build() string {
	var sb strings.Builder

	// Layer 1: Default coding instructions.
	sb.WriteString(b.defaultInstructions())
	sb.WriteString("\n\n")

	// Layer 2: AGENTS.md content.
	if agentsContent := b.loadAgentsMD(); agentsContent != "" {
		sb.WriteString("<project_instructions>\n")
		sb.WriteString(agentsContent)
		sb.WriteString("\n</project_instructions>\n\n")
	}

	// Layer 3: Tool PromptGuidelines.
	if guidelines := b.toolGuidelines(); guidelines != "" {
		sb.WriteString(guidelines)
		sb.WriteString("\n\n")
	}

	// Layer 4: Environment awareness.
	sb.WriteString(b.environmentContext())

	// Extra context.
	if b.ExtraContext != "" {
		sb.WriteString("\n\n")
		sb.WriteString(b.ExtraContext)
	}

	return sb.String()
}

// toolGuidelines collects PromptGuidelines from the tool registry and formats
// them as a <tool_guidelines> section. Returns empty string if no registry
// is set or no guidelines exist.
func (b *Builder) toolGuidelines() string {
	if b.toolRegistry == nil {
		return ""
	}

	guidelines := b.toolRegistry.ListGuidelines()
	if len(guidelines) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<tool_guidelines>\n")
	for _, g := range guidelines {
		if g.Guidelines == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("- %s: %s\n", g.Name, g.Guidelines))
	}
	sb.WriteString("</tool_guidelines>")
	return sb.String()
}

// defaultInstructions returns the base coding instruction.
func (b *Builder) defaultInstructions() string {
	if b.DefaultInstruction != "" {
		return b.DefaultInstruction
	}
	return defaultCodingInstruction
}

// loadAgentsMD loads AGENTS.md from the work directory.
func (b *Builder) loadAgentsMD() string {
	path := b.AgentsMDPath
	if path == "" {
		if b.WorkDir == "" {
			return ""
		}
		path = filepath.Join(b.WorkDir, "AGENTS.md")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// environmentContext generates environment-aware context injection.
func (b *Builder) environmentContext() string {
	var sb strings.Builder
	sb.WriteString("<environment>\n")

	// Working directory.
	cwd := b.WorkDir
	if cwd == "" {
		if dir, err := os.Getwd(); err == nil {
			cwd = dir
		}
	}
	if cwd != "" {
		sb.WriteString(fmt.Sprintf("cwd: %s\n", cwd))
	}

	// Git branch.
	if branch := b.gitBranch(); branch != "" {
		sb.WriteString(fmt.Sprintf("git_branch: %s\n", branch))
	}

	// OS.
	sb.WriteString(fmt.Sprintf("os: %s/%s\n", runtime.GOOS, runtime.GOARCH))

	// Current time.
	sb.WriteString(fmt.Sprintf("time: %s\n", time.Now().Format("2006-01-02 15:04:05 MST")))

	sb.WriteString("</environment>")
	return sb.String()
}

// gitBranch returns the current git branch name, or empty if not in a git repo.
func (b *Builder) gitBranch() string {
	dir := b.WorkDir
	if dir == "" {
		dir = "."
	}

	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// defaultCodingInstruction is the base system prompt for the coding agent.
const defaultCodingInstruction = `You are an expert software engineer assistant with access to file system and shell tools.

You can use the following tools to help with coding tasks:
- read_file: Read the contents of a file with line numbers
- write_file: Write content to a file (creates parent directories)
- edit_file: Edit a file by replacing exact string matches
- execute: Run shell commands (working directory is locked to the project root)
- grep: Search for regex patterns across files
- glob: Find files matching glob patterns

When working on code:
1. Always read a file before editing it to understand the context.
2. Use edit_file for precise modifications, not write_file for full rewrites.
3. When execute returns a non-zero exit code, read the stderr output and self-correct.
4. Use grep to find relevant code locations before making changes.
5. Prefer targeted edits over broad rewrites.

Be concise and direct. Explain what you're doing, then do it.`