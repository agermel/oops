package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	agentruntime "oops/internal/agent/runtime"
	agentresources "oops/internal/agent/runtime/resources"
	"oops/internal/agent/runtime/session"
)

// sessionInfo 是 HTTP 会话列表和重命名响应的稳定 DTO。
// Session 的领域对象不会穿透到 HTTP 边界。
type sessionInfo struct {
	ID           string `json:"id"`
	ProjectID    string `json:"projectId,omitempty"`
	Title        string `json:"title,omitempty"`
	Summary      string `json:"summary,omitempty"`
	MessageCount int    `json:"messageCount"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

type runtimeBranchRequest struct {
	LeafID  string `json:"leafId"`
	Summary string `json:"summary,omitempty"`
}

type runtimeSessionUpdateRequest struct {
	Title string `json:"title"`
}

func (s *Server) ensureAgentRuntime() *agentruntime.Runtime {
	if s.agentRuntime != nil {
		return s.agentRuntime
	}
	if s.agentRepo == nil {
		s.agentRepo = session.NewRepository(nil)
	}
	s.agentRuntime = agentruntime.NewRuntime(agentruntime.RuntimeOptions{
		Repo:            s.agentRepo,
		PromptTemplates: s.promptTemplates,
	})
	return s.agentRuntime
}

func (s *Server) acquireRuntimeSessionLease(w http.ResponseWriter, sessionID string) (*agentruntime.SessionLease, bool) {
	if !validateRuntimeSessionID(w, sessionID) {
		return nil, false
	}
	lease, err := s.ensureAgentRuntime().AcquireSession(sessionID)
	if err == nil {
		return lease, true
	}
	if errors.Is(err, ErrSessionBusy) {
		writeJSONError(w, ErrSessionBusy.Error(), http.StatusConflict)
		return nil, false
	}
	if errors.Is(err, ErrRunManagerQuiescing) {
		writeJSONError(w, "server is shutting down", http.StatusServiceUnavailable)
		return nil, false
	}
	sanitizedError(w, "acquire runtime session lease", err, http.StatusInternalServerError)
	return nil, false
}

func validateRuntimeSessionID(w http.ResponseWriter, sessionID string) bool {
	if err := session.ValidateID(sessionID); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
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
	lease, ok := s.acquireRuntimeSessionLease(w, id)
	if !ok {
		return
	}
	defer lease.Release()
	harness, ok, err := s.runtimeAgentHarness(r.Context(), id, "")
	if err != nil {
		sanitizedError(w, "load session", err, http.StatusInternalServerError)
		return
	}
	if !ok {
		writeJSONError(w, "session not found", http.StatusNotFound)
		return
	}
	var snapshot agentruntime.SessionSnapshot
	var navErr error
	if req.Summary != "" {
		snapshot, navErr = harness.NavigateTreeWithSummarySnapshot(req.LeafID, req.Summary)
	} else {
		snapshot, navErr = harness.NavigateTreeSnapshot(req.LeafID)
	}
	if navErr != nil {
		if errors.Is(navErr, agentruntime.ErrAgentBusy) {
			writeJSONError(w, "session is busy", http.StatusConflict)
			return
		}
		writeJSONError(w, navErr.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, snapshot)
}

func (s *Server) runtimeSessionInfos(projectID string) ([]sessionInfo, error) {
	if s.agentRepo == nil {
		return nil, nil
	}
	infos, err := s.agentRepo.List()
	if err != nil {
		return nil, err
	}
	out := make([]sessionInfo, 0, len(infos))
	for _, info := range infos {
		if info.ProjectID != projectID {
			continue
		}
		out = append(out, runtimeSessionInfo(info))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out, nil
}

func runtimeSessionInfo(info session.Info) sessionInfo {
	return sessionInfo{
		ID:           info.ID,
		ProjectID:    info.ProjectID,
		Title:        info.Title,
		Summary:      info.Summary,
		MessageCount: info.Messages,
		CreatedAt:    unixMilli(info.CreatedAt),
		UpdatedAt:    unixMilli(info.UpdatedAt),
	}
}

func (s *Server) runtimeSessionInfoByID(sessionID string) (session.Info, bool, error) {
	if s.agentRepo == nil || sessionID == "" {
		return session.Info{}, false, nil
	}
	runtimeSession, ok := s.agentRepo.Get(sessionID)
	if !ok {
		var err error
		runtimeSession, err = s.agentRepo.Load(sessionID)
		if err != nil {
			if isRuntimeSessionUnavailable(err) {
				return session.Info{}, false, nil
			}
			return session.Info{}, false, err
		}
		if len(runtimeSession.Entries()) == 0 {
			_, _ = s.agentRepo.Delete(sessionID)
			return session.Info{}, false, nil
		}
	}
	return runtimeSession.Info(), true, nil
}

func (s *Server) renameRuntimeSession(sessionID, projectID, title string) (sessionInfo, bool, error) {
	if s.agentRepo == nil || sessionID == "" {
		return sessionInfo{}, false, nil
	}
	runtimeSession, ok := s.agentRepo.Get(sessionID)
	if !ok {
		var err error
		runtimeSession, err = s.agentRepo.Load(sessionID)
		if err != nil {
			if isRuntimeSessionUnavailable(err) {
				return sessionInfo{}, false, nil
			}
			return sessionInfo{}, false, err
		}
		if len(runtimeSession.Entries()) == 0 {
			_, _ = s.agentRepo.Delete(sessionID)
			return sessionInfo{}, false, nil
		}
	}
	info := runtimeSession.Info()
	if projectID != "" && info.ProjectID != projectID {
		return sessionInfo{}, false, nil
	}
	if _, err := s.agentRepo.AppendEntry(runtimeSession.ID(), session.Entry{Type: session.EntrySessionInfo, Title: title}); err != nil {
		return sessionInfo{}, false, err
	}
	return runtimeSessionInfo(runtimeSession.Info()), true, nil
}

func decodeRuntimeSessionTitle(r *http.Request) (string, bool) {
	var req runtimeSessionUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", false
	}
	title := strings.TrimSpace(req.Title)
	if title == "" || len([]rune(title)) > 120 {
		return "", false
	}
	return title, true
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (s *Server) runtimeSessionSnapshot(ctx context.Context, sessionID, projectID string) (agentruntime.SessionSnapshot, bool, error) {
	harness, ok, err := s.runtimeAgentHarness(ctx, sessionID, projectID)
	if err != nil || !ok {
		return agentruntime.SessionSnapshot{}, ok, err
	}
	return harness.Snapshot(), true, nil
}

func (s *Server) runtimeAgentHarness(ctx context.Context, sessionID, projectID string) (*agentruntime.AgentHarness, bool, error) {
	if s.agentRepo == nil || sessionID == "" {
		return nil, false, nil
	}
	session, ok := s.agentRepo.Get(sessionID)
	if !ok {
		var err error
		session, err = s.agentRepo.Load(sessionID)
		if err != nil {
			if isRuntimeSessionUnavailable(err) {
				return nil, false, nil
			}
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
		harness, err := s.newRunAgentHarness(ctx, req, "", s.enabledSkillSnapshot())
		if err != nil {
			return nil, false, err
		}
		return harness, true, nil
	}
	harness, err := s.ensureAgentRuntime().ResumeWithOptions(ctx, sessionID, agentruntime.ResumeSessionOptions{
		CWD:       info.CWD,
		Resources: &agentresources.Snapshot{},
	})
	if err != nil {
		return nil, false, err
	}
	return harness, true, nil
}

func isRuntimeSessionUnavailable(err error) bool {
	return errors.Is(err, fs.ErrNotExist) ||
		errors.Is(err, session.ErrSessionCreating) ||
		errors.Is(err, session.ErrSessionDeleting)
}
