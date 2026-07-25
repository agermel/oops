package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

func TestSkillStoreCloseWaitsForWatcher(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "skill.md"), []byte("---\nname: test\ndescription: test\n---\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	store.Close()
	store.Close()
}

func TestSkillStore_LoadFromDir(t *testing.T) {
	dir := t.TempDir()

	// Write a test skill file.
	content := `---
name: test-skill
description: A test skill for unit testing
icon: Bug
label: 测试
color: custom
---
这是测试技能的内容。
`
	if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ss, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore() error = %v", err)
	}
	defer ss.Close()

	skill, ok := ss.Get("test-skill")
	if !ok {
		t.Fatal("expected test-skill to be loaded")
	}

	if skill.Name != "test-skill" {
		t.Errorf("Name = %q, want %q", skill.Name, "test-skill")
	}
	if skill.Description != "A test skill for unit testing" {
		t.Errorf("Description = %q", skill.Description)
	}
	if skill.Icon != "Bug" {
		t.Errorf("Icon = %q, want %q", skill.Icon, "Bug")
	}
	if skill.Label != "测试" {
		t.Errorf("Label = %q, want %q", skill.Label, "测试")
	}
	if skill.Color != "custom" {
		t.Errorf("Color = %q, want %q", skill.Color, "custom")
	}
	if !skill.Enabled {
		t.Error("Enabled should default to true")
	}
	if skill.Content != "这是测试技能的内容。" {
		t.Errorf("Content = %q", skill.Content)
	}
}

func TestSkillStore_RenderAvailable(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "a.md"), []byte(`---
name: a
description: Skill A
---
body
`), 0644)

	os.WriteFile(filepath.Join(dir, "b.md"), []byte(`---
name: b
description: Skill B
enabled: false
---
body
`), 0644)

	ss, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore() error = %v", err)
	}
	defer ss.Close()

	xml := ss.RenderAvailable()
	if !strings.Contains(xml, "<name>a</name>") {
		t.Error("expected skill 'a' in available skills")
	}
	if !strings.Contains(xml, "<description>Skill A</description>") {
		t.Error("expected description for skill 'a'")
	}
	if strings.Contains(xml, "<name>b</name>") {
		t.Error("disabled skill 'b' should not appear")
	}
	if !strings.Contains(xml, "<available_skills>") {
		t.Error("expected <available_skills> tag")
	}
}

func TestSkillStore_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()

	// File without frontmatter — entire file is body, name comes from filename edge case.
	// Actually without frontmatter, parseFrontmatter returns nil, nil which causes yaml unmarshal to create zero Skill.
	// Name="" → error. So this skill would be skipped.
	os.WriteFile(filepath.Join(dir, "nofm.md"), []byte(`Just some markdown`), 0644)

	ss, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore() error = %v", err)
	}
	defer ss.Close()

	// No skills should be loaded because the file has no valid frontmatter.
	skills := ss.List()
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestSkillStore_EnabledDefault(t *testing.T) {
	dir := t.TempDir()

	// enabled not in frontmatter → should default to true.
	os.WriteFile(filepath.Join(dir, "default.md"), []byte(`---
name: default
---
Default skill content.
`), 0644)

	ss, err := NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore() error = %v", err)
	}
	defer ss.Close()

	s, ok := ss.Get("default")
	if !ok {
		t.Fatal("expected default skill")
	}
	if !s.Enabled {
		t.Error("Enabled should default to true when not specified in frontmatter")
	}
}

func TestLoadSkillFileFrontmatterCharacterization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		content     string
		wantName    string
		wantContent string
		wantEnabled bool
		wantErr     bool
	}{
		{
			name:        "CRLF",
			content:     "---\r\nname: crlf\r\n---\r\n第一行\r\n第二行\r\n",
			wantName:    "crlf",
			wantContent: "第一行\r\n第二行",
			wantEnabled: true,
		},
		{
			name:        "empty body",
			content:     "---\nname: empty\n---\n",
			wantName:    "empty",
			wantContent: "",
			wantEnabled: true,
		},
		{
			name:        "missing closing delimiter",
			content:     "---\nname: unfinished\ndescription: still metadata\n",
			wantName:    "unfinished",
			wantContent: "",
			wantEnabled: true,
		},
		{
			name:    "invalid YAML",
			content: "---\nname: [\n---\nbody\n",
			wantErr: true,
		},
		{
			name:        "enabled default",
			content:     "---\nname: default-enabled\n---\nbody\n",
			wantName:    "default-enabled",
			wantContent: "body",
			wantEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "skill.md")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write skill: %v", err)
			}

			skill, err := loadSkillFile(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("loadSkillFile() error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadSkillFile() error = %v", err)
			}
			if skill.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", skill.Name, tt.wantName)
			}
			if skill.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", skill.Content, tt.wantContent)
			}
			if skill.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %t, want %t", skill.Enabled, tt.wantEnabled)
			}
		})
	}
}
