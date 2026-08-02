package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"oops/internal/agent/runtime/skills"
	"oops/internal/logutil"

	"go.uber.org/zap"
)

// handleSkillsList handles GET /api/skills.
func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		writeJSON(w, []skills.Skill{})
		return
	}
	writeJSON(w, s.skillStore.List())
}

// handleSkillsUpdate handles PUT /api/skills/{name}.
func (s *Server) handleSkillsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		writeJSONError(w, "skill store not configured", http.StatusServiceUnavailable)
		return
	}

	name := r.PathValue("name")
	existing, ok := s.skillStore.Get(name)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}

	var updated skills.Skill
	if err := json.NewDecoder(r.Body).Decode(&updated); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}

	// 只允许更新前端可编辑字段。
	existing.Content = updated.Content
	existing.Description = updated.Description
	existing.Icon = updated.Icon
	existing.Label = updated.Label
	existing.Color = updated.Color
	existing.Enabled = updated.Enabled

	if existing.FilePath == "" {
		writeJSONError(w, "skill file path is missing", http.StatusConflict)
		return
	}
	if err := s.skillStore.Save(existing); err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) || errors.Is(err, skills.ErrSkillChanged) || errors.Is(err, skills.ErrSkillPathOutsideRoot) {
			writeJSONError(w, "skill changed during update", http.StatusConflict)
			return
		}
		logutil.Error("skill: write file", zap.String("path", existing.FilePath), zap.Error(err))
		writeJSONError(w, "failed to write skill file", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w)
}

// handleSkillsDelete handles DELETE /api/skills/{name}.
func (s *Server) handleSkillsDelete(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		writeJSONError(w, "skill store not configured", http.StatusServiceUnavailable)
		return
	}

	name := r.PathValue("name")
	existing, ok := s.skillStore.Get(name)
	if !ok {
		writeJSONError(w, "skill not found", http.StatusNotFound)
		return
	}

	// 保护内置 default skill 不可删除。
	if name == "default" {
		writeJSONError(w, "cannot delete the default skill", http.StatusBadRequest)
		return
	}

	// 不允许删除最后一个启用 Skill。
	enabled := s.skillStore.Enabled()
	if existing.Enabled && len(enabled) <= 1 {
		writeJSONError(w, "cannot delete the last enabled skill", http.StatusBadRequest)
		return
	}

	if existing.FilePath == "" {
		writeJSONError(w, "skill file path is missing", http.StatusConflict)
		return
	}
	if err := s.skillStore.Remove(name); err != nil {
		if errors.Is(err, skills.ErrSkillNotFound) || errors.Is(err, skills.ErrSkillChanged) || errors.Is(err, skills.ErrSkillPathOutsideRoot) {
			writeJSONError(w, "skill changed during delete", http.StatusConflict)
			return
		}
		logutil.Error("skill: delete file", zap.String("path", existing.FilePath), zap.Error(err))
		writeJSONError(w, "failed to delete skill file", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w)
}
