package api

import (
	"errors"
	"net/http"
	"strings"

	agentruntime "oops/internal/agent/runtime"
)

var errSkillStoreUnavailable = errors.New("skill store not configured")

type skillCommandError struct {
	status  int
	message string
}

func (e skillCommandError) Error() string {
	return e.message
}

func (s *Server) expandSkillCommand(text string) (string, error) {
	command, ok := parseSkillCommand(text)
	if !ok {
		return text, nil
	}
	if command.namespaced && command.name == "" {
		return "", skillCommandError{status: http.StatusBadRequest, message: "skill name is required"}
	}
	if s.skillStore == nil {
		if command.namespaced {
			return "", skillCommandError{status: http.StatusServiceUnavailable, message: errSkillStoreUnavailable.Error()}
		}
		return text, nil
	}
	skill, ok := s.skillStore.Get(command.name)
	if !ok || !skill.Enabled {
		if command.namespaced {
			return "", skillCommandError{status: http.StatusBadRequest, message: "skill not found or disabled"}
		}
		return text, nil
	}
	return agentruntime.FormatSkillInvocation(skill, command.instructions), nil
}

type parsedSkillCommand struct {
	namespaced   bool
	name         string
	instructions string
}

func parseSkillCommand(text string) (parsedSkillCommand, bool) {
	if strings.HasPrefix(text, "/skill:") {
		rest := strings.TrimPrefix(text, "/skill:")
		if rest == "" || startsWithSpace(rest) {
			return parsedSkillCommand{namespaced: true}, true
		}
		name, instructions := splitSkillCommandRest(rest)
		return parsedSkillCommand{namespaced: true, name: name, instructions: instructions}, true
	}
	if text == "/skill" || strings.HasPrefix(text, "/skill ") || !strings.HasPrefix(text, "/") {
		return parsedSkillCommand{}, false
	}
	rest := strings.TrimPrefix(text, "/")
	name, instructions := splitSkillCommandRest(rest)
	if name == "" || strings.Contains(name, ":") {
		return parsedSkillCommand{}, false
	}
	return parsedSkillCommand{name: name, instructions: instructions}, true
}

func startsWithSpace(value string) bool {
	if value == "" {
		return false
	}
	return strings.TrimLeft(value[:1], " \t\r\n") == ""
}

func splitSkillCommandRest(rest string) (string, string) {
	rest = strings.TrimLeft(rest, " \t\r\n")
	if rest == "" {
		return "", ""
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", ""
	}
	name := fields[0]
	if len(rest) == len(name) {
		return name, ""
	}
	return name, strings.TrimSpace(rest[len(name):])
}
