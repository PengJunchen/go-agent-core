package skill

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// FS-001: 编译时接口断言。
func TestFileSkillProvider_InterfaceCheck(t *testing.T) {
	var _ SkillProvider = NewFileSkillProvider()
}

// FS-002: LoadSkills 从包含 SKILL.md 的目录加载 Skill。
func TestFileSkillProvider_LoadSkills_WithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "---\nname: my-skill\ndescription: A test skill\n---\n# My Skill Content\nSome body text."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	s := skills[0]
	if s.Name != "my-skill" {
		t.Errorf("Name = %q, want %q", s.Name, "my-skill")
	}
	if s.Description != "A test skill" {
		t.Errorf("Description = %q, want %q", s.Description, "A test skill")
	}
	if !strings.Contains(s.Content, "# My Skill Content") {
		t.Errorf("Content should contain body, got: %q", s.Content)
	}
	if !strings.Contains(s.Source, "SKILL.md") {
		t.Errorf("Source should contain SKILL.md, got: %q", s.Source)
	}
}

// FS-003: 无 frontmatter 时使用目录名和首行内容。
func TestFileSkillProvider_LoadSkills_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "fallback-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := "# This is the first line\nMore content here."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	s := skills[0]
	if s.Name != "fallback-skill" {
		t.Errorf("Name = %q, want %q", s.Name, "fallback-skill")
	}
	if !strings.Contains(s.Description, "first line") {
		t.Errorf("Description should contain first line, got: %q", s.Description)
	}
}

// FS-004: 多个目录分别包含 SKILL.md。
func TestFileSkillProvider_LoadSkills_MultipleDirs(t *testing.T) {
	dir1 := t.TempDir()
	skillDir1 := filepath.Join(dir1, "skill-a")
	if err := os.MkdirAll(skillDir1, 0o755); err != nil {
		t.Fatal(err)
	}
	content1 := "---\nname: skill-a\ndescription: Skill A\n---\n# A"
	if err := os.WriteFile(filepath.Join(skillDir1, "SKILL.md"), []byte(content1), 0o644); err != nil {
		t.Fatal(err)
	}

	dir2 := t.TempDir()
	skillDir2 := filepath.Join(dir2, "skill-b")
	if err := os.MkdirAll(skillDir2, 0o755); err != nil {
		t.Fatal(err)
	}
	content2 := "---\nname: skill-b\ndescription: Skill B\n---\n# B"
	if err := os.WriteFile(filepath.Join(skillDir2, "SKILL.md"), []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir1, dir2})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills count = %d, want 2", len(skills))
	}

	// 验证两个 skill 都存在
	names := make(map[string]bool)
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["skill-a"] || !names["skill-b"] {
		t.Errorf("expected both skill-a and skill-b, got: %v", names)
	}
}

// FS-005: 空目录不产生 Skill。
func TestFileSkillProvider_LoadSkills_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("skills count = %d, want 0", len(skills))
	}
}

// FS-006: 不存在的目录返回错误。
func TestFileSkillProvider_LoadSkills_NonexistentDir(t *testing.T) {
	p := NewFileSkillProvider()
	_, err := p.LoadSkills(context.Background(), []string{"/nonexistent/path/xyz"})
	if err == nil {
		t.Error("LoadSkills with nonexistent dir should fail")
	}
}

// FS-007: Available 返回按名称排序的 Skill 列表。
func TestFileSkillProvider_Available_Sorted(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + " skill\n---\n# " + name
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := NewFileSkillProvider()
	_, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	skills, err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if len(skills) != 3 {
		t.Fatalf("Available count = %d, want 3", len(skills))
	}
	if skills[0].Name != "alpha" || skills[1].Name != "bravo" || skills[2].Name != "charlie" {
		t.Errorf("Available order = %v, want [alpha, bravo, charlie]",
			[]string{skills[0].Name, skills[1].Name, skills[2].Name})
	}
}

// FS-008: Available 在无已加载 Skill 时返回空列表。
func TestFileSkillProvider_Available_Empty(t *testing.T) {
	p := NewFileSkillProvider()
	skills, err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("Available count = %d, want 0", len(skills))
	}
}

// FS-009: Rescan 重新扫描已加载的目录。
func TestFileSkillProvider_Rescan(t *testing.T) {
	dir := t.TempDir()

	// 初始加载：一个 skill
	skillDir := filepath.Join(dir, "initial-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: initial-skill\ndescription: Initial\n---\n# Initial"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	_, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// 添加新 skill
	newSkillDir := filepath.Join(dir, "new-skill")
	if err := os.MkdirAll(newSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	newContent := "---\nname: new-skill\ndescription: New\n---\n# New"
	if err := os.WriteFile(filepath.Join(newSkillDir, "SKILL.md"), []byte(newContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rescan 应该发现新添加的 skill
	skills, err := p.Rescan(context.Background())
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("Rescan count = %d, want 2", len(skills))
	}

	// 验证 Available 也更新了
	available, _ := p.Available(context.Background())
	if len(available) != 2 {
		t.Errorf("Available after Rescan count = %d, want 2", len(available))
	}
}

// FS-010: Rescan 在无已加载目录时报错。
func TestFileSkillProvider_Rescan_NoDirs(t *testing.T) {
	p := NewFileSkillProvider()
	_, err := p.Rescan(context.Background())
	if err == nil {
		t.Error("Rescan with no dirs should fail")
	}
}

// FS-011: 递归扫描子目录。
func TestFileSkillProvider_LoadSkills_Recursive(t *testing.T) {
	dir := t.TempDir()

	// 创建嵌套目录结构
	nestedDir := filepath.Join(dir, "parent", "child-skill")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: child-skill\ndescription: Nested skill\n---\n# Nested"
	if err := os.WriteFile(filepath.Join(nestedDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	if skills[0].Name != "child-skill" {
		t.Errorf("Name = %q, want %q", skills[0].Name, "child-skill")
	}
}

// FS-012: frontmatter 中引号包裹的值。
func TestFileSkillProvider_Frontmatter_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "quoted")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: \"quoted-skill\"\ndescription: 'A quoted description'\n---\n# Content"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	if skills[0].Name != "quoted-skill" {
		t.Errorf("Name = %q, want %q", skills[0].Name, "quoted-skill")
	}
	if skills[0].Description != "A quoted description" {
		t.Errorf("Description = %q, want %q", skills[0].Description, "A quoted description")
	}
}

// FS-013: LoadSkills 重复目录不重复加载。
func TestFileSkillProvider_LoadSkills_DuplicateDir(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: test-skill\ndescription: Test\n---\n# Test"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	_, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("first LoadSkills: %v", err)
	}
	_, err = p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("second LoadSkills: %v", err)
	}

	skills, _ := p.Available(context.Background())
	if len(skills) != 1 {
		t.Errorf("Available count = %d, want 1 (no duplicates)", len(skills))
	}
}

// FS-014: 并发访问不会 panic。
func TestFileSkillProvider_Concurrent(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		name := "skill-" + string(rune('A'+i))
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + "\n---\n# " + name
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := NewFileSkillProvider()
	var wg sync.WaitGroup

	for range 10 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, _ = p.LoadSkills(context.Background(), []string{dir})
		}()
		go func() {
			defer wg.Done()
			_, _ = p.Available(context.Background())
		}()
		go func() {
			defer wg.Done()
			_, _ = p.Rescan(context.Background())
		}()
	}

	wg.Wait()
}

// FS-015: extractFrontmatter 单元测试。
func TestExtractFrontmatter(t *testing.T) {
	tests := []struct {
		name string
		input string
		wantName string
		wantDesc string
		wantBody string
		wantOK bool
	}{
		{
			name: "valid frontmatter",
			input: "---\nname: test\ndescription: desc\n---\n# Body",
			wantName: "test",
			wantDesc: "desc",
			wantBody: "# Body",
			wantOK: true,
		},
		{
			name: "no frontmatter",
			input: "# Just a title\nSome content",
			wantOK: false,
		},
		{
			name: "empty body after frontmatter",
			input: "---\nname: empty\ndescription: empty desc\n---\n",
			wantName: "empty",
			wantDesc: "empty desc",
			wantBody: "",
			wantOK: true,
		},
		{
			name: "frontmatter with extra whitespace",
			input: "---\n name: spaced \n description: spaced desc \n---\n# Body",
			wantName: "spaced",
			wantDesc: "spaced desc",
			wantBody: "# Body",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := extractFrontmatter(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if result.name != tt.wantName {
				t.Errorf("name = %q, want %q", result.name, tt.wantName)
			}
			if result.description != tt.wantDesc {
				t.Errorf("description = %q, want %q", result.description, tt.wantDesc)
			}
			if !strings.Contains(result.body, tt.wantBody) {
				t.Errorf("body = %q, want to contain %q", result.body, tt.wantBody)
			}
		})
	}
}

// FS-016: parseSimpleYAML 单元测试。
func TestParseSimpleYAML(t *testing.T) {
	tests := []struct {
		name string
		input string
		wantName string
		wantDesc string
	}{
		{
			name: "basic key-value",
			input: "name: hello\ndescription: world",
			wantName: "hello",
			wantDesc: "world",
		},
		{
			name: "quoted values",
			input: "name: \"hello\"\ndescription: 'world'",
			wantName: "hello",
			wantDesc: "world",
		},
		{
			name: "with comments",
			input: "# comment\nname: test\ndescription: desc",
			wantName: "test",
			wantDesc: "desc",
		},
		{
			name: "empty input",
			input: "",
			wantName: "",
			wantDesc: "",
		},
		{
			name: "extra fields ignored",
			input: "name: test\nauthor: someone\ndescription: desc\nversion: 1.0",
			wantName: "test",
			wantDesc: "desc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, desc := parseSimpleYAML(tt.input)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if desc != tt.wantDesc {
				t.Errorf("description = %q, want %q", desc, tt.wantDesc)
			}
		})
	}
}

// FS-017: SKILL.md 在根目录（无子目录包装）。
func TestFileSkillProvider_SkillMD_InRootDir(t *testing.T) {
	dir := t.TempDir()
	// SKILL.md 直接在扫描目录下
	content := "---\nname: root-skill\ndescription: Root level\n---\n# Root"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1", len(skills))
	}
	if skills[0].Name != "root-skill" {
		t.Errorf("Name = %q, want %q", skills[0].Name, "root-skill")
	}
}

// FS-018: 修改 SKILL.md 后 Rescan 能获取新内容。
func TestFileSkillProvider_Rescan_UpdatedContent(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "up-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 初始内容
	content := "---\nname: up-skill\ndescription: V1\n---\n# V1"
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	_, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// 更新内容
	updatedContent := "---\nname: up-skill\ndescription: V2\n---\n# V2"
	if err := os.WriteFile(skillFile, []byte(updatedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Rescan
	skills, err := p.Rescan(context.Background())
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("Rescan count = %d, want 1", len(skills))
	}
	if skills[0].Description != "V2" {
		t.Errorf("Description after Rescan = %q, want %q", skills[0].Description, "V2")
	}
	if !strings.Contains(skills[0].Content, "# V2") {
		t.Errorf("Content after Rescan should contain V2, got: %q", skills[0].Content)
	}
}

// FS-019: 删除 SKILL.md 后 Rescan 应减少 skill 数量。
func TestFileSkillProvider_Rescan_DeletedFile(t *testing.T) {
	dir := t.TempDir()

	// 创建两个 skill
	for _, name := range []string{"keep-skill", "remove-skill"} {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + "\n---\n# " + name
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := NewFileSkillProvider()
	_, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// 删除一个 SKILL.md
	if err := os.Remove(filepath.Join(dir, "remove-skill", "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	// Rescan
	skills, err := p.Rescan(context.Background())
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("Rescan count = %d, want 1", len(skills))
	}
	if skills[0].Name != "keep-skill" {
		t.Errorf("remaining skill Name = %q, want %q", skills[0].Name, "keep-skill")
	}
}

// FS-020: Available 返回的结果按名称排序。
func TestFileSkillProvider_Available_SortedOrder(t *testing.T) {
	p := NewFileSkillProvider()
	dir := t.TempDir()

	names := []string{"zulu", "alpha", "mike"}
	for _, name := range names {
		skillDir := filepath.Join(dir, name)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + "\n---\n# " + name
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, _ = p.LoadSkills(context.Background(), []string{dir})
	skills, err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}

	// 验证排序
	sorted := make([]string, len(skills))
	for i, s := range skills {
		sorted[i] = s.Name
	}
	if !sort.StringsAreSorted(sorted) {
		t.Errorf("Available not sorted: %v", sorted)
	}
}
