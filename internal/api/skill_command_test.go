package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentruntime "oops/internal/agent/runtime"
)

func TestExpandSkillCommandNamespaced(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}

	got, err := server.expandSkillCommand(`/skill:diagnose inspect <target>`)
	if err != nil {
		t.Fatalf("expandSkillCommand: %v", err)
	}
	if !strings.Contains(got, `<skill_content name="diagnose">`) {
		t.Fatalf("missing skill block:\n%s", got)
	}
	if !strings.Contains(got, `Use &lt;probe&gt; &amp; report.`) {
		t.Fatalf("skill content was not escaped:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\ninspect <target>") {
		t.Fatalf("instructions not preserved outside XML:\n%s", got)
	}
}

func TestExpandSkillCommandShorthand(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}

	got, err := server.expandSkillCommand(`/diagnose`)
	if err != nil {
		t.Fatalf("expandSkillCommand: %v", err)
	}
	if !strings.Contains(got, `<skill_content name="diagnose">`) {
		t.Fatalf("missing skill block:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("empty instructions should not add extra separator:\n%s", got)
	}
}

func TestExpandSkillCommandBoundaries(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "unknown shorthand", in: "/missing args", want: "/missing args"},
		{name: "bare skill command", in: "/skill", want: "/skill"},
		{name: "uppercase command prefix", in: "/Skill:diagnose", want: "/Skill:diagnose"},
		{name: "colon in shorthand", in: "/other:diagnose", want: "/other:diagnose"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := server.expandSkillCommand(tt.in)
			if err != nil {
				t.Fatalf("expandSkillCommand: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expandSkillCommand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandSkillCommandErrors(t *testing.T) {
	server := &Server{skillStore: newTestSkillStore(t)}
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
			_, err := server.expandSkillCommand(tt.in)
			assertSkillCommandError(t, err, tt.status)
		})
	}
}

func TestExpandSkillCommandNoSkillStore(t *testing.T) {
	server := &Server{}

	got, err := server.expandSkillCommand("/diagnose")
	if err != nil {
		t.Fatalf("shorthand without skill store should pass through: %v", err)
	}
	if got != "/diagnose" {
		t.Fatalf("shorthand without skill store = %q", got)
	}

	_, err = server.expandSkillCommand("/skill:diagnose")
	assertSkillCommandError(t, err, http.StatusServiceUnavailable)
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

func newTestSkillStore(t *testing.T) *agentruntime.SkillStore {
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
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write skill %s: %v", name, err)
		}
	}
	store, err := agentruntime.NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}
