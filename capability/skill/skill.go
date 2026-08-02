// Package skill 定义 Skill 能力供给抽象。
//
// SkillProvider 加载 SKILL.md 文件并注入到 system prompt，
// 扩展 Agent 的指令能力。
package skill

import "context"

// SkillDefinition 描述一个 Skill。
type SkillDefinition struct {
	Name string
	Description string
	Content string // SKILL.md 内容
	Source string // 来源路径或 URL
}

// SkillProvider 是 Skill 能力供给接口。
type SkillProvider interface {
	// LoadSkills 从指定目录加载 Skill。
	LoadSkills(ctx context.Context, dirs []string) ([]SkillDefinition, error)
	// Rescan 重新扫描已加载的 Skill 目录。
	Rescan(ctx context.Context) ([]SkillDefinition, error)
	// Available 列出当前可用的 Skill。
	Available(ctx context.Context) ([]SkillDefinition, error)
}
