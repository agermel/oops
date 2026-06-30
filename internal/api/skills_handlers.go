package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"oops/internal/llm"
	"oops/internal/logutil"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// handleSkillsList handles GET /api/skills.
func (s *Server) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		writeJSON(w, []llm.Skill{})
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

	var updated llm.Skill
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

	// 写入对应的 .md 文件。
	path := filepath.Join("config/skills", name+".md")
	if err := writeSkillFile(path, existing); err != nil {
		logutil.Error("skill: write file", zap.String("path", path), zap.Error(err))
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
	_, ok := s.skillStore.Get(name)
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
	if len(enabled) <= 1 {
		writeJSONError(w, "cannot delete the last enabled skill", http.StatusBadRequest)
		return
	}

	path := filepath.Join("config/skills", name+".md")
	if err := os.Remove(path); err != nil {
		logutil.Error("skill: delete file", zap.String("path", path), zap.Error(err))
		writeJSONError(w, "failed to delete skill file", http.StatusInternalServerError)
		return
	}

	writeJSONOK(w)
}

// writeSkillFile 将 Skill 序列化为 Markdown + YAML frontmatter 写入文件。
func writeSkillFile(path string, skill *llm.Skill) error {
	fm, err := yaml.Marshal(skill)
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	b.WriteString(skill.Content)
	b.WriteString("\n")

	return os.WriteFile(path, []byte(b.String()), 0644)
}
