package runtime

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// FormatSkillContent renders a skill as the XML-like block used in agent prompts.
func FormatSkillContent(skill *Skill) string {
	if skill == nil {
		return ""
	}
	name := escapeXML(skill.Name)
	return strings.Join([]string{
		`<skill_content name="` + name + `">`,
		"# Skill: " + name,
		"",
		escapeXML(skill.Content),
		"</skill_content>",
	}, "\n")
}

// FormatSkillInvocation appends user instructions outside the skill XML block.
func FormatSkillInvocation(skill *Skill, instructions string) string {
	block := FormatSkillContent(skill)
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return block
	}
	return block + "\n\n" + instructions
}

func escapeXML(value string) string {
	var out bytes.Buffer
	if err := xml.EscapeText(&out, []byte(value)); err != nil {
		return value
	}
	return out.String()
}
