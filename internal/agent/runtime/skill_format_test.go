package runtime

import (
	"strings"
	"testing"
)

func TestFormatSkillInvocationEscapesSkillBlockOnly(t *testing.T) {
	skill := &Skill{
		Name:    `diag&"nose`,
		Content: `Use <probe> & report "why".`,
	}

	got := FormatSkillInvocation(skill, `write <draft> & explain "why"`)

	if !strings.Contains(got, `<skill_content name="diag&amp;&#34;nose">`) {
		t.Fatalf("skill name was not escaped in XML attribute:\n%s", got)
	}
	if !strings.Contains(got, `Use &lt;probe&gt; &amp; report &#34;why&#34;.`) {
		t.Fatalf("skill content was not escaped in XML body:\n%s", got)
	}
	if !strings.HasSuffix(got, "\n\n"+`write <draft> & explain "why"`) {
		t.Fatalf("instructions should remain unescaped outside XML block:\n%s", got)
	}
}

func TestFormatSkillInvocationOmitsBlankInstructions(t *testing.T) {
	skill := &Skill{Name: "diagnose", Content: "body"}
	got := FormatSkillInvocation(skill, "  \n\t")
	if strings.Contains(got, "\n\n\n") {
		t.Fatalf("blank instructions added extra separator:\n%s", got)
	}
	if strings.Contains(got, "\t") {
		t.Fatalf("blank instructions should be omitted:\n%s", got)
	}
}
