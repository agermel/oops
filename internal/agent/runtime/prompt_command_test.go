package runtime

import (
	"errors"
	"testing"

	agentskills "oops/internal/agent/runtime/skills"
)

func TestResolvePromptCommandRoutesUsingHarnessPrecedence(t *testing.T) {
	t.Parallel()

	templates := []PromptTemplate{
		{Name: "diagnose", Content: "template $1"},
		{Name: "skill:diagnose", Content: "namespaced template"},
	}
	skills := []agentskills.Skill{
		{Name: "diagnose", Content: "skill body", FilePath: "/skills/diagnose/SKILL.md", Enabled: true},
		{Name: "disabled", Content: "disabled body", FilePath: "/skills/disabled/SKILL.md", Enabled: false},
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "namespaced skill", input: "/skill:diagnose inspect target", want: "<skill name=\"diagnose\" location=\"/skills/diagnose/SKILL.md\">\nReferences are relative to /skills/diagnose.\n\nskill body\n</skill>\n\ninspect target"},
		{name: "template before shorthand", input: "/diagnose target", want: "template target"},
		{name: "unknown shorthand", input: "/missing target", want: "/missing target"},
		{name: "disabled shorthand", input: "/disabled target", want: "/disabled target"},
		{name: "plain text", input: "inspect target", want: "inspect target"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ResolvePromptCommand(test.input, templates, skills)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("ResolvePromptCommand() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolvePromptCommandValidatesNamespacedSkills(t *testing.T) {
	t.Parallel()

	skills := []agentskills.Skill{{Name: "disabled", Enabled: false}}
	for _, input := range []string{"/skill:", "/skill: disabled"} {
		if _, err := ResolvePromptCommand(input, nil, skills); !errors.Is(err, ErrPromptSkillNameRequired) {
			t.Fatalf("ResolvePromptCommand(%q) error = %v", input, err)
		}
	}
	for _, input := range []string{"/skill:missing", "/skill:disabled", "/skill:Disabled"} {
		if _, err := ResolvePromptCommand(input, nil, skills); !errors.Is(err, ErrPromptSkillUnavailable) {
			t.Fatalf("ResolvePromptCommand(%q) error = %v", input, err)
		}
	}
	if got, err := ResolvePromptCommand("/Skill:disabled", nil, skills); err != nil || got != "/Skill:disabled" {
		t.Fatalf("uppercase namespace = %q, %v", got, err)
	}
}
