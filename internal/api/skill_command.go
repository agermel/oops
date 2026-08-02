package api

import (
	"errors"
	"net/http"

	agentruntime "oops/internal/agent/runtime"
	"oops/internal/agent/runtime/skills"
)

var errSkillStoreUnavailable = errors.New("skill store not configured")

type skillCommandError struct {
	status  int
	message string
}

func (e skillCommandError) Error() string {
	return e.message
}

func (s *Server) validateRunPrompt(text string, available []skills.Skill) error {
	_, err := agentruntime.ResolvePromptCommand(text, s.promptTemplates, available)
	if err == nil {
		return nil
	}
	if errors.Is(err, agentruntime.ErrPromptSkillUnavailable) && s.skillStore == nil {
		return skillCommandError{status: http.StatusServiceUnavailable, message: errSkillStoreUnavailable.Error()}
	}
	if errors.Is(err, agentruntime.ErrPromptSkillNameRequired) || errors.Is(err, agentruntime.ErrPromptSkillUnavailable) {
		return skillCommandError{status: http.StatusBadRequest, message: err.Error()}
	}
	return err
}

func (s *Server) enabledSkillSnapshot() []skills.Skill {
	if s.skillStore == nil {
		return nil
	}
	enabled := s.skillStore.Enabled()
	snapshot := make([]skills.Skill, 0, len(enabled))
	for _, skill := range enabled {
		if skill != nil {
			snapshot = append(snapshot, *skill)
		}
	}
	return snapshot
}
