package runtime

import (
	"errors"
	"strings"

	agentskills "oops/internal/agent/runtime/skills"
)

var (
	ErrPromptSkillNameRequired = errors.New("skill name is required")
	ErrPromptSkillUnavailable  = errors.New("skill not found or disabled")
)

// ResolvePromptCommand expands a namespaced skill, prompt template, or enabled skill shorthand.
func ResolvePromptCommand(input string, templates []PromptTemplate, available []agentskills.Skill) (string, error) {
	command, namespaced := parseNamespacedSkillCommand(input)
	if namespaced {
		if command.name == "" {
			return "", ErrPromptSkillNameRequired
		}
		skill, ok := findEnabledSkill(available, command.name)
		if !ok {
			return "", ErrPromptSkillUnavailable
		}
		return agentskills.FormatInvocation(skill, command.instructions), nil
	}

	if expanded, ok := ResolvePromptTemplateCommand(input, templates); ok {
		return expanded, nil
	}

	command, ok := parseSkillShorthand(input)
	if !ok {
		return input, nil
	}
	if skill, found := findEnabledSkill(available, command.name); found {
		return agentskills.FormatInvocation(skill, command.instructions), nil
	}
	return input, nil
}

type promptSkillCommand struct {
	name         string
	instructions string
}

func parseNamespacedSkillCommand(input string) (promptSkillCommand, bool) {
	if !strings.HasPrefix(input, "/skill:") {
		return promptSkillCommand{}, false
	}
	rest := strings.TrimPrefix(input, "/skill:")
	if rest == "" || startsWithWhitespace(rest) {
		return promptSkillCommand{}, true
	}
	return splitPromptSkillCommand(rest), true
}

func parseSkillShorthand(input string) (promptSkillCommand, bool) {
	if input == "/skill" || strings.HasPrefix(input, "/skill ") || !strings.HasPrefix(input, "/") {
		return promptSkillCommand{}, false
	}
	command := splitPromptSkillCommand(strings.TrimPrefix(input, "/"))
	if command.name == "" || strings.Contains(command.name, ":") {
		return promptSkillCommand{}, false
	}
	return command, true
}

func splitPromptSkillCommand(input string) promptSkillCommand {
	input = strings.TrimLeft(input, " \t\r\n")
	if input == "" {
		return promptSkillCommand{}
	}
	nameEnd := strings.IndexAny(input, " \t\r\n")
	if nameEnd < 0 {
		return promptSkillCommand{name: input}
	}
	return promptSkillCommand{
		name:         input[:nameEnd],
		instructions: strings.TrimSpace(input[nameEnd:]),
	}
}

func startsWithWhitespace(value string) bool {
	return value != "" && strings.TrimLeft(value[:1], " \t\r\n") == ""
}

func findEnabledSkill(available []agentskills.Skill, name string) (*agentskills.Skill, bool) {
	for i := range available {
		if available[i].Name == name && available[i].Enabled {
			return &available[i], true
		}
	}
	return nil, false
}
