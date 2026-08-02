package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	agentruntime "oops/internal/agent/runtime"
	"oops/internal/agent/runtime/skills"
)

func TestResolveRunPromptNamespacedSkill(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}

	if err := server.validateRunPrompt(`/skill:diagnose inspect <target>`, server.enabledSkillSnapshot()); err != nil {
		t.Fatalf("validateRunPrompt: %v", err)
	}
}

func TestResolveRunPromptShorthandSkill(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}

	if err := server.validateRunPrompt(`/diagnose`, server.enabledSkillSnapshot()); err != nil {
		t.Fatalf("validateRunPrompt: %v", err)
	}
}

func TestResolveRunPromptBoundaries(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}
	available := server.enabledSkillSnapshot()
	tests := []struct {
		name string
		in   string
	}{
		{name: "unknown shorthand", in: "/missing args"},
		{name: "bare skill command", in: "/skill"},
		{name: "uppercase command prefix", in: "/Skill:diagnose"},
		{name: "colon in shorthand", in: "/other:diagnose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := server.validateRunPrompt(tt.in, available); err != nil {
				t.Fatalf("validateRunPrompt(%q): %v", tt.in, err)
			}
		})
	}
}

func TestResolveRunPromptErrors(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}
	available := server.enabledSkillSnapshot()
	tests := []struct {
		name   string
		in     string
		status int
	}{
		{name: "empty namespaced", in: "/skill:", status: http.StatusBadRequest},
		{name: "space after namespace", in: "/skill: missing", status: http.StatusBadRequest},
		{name: "unknown namespaced", in: "/skill:missing", status: http.StatusBadRequest},
		{name: "disabled namespaced", in: "/skill:disabled", status: http.StatusBadRequest},
		{name: "case sensitive", in: "/skill:Diagnose", status: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := server.validateRunPrompt(tt.in, available)
			assertSkillCommandError(t, err, tt.status)
		})
	}
}

func TestResolveRunPromptNoSkillStore(t *testing.T) {
	server := &Server{}

	if err := server.validateRunPrompt("/diagnose", nil); err != nil {
		t.Fatalf("shorthand without skill store should pass through: %v", err)
	}

	err := server.validateRunPrompt("/skill:diagnose", nil)
	assertSkillCommandError(t, err, http.StatusServiceUnavailable)
}

func TestValidateRunPromptAcceptsTemplatesAndShorthandSkills(t *testing.T) {
	server := &Server{
		skillStore: newTestSkillStore(t),
		promptTemplates: []agentruntime.PromptTemplate{
			{Name: "review", Content: "Review $1 with ${@:2}"},
			{Name: "diagnose", Content: "template wins shorthand"},
			{Name: "skill:diagnose", Content: "template should not win namespace"},
		},
	}
	available := server.enabledSkillSnapshot()

	for _, input := range []string{`/review target "extra context"`, "/diagnose", "/skill:diagnose"} {
		if err := server.validateRunPrompt(input, available); err != nil {
			t.Fatalf("validateRunPrompt(%q): %v", input, err)
		}
	}
}

func assertSkillCommandError(t *testing.T, err error, status int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected skill command error")
	}
	commandErr, ok := err.(skillCommandError)
	if !ok {
		t.Fatalf("err = %T %[1]v, want skillCommandError", err)
	}
	if commandErr.status != status {
		t.Fatalf("status = %d, want %d", commandErr.status, status)
	}
}

func newTestSkillStore(t *testing.T) *skills.Store {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"diagnose.md": `---
name: diagnose
description: Diagnose problems
---
Use <probe> & report.
`,
		"disabled.md": `---
name: disabled
description: Disabled skill
enabled: false
---
Disabled.
`,
	}
	for name, content := range files {
		skillDir := filepath.Join(dir, name[:len(name)-len(filepath.Ext(name))])
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("create skill dir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatalf("write skill %s: %v", name, err)
		}
	}
	store, err := skills.NewStore(dir)
	if err != nil {
		t.Fatalf("skills.NewStore: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}
