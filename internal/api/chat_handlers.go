package api

import (
	"net/http"

	oldsession "oops/internal/llm/session"
)

// handleSessions handles GET /api/sessions — lists global or project sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	infos, err := s.runtimeSessionInfos(projectID)
	if err != nil {
		sanitizedError(w, "list runtime sessions", err, http.StatusInternalServerError)
		return
	}
	if s.agentRepo != nil {
		writeJSON(w, infos)
		return
	}
	writeJSON(w, s.sessionStore.List(projectID))
}

// handleSessionGet handles GET /api/sessions/{id}.
func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.agentRepo != nil {
		snapshot, ok, err := s.runtimeSessionSnapshot(r.Context(), id, "")
		if err != nil {
			sanitizedError(w, "get runtime session", err, http.StatusInternalServerError)
			return
		}
		if ok {
			writeJSON(w, snapshot)
			return
		}
	}
	sess, ok := s.sessionStore.Get(id)
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("include_messages") == "true" {
		writeJSON(w, sess.ToDetail())
	} else {
		writeJSON(w, oldsession.SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
}

// handleSessionDelete handles DELETE /api/sessions/{id}.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.agentRepo != nil {
		deleted, err := s.agentRepo.Delete(id)
		if err != nil {
			sanitizedError(w, "delete runtime session", err, http.StatusInternalServerError)
			return
		}
		if deleted {
			writeJSONOK(w)
			return
		}
	}
	if !s.sessionStore.Delete(id) {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSONOK(w)
}

// handleProjectSessions handles GET /api/projects/{pid}/sessions.
func (s *Server) handleProjectSessions(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	infos, err := s.runtimeSessionInfos(pid)
	if err != nil {
		sanitizedError(w, "list project runtime sessions", err, http.StatusInternalServerError)
		return
	}
	if s.agentRepo != nil {
		writeJSON(w, infos)
		return
	}
	writeJSON(w, s.sessionStore.List(pid))
}

// handleProjectSessionGet handles GET /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionGet(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	if s.agentRepo != nil {
		snapshot, ok, err := s.runtimeSessionSnapshot(r.Context(), id, pid)
		if err != nil {
			sanitizedError(w, "get project runtime session", err, http.StatusInternalServerError)
			return
		}
		if ok {
			writeJSON(w, snapshot)
			return
		}
	}
	sess, ok := s.sessionStore.Get(id)
	if !ok || sess.ProjectID != pid {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	if r.URL.Query().Get("include_messages") == "true" {
		writeJSON(w, sess.ToDetail())
	} else {
		writeJSON(w, oldsession.SessionInfo{
			ID:           sess.ID,
			ProjectID:    sess.ProjectID,
			MessageCount: len(sess.Messages),
			CreatedAt:    sess.CreatedAt.UnixMilli(),
			UpdatedAt:    sess.UpdatedAt.UnixMilli(),
		})
	}
}

// handleProjectSessionDelete handles DELETE /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionDelete(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	if s.agentRepo != nil {
		info, ok, err := s.runtimeSessionInfoByID(id)
		if err != nil {
			sanitizedError(w, "delete project runtime session", err, http.StatusInternalServerError)
			return
		}
		if ok && info.ProjectID == pid {
			deleted, err := s.agentRepo.Delete(id)
			if err != nil {
				sanitizedError(w, "delete project runtime session", err, http.StatusInternalServerError)
				return
			}
			if deleted {
				writeJSONOK(w)
				return
			}
		}
	}
	sess, ok := s.sessionStore.Get(id)
	if !ok || sess.ProjectID != pid {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	s.sessionStore.Delete(id)
	writeJSONOK(w)
}
