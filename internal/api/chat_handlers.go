package api

import (
	"net/http"
)

// handleSessions handles GET /api/sessions — lists global or project sessions.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	infos, err := s.runtimeSessionInfos(projectID)
	if err != nil {
		sanitizedError(w, "list runtime sessions", err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, infos)
}

// handleSessionGet handles GET /api/sessions/{id}.
func (s *Server) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validateRuntimeSessionID(w, id) {
		return
	}
	snapshot, ok, err := s.runtimeSessionSnapshot(r.Context(), id, "")
	if err != nil {
		sanitizedError(w, "get runtime session", err, http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, snapshot)
}

// handleSessionUpdate handles PATCH /api/sessions/{id}.
func (s *Server) handleSessionUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	title, ok := decodeRuntimeSessionTitle(r)
	if !ok {
		writeJSONError(w, "invalid session title", http.StatusBadRequest)
		return
	}
	lease, ok := s.beginSessionMutation(w, id)
	if !ok {
		return
	}
	defer lease.release()
	info, found, err := s.renameRuntimeSession(id, "", title)
	if err != nil {
		sanitizedError(w, "update runtime session", err, http.StatusInternalServerError)
		return
	}
	if found {
		writeJSON(w, info)
		return
	}
	writeJSONError(w, "session not found", http.StatusNotFound)
}

// handleSessionDelete handles DELETE /api/sessions/{id}.
func (s *Server) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.agentRepo == nil {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	lease, ok := s.beginSessionMutation(w, id)
	if !ok {
		return
	}
	defer lease.release()
	deleted, err := s.agentRepo.Delete(id)
	if err != nil {
		sanitizedError(w, "delete runtime session", err, http.StatusInternalServerError)
		return
	}
	if !deleted {
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
	writeJSON(w, infos)
}

// handleProjectSessionGet handles GET /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionGet(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	if !validateRuntimeSessionID(w, id) {
		return
	}
	snapshot, ok, err := s.runtimeSessionSnapshot(r.Context(), id, pid)
	if err != nil {
		sanitizedError(w, "get project runtime session", err, http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSON(w, snapshot)
}

// handleProjectSessionUpdate handles PATCH /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionUpdate(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	title, ok := decodeRuntimeSessionTitle(r)
	if !ok {
		writeJSONError(w, "invalid session title", http.StatusBadRequest)
		return
	}
	lease, ok := s.beginSessionMutation(w, id)
	if !ok {
		return
	}
	defer lease.release()
	info, found, err := s.renameRuntimeSession(id, pid, title)
	if err != nil {
		sanitizedError(w, "update project runtime session", err, http.StatusInternalServerError)
		return
	}
	if found {
		writeJSON(w, info)
		return
	}
	writeJSONError(w, "session not found", http.StatusNotFound)
}

// handleProjectSessionDelete handles DELETE /api/projects/{pid}/sessions/{id}.
func (s *Server) handleProjectSessionDelete(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	id := r.PathValue("id")
	lease, ok := s.beginSessionMutation(w, id)
	if !ok {
		return
	}
	defer lease.release()
	info, ok, err := s.runtimeSessionInfoByID(id)
	if err != nil {
		sanitizedError(w, "delete project runtime session", err, http.StatusInternalServerError)
		return
	}
	if !ok || info.ProjectID != pid || s.agentRepo == nil {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	deleted, err := s.agentRepo.Delete(id)
	if err != nil {
		sanitizedError(w, "delete project runtime session", err, http.StatusInternalServerError)
		return
	}
	if !deleted {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	writeJSONOK(w)
}
