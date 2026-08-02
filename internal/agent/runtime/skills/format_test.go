package skills

import (
	"strings"
	"testing"
)

func TestFormatInvocationEscapesMetadataAndPreservesBody(t *testing.T) {
	skill := &Skill{
		Name:     `diag&"nose`,
		FilePath: `/project/a&"b/diag/SKILL.md`,
		Content:  `Use <probe> & report "why".`,
	}

	got := FormatInvocation(skill, `write <draft> & explain "why"`)
	want := `<skill name="diag&amp;&#34;nose" location="/project/a&amp;&#34;b/diag/SKILL.md">
References are relative to /project/a&amp;&#34;b/diag.

Use <probe> & report "why".
</skill>

write <draft> & explain "why"`
	if got != want {
		t.Fatalf("FormatInvocation() =\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatInvocationOmitsEmptyInstructions(t *testing.T) {
	skill := &Skill{Name: "diagnose", FilePath: "/skills/diagnose/SKILL.md", Content: "body"}
	got := FormatInvocation(skill, "")
	if got != FormatContent(skill) {
		t.Fatalf("empty instructions changed the skill block:\n%s", got)
	}
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("empty instructions added an extra separator:\n%s", got)
	}
}

func TestFormatInvocationPreservesInstructionWhitespace(t *testing.T) {
	skill := &Skill{Name: "diagnose", FilePath: "/skills/diagnose/SKILL.md", Content: "body"}
	instructions := "  inspect carefully  \n"
	got := FormatInvocation(skill, instructions)
	if !strings.HasSuffix(got, "\n\n"+instructions) {
		t.Fatalf("instruction whitespace changed:\n%q", got)
	}
}

func TestFormatContentNil(t *testing.T) {
	if got := FormatContent(nil); got != "" {
		t.Fatalf("FormatContent(nil) = %q", got)
	}
	if got := FormatInvocation(nil, "instructions"); got != "" {
		t.Fatalf("FormatInvocation(nil) = %q", got)
	}
}

func TestFormatAvailableReturnsEmptyWithoutVisibleSkills(t *testing.T) {
	tests := []struct {
		name   string
		skills []Skill
	}{
		{name: "empty"},
		{name: "disabled", skills: []Skill{{Name: "disabled", Description: "Disabled", Enabled: false}}},
		{name: "explicit only", skills: []Skill{{Name: "hidden", Description: "Hidden", Enabled: true, DisableModelInvocation: true}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatAvailable(tt.skills); got != "" {
				t.Fatalf("FormatAvailable() = %q, want empty", got)
			}
		})
	}
}

func TestFormatAvailableFormatsVisibleSkillsInOrder(t *testing.T) {
	got := FormatAvailable([]Skill{
		{
			Name:        "visible",
			Description: "Use <this> & that",
			FilePath:    "/skills/visible/SKILL.md",
			Enabled:     true,
		},
		{
			Name:                   "hidden",
			Description:            "Hidden",
			FilePath:               "/skills/hidden/SKILL.md",
			Enabled:                true,
			DisableModelInvocation: true,
		},
		{
			Name:     "second",
			FilePath: "/skills/second/SKILL.md",
			Enabled:  true,
		},
	})
	want := `The following skills provide specialized instructions for specific tasks.
Read the full skill file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>visible</name>
    <description>Use &lt;this&gt; &amp; that</description>
    <location>/skills/visible/SKILL.md</location>
  </skill>
  <skill>
    <name>second</name>
    <description></description>
    <location>/skills/second/SKILL.md</location>
  </skill>
</available_skills>`
	if got != want {
		t.Fatalf("FormatAvailable() =\n%s\nwant:\n%s", got, want)
	}
}
