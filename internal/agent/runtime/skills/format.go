package skills

import (
	"bytes"
	"encoding/xml"
	"path/filepath"
	"strings"
)

// FormatContent renders a skill as the block used in agent prompts.
func FormatContent(skill *Skill) string {
	if skill == nil {
		return ""
	}
	return strings.Join([]string{
		`<skill name="` + escapeXML(skill.Name) + `" location="` + escapeXML(skill.FilePath) + `">`,
		"References are relative to " + escapeXML(filepath.Dir(skill.FilePath)) + ".",
		"",
		skill.Content,
		"</skill>",
	}, "\n")
}

// FormatInvocation appends user instructions outside the skill XML block.
func FormatInvocation(skill *Skill, instructions string) string {
	if skill == nil {
		return ""
	}
	block := FormatContent(skill)
	if instructions == "" {
		return block
	}
	return block + "\n\n" + instructions
}

// FormatAvailable renders model-invocable skills for a system prompt.
func FormatAvailable(available []Skill) string {
	visible := make([]Skill, 0, len(available))
	for _, skill := range available {
		if skill.Enabled && !skill.DisableModelInvocation {
			visible = append(visible, skill)
		}
	}
	if len(visible) == 0 {
		return ""
	}

	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		"Read the full skill file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, skill := range visible {
		lines = append(lines,
			"  <skill>",
			"    <name>"+escapeXML(skill.Name)+"</name>",
			"    <description>"+escapeXML(skill.Description)+"</description>",
			"    <location>"+escapeXML(skill.FilePath)+"</location>",
			"  </skill>",
		)
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

func escapeXML(value string) string {
	var out bytes.Buffer
	if err := xml.EscapeText(&out, []byte(value)); err != nil {
		return value
	}
	return out.String()
}
