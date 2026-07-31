package skill

import (
	"context"
	"errors"
	"testing"
)

// mockSkillProvider 实现SkillProvider接口，用于编译契约验证与基本行为测试。
type mockSkillProvider struct {
	skills map[string]SkillDefinition
	dirs []string
}

func newMockSkillProvider() *mockSkillProvider {
	return &mockSkillProvider{skills: make(map[string]SkillDefinition)}
}

func (p *mockSkillProvider) LoadSkills(_ context.Context, dirs []string) ([]SkillDefinition, error) {
	p.dirs = dirs
	for _, d := range dirs {
		name := d + "-skill"
		p.skills[name] = SkillDefinition{
			Name: name,
			Description: "skill from " + d,
			Content: "# " + name,
			Source: d,
		}
	}
	out := make([]SkillDefinition, 0, len(p.skills))
	for _, s := range p.skills {
		out = append(out, s)
	}
	return out, nil
}

func (p *mockSkillProvider) Rescan(_ context.Context) ([]SkillDefinition, error) {
	if len(p.dirs) == 0 {
		return nil, errors.New("no dirs loaded")
	}
	out := make([]SkillDefinition, 0, len(p.skills))
	for _, s := range p.skills {
		out = append(out, s)
	}
	return out, nil
}

func (p *mockSkillProvider) Available(_ context.Context) ([]SkillDefinition, error) {
	out := make([]SkillDefinition, 0, len(p.skills))
	for _, s := range p.skills {
		out = append(out, s)
	}
	return out, nil
}

// Interface-001: SkillProvider 接口可被 mock 实现。
func TestSkillProvider_InterfaceContract(t *testing.T) {
	var _ SkillProvider = (*mockSkillProvider)(nil)
}

// VT-001: LoadSkills 加载目录并返回 Skill 列表。
func TestSkillProvider_LoadSkills(t *testing.T) {
	p := newMockSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{"dir-a", "dir-b"})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("skills count = %d, want 2", len(skills))
	}
}

// VT-002: Available 列出当前可用 Skill。
func TestSkillProvider_Available(t *testing.T) {
	p := newMockSkillProvider()
	_, _ = p.LoadSkills(context.Background(), []string{"dir-a"})
	skills, err := p.Available(context.Background())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("Available count = %d, want 1", len(skills))
	}
	if skills[0].Name != "dir-a-skill" {
		t.Errorf("Available[0].Name = %q, want %q", skills[0].Name, "dir-a-skill")
	}
}

// VT-003: Rescan 重新扫描已加载目录。
func TestSkillProvider_Rescan(t *testing.T) {
	p := newMockSkillProvider()
	_, _ = p.LoadSkills(context.Background(), []string{"dir-a"})
	skills, err := p.Rescan(context.Background())
	if err != nil {
		t.Fatalf("Rescan: %v", err)
	}
	if len(skills) != 1 {
		t.Errorf("Rescan count = %d, want 1", len(skills))
	}
}

// VT-004: Rescan 在无已加载目录时报错。
func TestSkillProvider_RescanNoDirs(t *testing.T) {
	p := newMockSkillProvider()
	if _, err := p.Rescan(context.Background()); err == nil {
		t.Error("Rescan with no dirs should fail")
	}
}

// VT-005: SkillDefinition 字段完整传递。
func TestSkillDefinition_Fields(t *testing.T) {
	p := newMockSkillProvider()
	skills, _ := p.LoadSkills(context.Background(), []string{"mydir"})
	if len(skills) == 0 {
		t.Fatal("no skills loaded")
	}
	s := skills[0]
	if s.Source != "mydir" {
		t.Errorf("Source = %q, want %q", s.Source, "mydir")
	}
	if s.Content == "" {
		t.Error("Content should not be empty")
	}
}
