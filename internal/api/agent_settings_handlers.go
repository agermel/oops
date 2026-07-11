package api

import (
	"encoding/json"
	"net/http"

	runtimestore "oops/internal/store/runtime"
)

type agentSettingsResponse struct {
	MaxTurns int `json:"maxTurns"`
}

type agentSettingsUpdateRequest struct {
	MaxTurns int `json:"maxTurns"`
}

func (s *Server) handleAgentSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.runtimeStore == nil {
		writeJSONError(w, "runtime store not available", http.StatusInternalServerError)
		return
	}
	settings, err := s.runtimeStore.GetAgentSettings(r.Context())
	if err != nil {
		writeJSONError(w, "read agent settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, agentSettingsResponse{MaxTurns: settings.MaxTurns})
}

func (s *Server) handleAgentSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.runtimeStore == nil {
		writeJSONError(w, "runtime store not available", http.StatusInternalServerError)
		return
	}

	var req agentSettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.MaxTurns < runtimestore.AgentMaxTurnsMin || req.MaxTurns > runtimestore.AgentMaxTurnsMax {
		writeJSONError(w, "maxTurns must be between 1 and 100", http.StatusBadRequest)
		return
	}
	settings := runtimestore.AgentSettingsRecord{MaxTurns: req.MaxTurns}
	if err := s.runtimeStore.UpdateAgentSettings(r.Context(), settings); err != nil {
		writeJSONError(w, "save agent settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, agentSettingsResponse(req))
}
