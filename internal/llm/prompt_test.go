package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewPromptStore(t *testing.T) {
	dir := t.TempDir()

	// 创建测试 prompt 文件。
	content := `system: |
  你是一个助手。

diagnose: |
  你是一个诊断专家。`
	if err := os.WriteFile(filepath.Join(dir, "test.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ps, err := NewPromptStore(dir)
	if err != nil {
		t.Fatalf("NewPromptStore() error = %v", err)
	}
	defer ps.Close()

	if got := ps.Get("system"); got == "" {
		t.Error("Get(system) returned empty")
	}
	if got := ps.Get("diagnose"); got == "" {
		t.Error("Get(diagnose) returned empty")
	}
	if got := ps.Get("nonexistent"); got != "" {
		t.Errorf("Get(nonexistent) = %q, want empty", got)
	}
}

func TestPromptStore_Resolve(t *testing.T) {
	dir := t.TempDir()

	content := `system: |
  default prompt

diagnose: |
  diagnose prompt

inspect: |
  inspect prompt`
	if err := os.WriteFile(filepath.Join(dir, "prompts.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ps, err := NewPromptStore(dir)
	if err != nil {
		t.Fatalf("NewPromptStore() error = %v", err)
	}
	defer ps.Close()

	tests := []struct {
		question   string
		wantName   string
		wantPrefix string
	}{
		{"MySQL 报错了", "diagnose", "diagnose prompt"},
		{"巡检一下", "inspect", "inspect prompt"},
		{"你好", "system", "default prompt"},
	}

	for _, tt := range tests {
		content, name := ps.Resolve(tt.question)
		if name != tt.wantName {
			t.Errorf("Resolve(%q) name = %q, want %q", tt.question, name, tt.wantName)
		}
		// YAML | 块标量会去掉尾随换行，使用前缀匹配。
		if content != tt.wantPrefix && content != tt.wantPrefix+"\n" {
			t.Errorf("Resolve(%q) content = %q, want prefix %q", tt.question, content, tt.wantPrefix)
		}
	}
}

func TestPromptStore_FileReload(t *testing.T) {
	dir := t.TempDir()

	content := `system: |
  version 1`
	path := filepath.Join(dir, "prompts.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ps, err := NewPromptStore(dir)
	if err != nil {
		t.Fatalf("NewPromptStore() error = %v", err)
	}
	defer ps.Close()

	got := strings.TrimSpace(ps.Get("system"))
	if got != "version 1" {
		t.Errorf("initial = %q, want %q", got, "version 1")
	}

	// 修改文件。
	updated := `system: |
  version 2`
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatal(err)
	}

	// 手动触发重新加载。
	if err := ps.loadAll(); err != nil {
		t.Fatal(err)
	}

	got = strings.TrimSpace(ps.Get("system"))
	if got != "version 2" {
		t.Errorf("after reload = %q, want %q", got, "version 2")
	}
}

func TestPromptStore_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	ps, err := NewPromptStore(dir)
	if err != nil {
		t.Fatalf("NewPromptStore() error = %v", err)
	}
	defer ps.Close()

	if got := ps.Get("system"); got != "" {
		t.Errorf("Get(system) = %q, want empty", got)
	}
}
