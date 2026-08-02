package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Compile-time check that FileSkillProvider implements SkillProvider.
var _ SkillProvider = (*FileSkillProvider)(nil)

// FileSkillProvider 通过扫描文件系统目录中的 SKILL.md 文件来加载 Skill。
type FileSkillProvider struct {
	mu sync.RWMutex
	skills map[string]SkillDefinition // key: skill name
	dirs []string // 已加载的目录列表，供 Rescan 使用
}

// NewFileSkillProvider 创建一个空的 FileSkillProvider。
func NewFileSkillProvider() *FileSkillProvider {
	return &FileSkillProvider{
		skills: make(map[string]SkillDefinition),
	}
}

// LoadSkills 扫描指定目录列表中的 SKILL.md 文件并加载为 SkillDefinition。
// 扫描结果会累积到内部存储中。
func (p *FileSkillProvider) LoadSkills(ctx context.Context, dirs []string) ([]SkillDefinition, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 保存目录供 Rescan 使用（去重追加）
	existingDirs := make(map[string]bool, len(p.dirs))
	for _, d := range p.dirs {
		existingDirs[d] = true
	}
	for _, d := range dirs {
		if !existingDirs[d] {
			p.dirs = append(p.dirs, d)
		}
	}

	var allSkills []SkillDefinition
	for _, dir := range dirs {
		skills, err := p.scanDir(dir)
		if err != nil {
			return nil, err
		}
		for _, s := range skills {
			p.skills[s.Name] = s
			allSkills = append(allSkills, s)
		}
	}

	return allSkills, nil
}

// Rescan 重新扫描之前加载过的所有目录。
func (p *FileSkillProvider) Rescan(ctx context.Context) ([]SkillDefinition, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.dirs) == 0 {
		return nil, errors.New("no directories loaded; call LoadSkills first")
	}

	// 清空已有 skills，重新扫描
	p.skills = make(map[string]SkillDefinition)

	var allSkills []SkillDefinition
	for _, dir := range p.dirs {
		skills, err := p.scanDir(dir)
		if err != nil {
			return nil, err
		}
		for _, s := range skills {
			p.skills[s.Name] = s
			allSkills = append(allSkills, s)
		}
	}

	return allSkills, nil
}

// Available 返回当前已加载的所有 Skill，按名称排序。
func (p *FileSkillProvider) Available(_ context.Context) ([]SkillDefinition, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]SkillDefinition, 0, len(p.skills))
	for _, s := range p.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// scanDir 扫描单个目录下的 SKILL.md 文件。
// 调用方须持有写锁。
//
// 支持 .gitignore 解析：扫描时读取目录下的 .gitignore 文件，
// 跳过匹配模式的文件/目录。同时始终跳过 node_modules/、dist/、.git/、vendor/。
// 符号链接会被跳过以避免循环和重复（AC-4）。
func (p *FileSkillProvider) scanDir(dir string) ([]SkillDefinition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// 解析当前目录下的 .gitignore（如果存在）。
	var ignorePatterns []string
	gitignorePath := filepath.Join(dir, ".gitignore")
	if patterns, err := parseGitignore(gitignorePath); err == nil {
		ignorePatterns = patterns
	}

	var skills []SkillDefinition
	for _, entry := range entries {
		name := entry.Name()

		// 跳过 .gitignore 文件本身。
		if name == ".gitignore" {
			continue
		}

		// 跳过符号链接（AC-4: symlink deduplication）。
		if isSymlink(dir, entry) {
			continue
		}

		// 跳过默认忽略的目录。
		if entry.IsDir() && isDefaultIgnored(name) {
			continue
		}

		// 跳过 .gitignore 匹配的条目。
		if matchGitignorePattern(name, ignorePatterns) {
			continue
		}

		if entry.IsDir() {
			// 递归进入子目录查找 SKILL.md
			sub := filepath.Join(dir, name)
			subSkills, err := p.scanDir(sub)
			if err != nil {
				return nil, err
			}
			skills = append(skills, subSkills...)
			continue
		}
		if name == "SKILL.md" {
			path := filepath.Join(dir, name)
			skill, err := p.parseSkillFile(path)
			if err != nil {
				return nil, err
			}
			skills = append(skills, skill)
		}
	}

	return skills, nil
}

// parseSkillFile 解析单个 SKILL.md 文件。
// 支持可选的 YAML frontmatter（由 --- 分隔）。
// 若无 frontmatter，则使用文件名作为 name，第一行作为 description。
func (p *FileSkillProvider) parseSkillFile(path string) (SkillDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillDefinition{}, err
	}

	content := string(data)
	name := ""
	description := ""
	body := content

	// 尝试解析 YAML frontmatter
	if parsed, ok := extractFrontmatter(content); ok {
		name = parsed.name
		description = parsed.description
		body = parsed.body
	} else {
		// 无 frontmatter：使用文件所在目录名作为 name
		dirName := filepath.Base(filepath.Dir(path))
		name = dirName
		// 使用第一行非空内容作为 description
		lines := strings.Split(strings.TrimSpace(content), "\n")
		if len(lines) > 0 {
			description = strings.TrimSpace(lines[0])
		}
		body = content
	}

	return SkillDefinition{
		Name: name,
		Description: description,
		Content: body,
		Source: path,
	}, nil
}

// frontmatterResult 保存从 YAML frontmatter 解析出的字段。
type frontmatterResult struct {
	name string
	description string
	body string
}

// extractFrontmatter 尝试从内容中提取 YAML frontmatter。
// frontmatter 格式：
//
//	---
//	name: skill-name
//	description: When to use this skill
//	---
//	# Skill content here...
//
// 返回 (result, true) 表示成功提取，(nil值, false) 表示无 frontmatter。
func extractFrontmatter(content string) (frontmatterResult, bool) {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "---") {
		return frontmatterResult{}, false
	}

	// 找到第一个 --- 后面的换行
	afterFirst := content[3:]
	if len(afterFirst) > 0 && afterFirst[0] != '\n' && afterFirst[0] != '\r' {
		return frontmatterResult{}, false
	}
	afterFirst = strings.TrimPrefix(afterFirst, "\r\n")
	afterFirst = strings.TrimPrefix(afterFirst, "\n")

	// 找到闭合的 ---
	closeIdx := strings.Index(afterFirst, "\n---")
	if closeIdx < 0 {
		// 尝试 --- 在文件末尾（无后续 body）
		if strings.HasSuffix(strings.TrimSpace(afterFirst), "---") {
			trimmed := strings.TrimSpace(afterFirst)
			fmContent := strings.TrimSuffix(trimmed, "---")
			name, desc := parseSimpleYAML(fmContent)
			if name == "" && desc == "" {
				return frontmatterResult{}, false
			}
			return frontmatterResult{
				name: name,
				description: desc,
				body: "",
			}, true
		}
		return frontmatterResult{}, false
	}

	fmContent := afterFirst[:closeIdx]
	body := afterFirst[closeIdx+4:] // 跳过 \n---

	// 确保 body 的 --- 后面有换行或结束
	if len(body) > 0 && body[0] == '\r' {
		body = body[1:]
	}
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}

	name, desc := parseSimpleYAML(fmContent)
	if name == "" && desc == "" {
		return frontmatterResult{}, false
	}

	return frontmatterResult{
		name: name,
		description: desc,
		body: body,
	}, true
}

// parseSimpleYAML 手动解析简单的 YAML 键值对（仅支持 name 和 description）。
// 不依赖外部 YAML 库。
func parseSimpleYAML(content string) (name, description string) {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		// 去除引号
		value = strings.Trim(value, `"'`)

		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

