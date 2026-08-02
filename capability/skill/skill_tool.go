package skill

import (
	"context"
	"fmt"
	"os"

	"github.com/pengjunchen/go-agent-core/capability/registry"
)

// SkillTool 将一个 Skill 包装为 registry.ToolDefinition，实现渐进式披露
// （progressive disclosure）：
// - 注册阶段：仅暴露 Name 与 Description（节省 token）
// - 调用阶段：从磁盘按需加载完整 SKILL.md 内容并返回给 LLM
//
// 这样做可以避免在系统提示中一次性注入大量 Skill 指令文本，
// 仅当 LLM 显式调用对应工具时才加载完整指令。
type SkillTool struct {
	skillPath string // SKILL.md 文件路径
	name string
	description string
}

// NewSkillTool 根据技能名称、简短描述与 SKILL.md 路径创建 SkillTool。
func NewSkillTool(name, description, skillPath string) *SkillTool {
	return &SkillTool{
		name: name,
		description: description,
		skillPath: skillPath,
	}
}

// ToToolDefinition 将 SkillTool 转换为 registry.ToolDefinition。
//
// 返回的 ToolDefinition 仅在 Description 中携带简短描述；
// Handler 在被调用时才会读取完整 SKILL.md 内容，实现延迟加载。
func (s *SkillTool) ToToolDefinition() registry.ToolDefinition {
	return registry.ToolDefinition{
		Name: s.name,
		Description: s.description, // 仅简短描述，不包含完整内容
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type": "string",
					"description": "Input for the skill",
				},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (*registry.ToolResult, error) {
			// 延迟加载完整技能内容
			content, err := os.ReadFile(s.skillPath)
			if err != nil {
				return &registry.ToolResult{
					Content: fmt.Sprintf("Failed to load skill %s: %v", s.name, err),
					IsError: true,
				}, nil
			}

			// 返回完整技能内容，供 LLM 按照指令执行
			input, _ := args["input"].(string)
			result := fmt.Sprintf("Skill: %s\n\n%s\n\nUser input: %s", s.name, string(content), input)
			return &registry.ToolResult{
				Content: result,
			}, nil
		},
		ParallelSafe: true,
	}
}
