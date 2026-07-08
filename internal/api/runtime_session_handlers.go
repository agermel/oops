package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	coreagent "oops/internal/llm/core/agent"
	"oops/internal/llm/runtime/harness"
	runtimesession "oops/internal/llm/runtime/session"
	oldsession "oops/internal/llm/session"
)

type runtimeBranchRequest struct {
	LeafID  string `json:"leafId"`
	Summary string `json:"summary,omitempty"`
}

func (s *Server) handleSessionBranch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.agentRepo == nil {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	var req runtimeBranchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, "invalid request", http.StatusBadRequest)
		return
	}
	agentSession, ok, err := s.runtimeAgentSession(r.Context(), id, "")
	if err != nil {
		sanitizedError(w, "load runtime session", err, http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	var navErr error
	if req.Summary != "" {
		navErr = agentSession.NavigateTreeWithSummary(req.LeafID, req.Summary)
	} else {
		navErr = agentSession.NavigateTree(req.LeafID)
	}
	if navErr != nil {
		if errors.Is(navErr, coreagent.ErrAgentBusy) {
			writeJSONError(w, "session is busy", http.StatusConflict)
			return
		}
		writeJSONError(w, navErr.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, agentSession.Snapshot())
}

func (s *Server) runtimeSessionInfos(projectID string) ([]oldsession.SessionInfo, error) {
	if s.agentRepo == nil {
		return nil, nil
	}
	infos, err := s.agentRepo.List()
	if err != nil {
		return nil, err
	}
	out := make([]oldsession.SessionInfo, 0, len(infos))
	for _, info := range infos {
		if projectID != "*" && info.ProjectID != projectID {
			continue
		}
		out = append(out, runtimeSessionInfo(info))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

func runtimeSessionInfo(info runtimesession.Info) oldsession.SessionInfo {
	return oldsession.SessionInfo{
		ID:           info.ID,
		ProjectID:    info.ProjectID,
		MessageCount: info.Messages,
		CreatedAt:    unixMilli(info.CreatedAt),
		UpdatedAt:    unixMilli(info.UpdatedAt),
	}
}

func (s *Server) runtimeSessionInfoByID(sessionID string) (runtimesession.Info, bool, error) {
	if s.agentRepo == nil || sessionID == "" {
		return runtimesession.Info{}, false, nil
	}
	runtimeSession, ok := s.agentRepo.Get(sessionID)
	if !ok {
		var err error
		runtimeSession, err = s.agentRepo.Load(sessionID)
		if err != nil {
			return runtimesession.Info{}, false, err
		}
		if len(runtimeSession.Entries()) == 0 {
			_, _ = s.agentRepo.Delete(sessionID)
			return runtimesession.Info{}, false, nil
		}
	}
	return runtimeSession.Info(), true, nil
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (s *Server) runtimeSessionSnapshot(ctx context.Context, sessionID, projectID string) (harness.SessionSnapshot, bool, error) {
	agentSession, ok, err := s.runtimeAgentSession(ctx, sessionID, projectID)
	if err != nil || !ok {
		return harness.SessionSnapshot{}, ok, err
	}
	return agentSession.Snapshot(), true, nil
}

func (s *Server) runtimeAgentSession(ctx context.Context, sessionID, projectID string) (*harness.AgentSession, bool, error) {
	if s.agentRepo == nil || sessionID == "" {
		return nil, false, nil
	}
	session, ok := s.agentRepo.Get(sessionID)
	if !ok {
		var err error
		session, err = s.agentRepo.Load(sessionID)
		if err != nil {
			return nil, false, err
		}
		if len(session.Entries()) == 0 {
			_, _ = s.agentRepo.Delete(sessionID)
			return nil, false, nil
		}
	}
	info := session.Info()
	if projectID != "" && info.ProjectID != projectID {
		return nil, false, nil
	}
	effectiveProjectID := projectID
	if effectiveProjectID == "" {
		effectiveProjectID = info.ProjectID
	}
	if s.llmClient != nil {
		req := runCreateRequest{SessionID: sessionID, ProjectID: effectiveProjectID}
		agentSession, err := s.newRunAgentSession(ctx, req)
		if err != nil {
			return nil, false, err
		}
		return agentSession, true, nil
	}
	runtime := harness.NewRuntime(harness.RuntimeOptions{
		Repo:   s.agentRepo,
		Loader: harness.StaticResourceLoader{Snapshot: harness.ResourceSnapshot{}},
		CWD:    info.CWD,
	})
	agentSession, err := runtime.Resume(ctx, sessionID)
	if err != nil {
		return nil, false, err
	}
	return agentSession, true, nil
}
