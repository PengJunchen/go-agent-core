package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// AC-3: .gitignore patterns exclude files from scanning.
func TestFileSkillProvider_GitignoreExcludesFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a skill directory that should be found.
	skillDir := filepath.Join(dir, "visible-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: visible-skill\ndescription: Visible\n---\n# Visible"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an ignored directory with a SKILL.md.
	ignoredDir := filepath.Join(dir, "ignored-skill")
	if err := os.MkdirAll(ignoredDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ignoredContent := "---\nname: ignored-skill\ndescription: Ignored\n---\n# Ignored"
	if err := os.WriteFile(filepath.Join(ignoredDir, "SKILL.md"), []byte(ignoredContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write .gitignore that excludes "ignored-skill/".
	gitignoreContent := "ignored-skill/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	if len(skills) != 1 {
		t.Fatalf("skills count = %d, want 1 (ignored-skill excluded)", len(skills))
	}
	if skills[0].Name != "visible-skill" {
		t.Errorf("skill name = %q, want %q", skills[0].Name, "visible-skill")
	}
}

// AC-3: .gitignore with comments and empty lines.
func TestFileSkillProvider_GitignoreWithComments(t *testing.T) {
	dir := t.TempDir()

	// Create skill to find.
	skillDir := filepath.Join(dir, "keep-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: keep-skill\ndescription: Keep\n---\n# Keep"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create excluded skill.
	excludedDir := filepath.Join(dir, "excluded")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	excludedContent := "---\nname: excluded\ndescription: Excluded\n---\n# Excluded"
	if err := os.WriteFile(filepath.Join(excludedDir, "SKILL.md"), []byte(excludedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// .gitignore with comments and empty lines.
	gitignoreContent := "# This is a comment\n\nexcluded/\n\n# another comment\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0o644); err != nil {
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
	if skills[0].Name != "keep-skill" {
		t.Errorf("skill name = %q, want %q", skills[0].Name, "keep-skill")
	}
}

// AC-3: default ignored patterns (node_modules, dist, .git, vendor) are always excluded.
func TestFileSkillProvider_DefaultIgnoredPatterns(t *testing.T) {
	for _, pattern := range []string{"node_modules", "dist", ".git", "vendor"} {
		t.Run(pattern, func(t *testing.T) {
			dir := t.TempDir()

			// Create a skill in the ignored directory.
			ignoredDir := filepath.Join(dir, pattern)
			if err := os.MkdirAll(ignoredDir, 0o755); err != nil {
				t.Fatal(err)
			}
			content := "---\nname: inside-ignored\ndescription: Ignored\n---\n# Ignored"
			if err := os.WriteFile(filepath.Join(ignoredDir, "SKILL.md"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			p := NewFileSkillProvider()
			skills, err := p.LoadSkills(context.Background(), []string{dir})
			if err != nil {
				t.Fatalf("LoadSkills: %v", err)
			}

			for _, s := range skills {
				if s.Name == "inside-ignored" {
					t.Errorf("skill inside %s/ should not be found", pattern)
				}
			}
		})
	}
}

// AC-4: Symlink deduplication — symlinks are not followed.
func TestFileSkillProvider_SymlinkDeduplication(t *testing.T) {
	dir := t.TempDir()

	// Create a real skill directory.
	realDir := filepath.Join(dir, "real-skill")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: real-skill\ndescription: Real\n---\n# Real"
	if err := os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink to the real skill directory.
	symlinkPath := filepath.Join(dir, "symlink-skill")
	if err := os.Symlink(realDir, symlinkPath); err != nil {
		// Skip if symlinks are not supported (e.g., Windows without privileges).
		t.Skipf("cannot create symlink: %v", err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	// Should find only the real skill, not the symlink.
	if len(skills) != 1 {
		names := make([]string, len(skills))
		for i, s := range skills {
			names[i] = s.Name
		}
		t.Fatalf("skills count = %d, want 1 (symlink should be skipped), got: %v", len(skills), names)
	}
	if skills[0].Name != "real-skill" {
		t.Errorf("skill name = %q, want %q", skills[0].Name, "real-skill")
	}
}

// Unit test for parseGitignore.
func TestParseGitignore(t *testing.T) {
	dir := t.TempDir()
	content := "# comment\n\nnode_modules/\n*.log\ndist/\n"
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	patterns, err := parseGitignore(path)
	if err != nil {
		t.Fatalf("parseGitignore: %v", err)
	}

	// Should have 3 patterns (comments and empty lines excluded).
	if len(patterns) != 3 {
		t.Fatalf("patterns count = %d, want 3, got: %v", len(patterns), patterns)
	}

	found := make(map[string]bool)
	for _, p := range patterns {
		found[p] = true
	}
	if !found["node_modules/"] {
		t.Error("expected node_modules/ in patterns")
	}
	if !found["*.log"] {
		t.Error("expected *.log in patterns")
	}
	if !found["dist/"] {
		t.Error("expected dist/ in patterns")
	}
}

// Unit test for parseGitignore with nonexistent file.
func TestParseGitignore_NonexistentFile(t *testing.T) {
	_, err := parseGitignore("/nonexistent/.gitignore")
	if err == nil {
		t.Error("expected error for nonexistent .gitignore")
	}
}

// Unit test for matchGitignorePattern.
func TestMatchGitignorePattern(t *testing.T) {
	tests := []struct {
		name string
		path string
		patterns []string
		want bool
	}{
		{
			name: "exact directory match",
			path: "ignored-skill",
			patterns: []string{"ignored-skill/"},
			want: true,
		},
		{
			name: "no match",
			path: "visible-skill",
			patterns: []string{"ignored-skill/"},
			want: false,
		},
		{
			name: "glob pattern match",
			path: "debug.log",
			patterns: []string{"*.log"},
			want: true,
		},
		{
			name: "glob pattern no match",
			path: "readme.txt",
			patterns: []string{"*.log"},
			want: false,
		},
		{
			name: "multiple patterns one matches",
			path: "dist",
			patterns: []string{"node_modules/", "*.log", "dist/"},
			want: true,
		},
		{
			name: "empty patterns",
			path: "anything",
			patterns: nil,
			want: false,
		},
		{
			name: "pattern without slash matches exact name",
			path: "secret",
			patterns: []string{"secret"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchGitignorePattern(tt.path, tt.patterns)
			if got != tt.want {
				t.Errorf("matchGitignorePattern(%q, %v) = %v, want %v",
					tt.path, tt.patterns, got, tt.want)
			}
		})
	}
}

// AC-3: .gitignore in subdirectory is also respected.
func TestFileSkillProvider_NestedGitignore(t *testing.T) {
	dir := t.TempDir()

	// Create parent directory with a visible skill.
	parentSkillDir := filepath.Join(dir, "parent-skill")
	if err := os.MkdirAll(parentSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: parent-skill\ndescription: Parent\n---\n# Parent"
	if err := os.WriteFile(filepath.Join(parentSkillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory with its own .gitignore.
	subDir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Skill in subdir that should be found.
	visibleInSub := filepath.Join(subDir, "sub-visible")
	if err := os.MkdirAll(visibleInSub, 0o755); err != nil {
		t.Fatal(err)
	}
	subContent := "---\nname: sub-visible\ndescription: Sub Visible\n---\n# Sub"
	if err := os.WriteFile(filepath.Join(visibleInSub, "SKILL.md"), []byte(subContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ignored skill in subdir.
	ignoredInSub := filepath.Join(subDir, "sub-ignored")
	if err := os.MkdirAll(ignoredInSub, 0o755); err != nil {
		t.Fatal(err)
	}
	ignoredContent := "---\nname: sub-ignored\ndescription: Sub Ignored\n---\n# Ignored"
	if err := os.WriteFile(filepath.Join(ignoredInSub, "SKILL.md"), []byte(ignoredContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// .gitignore in subdir.
	gitignoreContent := "sub-ignored/\n"
	if err := os.WriteFile(filepath.Join(subDir, ".gitignore"), []byte(gitignoreContent), 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewFileSkillProvider()
	skills, err := p.LoadSkills(context.Background(), []string{dir})
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}

	names := make(map[string]bool)
	for _, s := range skills {
		names[s.Name] = true
	}
	if !names["parent-skill"] {
		t.Error("expected parent-skill to be found")
	}
	if !names["sub-visible"] {
		t.Error("expected sub-visible to be found")
	}
	if names["sub-ignored"] {
		t.Error("sub-ignored should not be found")
	}
}
