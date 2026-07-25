package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "oops/internal/agent/ai"
	agentruntime "oops/internal/agent/runtime"
)

func TestRuntimeSessionHandlersListDetailAndDelete(t *testing.T) {
	storage, err := agentruntime.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := agentruntime.NewRepository(storage)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-1", "proj-1", "hello")
	createRuntimeSession(t, repo, "rt-2", "proj-2", "other")

	listResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/projects/proj-1/sessions", "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("project list status = %d, want %d, body = %s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var infos []sessionInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode project list: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != sess.ID() || infos[0].ProjectID != "proj-1" || infos[0].MessageCount != 1 || infos[0].Summary != "hello" {
		t.Fatalf("project infos = %+v", infos)
	}
	topLevelListResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/sessions", "")
	if topLevelListResp.Code != http.StatusNotFound {
		t.Fatalf("top-level list status = %d, want %d, body = %s", topLevelListResp.Code, http.StatusNotFound, topLevelListResp.Body.String())
	}

	detailResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/sessions/rt-1", "")
	if detailResp.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d, body = %s", detailResp.Code, http.StatusOK, detailResp.Body.String())
	}
	var snapshot agentruntime.SessionSnapshot
	if err := json.NewDecoder(detailResp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if snapshot.SessionID != "rt-1" || len(snapshot.Messages) != 1 || runtimeMessageText(t, snapshot.Messages[0]) != "hello" {
		t.Fatalf("snapshot = %+v", snapshot)
	}

	wrongProjectDelete := serveAuthed(t, server, jwtToken, http.MethodDelete, "/api/projects/proj-1/sessions/rt-2", "")
	if wrongProjectDelete.Code != http.StatusNotFound {
		t.Fatalf("wrong project delete status = %d, want %d, body = %s", wrongProjectDelete.Code, http.StatusNotFound, wrongProjectDelete.Body.String())
	}
	projectDeleteResp := serveAuthed(t, server, jwtToken, http.MethodDelete, "/api/projects/proj-2/sessions/rt-2", "")
	if projectDeleteResp.Code != http.StatusOK {
		t.Fatalf("project delete status = %d, want %d, body = %s", projectDeleteResp.Code, http.StatusOK, projectDeleteResp.Body.String())
	}

	deleteResp := serveAuthed(t, server, jwtToken, http.MethodDelete, "/api/sessions/rt-1", "")
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d, body = %s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}
	missingResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/sessions/rt-1", "")
	if missingResp.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want %d, body = %s", missingResp.Code, http.StatusNotFound, missingResp.Body.String())
	}
}

func TestProjectSessionListSkipsInvalidFile(t *testing.T) {
	dir := t.TempDir()
	storage, err := agentruntime.NewFileStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	valid := agentruntime.New("valid")
	entry, err := valid.AppendSessionInfoWithProject("/tmp/project", "work", "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.Append(valid.ID(), entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invalid.jsonl"), []byte(`{"type":"session_info","version":1}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	server, jwtToken := newRuntimeSessionTestServer(t, agentruntime.NewRepository(storage))
	listResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/projects/proj-1/sessions", "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var infos []sessionInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != "valid" {
		t.Fatalf("infos = %+v, want only valid session", infos)
	}

	invalidResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/sessions/invalid", "")
	if invalidResp.Code != http.StatusInternalServerError {
		t.Fatalf("invalid session status = %d, want %d, body = %s", invalidResp.Code, http.StatusInternalServerError, invalidResp.Body.String())
	}
}

func TestRuntimeSessionHandlersRename(t *testing.T) {
	storage, err := agentruntime.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := agentruntime.NewRepository(storage)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-title", "proj-1", "hello")
	createRuntimeSession(t, repo, "rt-other-title", "proj-2", "other")

	wrongProjectResp := serveAuthed(t, server, jwtToken, http.MethodPatch, "/api/projects/proj-2/sessions/rt-title", `{"title":"wrong"}`)
	if wrongProjectResp.Code != http.StatusNotFound {
		t.Fatalf("wrong project rename status = %d, want %d, body = %s", wrongProjectResp.Code, http.StatusNotFound, wrongProjectResp.Body.String())
	}

	resp := serveAuthed(t, server, jwtToken, http.MethodPatch, "/api/projects/proj-1/sessions/rt-title", `{"title":" Deploy checklist "}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var info sessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode rename: %v", err)
	}
	if info.ID != sess.ID() || info.Title != "Deploy checklist" || info.Summary != "hello" {
		t.Fatalf("rename info = %+v", info)
	}

	listResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/projects/proj-1/sessions", "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var infos []sessionInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(infos) != 1 || infos[0].Title != "Deploy checklist" {
		t.Fatalf("infos = %+v", infos)
	}
}

func TestRuntimeSessionMutationsRejectBusySession(t *testing.T) {
	repo := agentruntime.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	createRuntimeSession(t, repo, "rt-busy", "proj-1", "hello")
	lease, err := server.ensureAgentRuntime().AcquireSession("rt-busy")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "branch", method: http.MethodPost, path: "/api/sessions/rt-busy/branch", body: `{"leafId":"entry"}`},
		{name: "rename", method: http.MethodPatch, path: "/api/sessions/rt-busy", body: `{"title":"busy"}`},
		{name: "delete", method: http.MethodDelete, path: "/api/sessions/rt-busy"},
		{name: "project rename", method: http.MethodPatch, path: "/api/projects/proj-1/sessions/rt-busy", body: `{"title":"busy"}`},
		{name: "project delete", method: http.MethodDelete, path: "/api/projects/proj-1/sessions/rt-busy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := serveAuthed(t, server, jwtToken, tt.method, tt.path, tt.body)
			if resp.Code != http.StatusConflict {
				t.Fatalf("status = %d, want %d, body = %s", resp.Code, http.StatusConflict, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "session is busy") {
				t.Fatalf("body = %s", resp.Body.String())
			}
		})
	}
}

func TestRuntimeSessionHandlersRejectInvalidSessionID(t *testing.T) {
	repo := agentruntime.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "get", method: http.MethodGet, path: "/api/sessions/bad%20id"},
		{name: "uppercase alias", method: http.MethodGet, path: "/api/sessions/RT-BUSY"},
		{name: "branch", method: http.MethodPost, path: "/api/sessions/bad%20id/branch", body: `{"leafId":"entry"}`},
		{name: "rename", method: http.MethodPatch, path: "/api/sessions/bad%20id", body: `{"title":"title"}`},
		{name: "delete", method: http.MethodDelete, path: "/api/sessions/bad%20id"},
		{name: "project get", method: http.MethodGet, path: "/api/projects/project-1/sessions/bad%20id"},
		{name: "project rename", method: http.MethodPatch, path: "/api/projects/project-1/sessions/bad%20id", body: `{"title":"title"}`},
		{name: "project delete", method: http.MethodDelete, path: "/api/projects/project-1/sessions/bad%20id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := serveAuthed(t, server, jwtToken, tt.method, tt.path, tt.body)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body = %s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), "invalid session id") {
				t.Fatalf("body = %s", resp.Body.String())
			}
		})
	}
}

func TestRuntimeSessionBranchNavigatesLeaf(t *testing.T) {
	repo := agentruntime.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-branch", "proj-1", "root")
	root := sess.LeafID()
	left := appendRuntimeMessage(t, repo, sess, "left")
	if err := sess.MoveTo(root); err != nil {
		t.Fatal(err)
	}
	appendRuntimeMessage(t, repo, sess, "right")

	resp := serveAuthed(t, server, jwtToken, http.MethodPost, "/api/sessions/rt-branch/branch", `{"leafId":"`+left.ID+`"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("branch status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var snapshot agentruntime.SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode branch: %v", err)
	}
	if snapshot.LeafID != root {
		t.Fatalf("leaf = %q, want %q", snapshot.LeafID, root)
	}
	if snapshot.EditorText != "left" {
		t.Fatalf("editor text = %q, want left", snapshot.EditorText)
	}
	if len(snapshot.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(snapshot.Messages))
	}
	if runtimeMessageText(t, snapshot.Messages[0]) != "root" {
		t.Fatalf("message path = %q", runtimeMessageText(t, snapshot.Messages[0]))
	}
}

func TestRuntimeSessionBranchUserTargetReturnsEditorText(t *testing.T) {
	repo := agentruntime.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-edit-user", "proj-1", "root")
	root := sess.LeafID()
	user := appendRuntimeMessage(t, repo, sess, "edit this")

	resp := serveAuthed(t, server, jwtToken, http.MethodPost, "/api/sessions/rt-edit-user/branch", `{"leafId":"`+user.ID+`"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("branch status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var snapshot agentruntime.SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode branch: %v", err)
	}
	if snapshot.LeafID != root || snapshot.EditorText != "edit this" {
		t.Fatalf("snapshot leaf/editor = %q/%q, want %q/edit this", snapshot.LeafID, snapshot.EditorText, root)
	}
}

func TestRuntimeSessionBranchCanAppendSummary(t *testing.T) {
	repo := agentruntime.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-summary", "proj-1", "root")
	root := sess.LeafID()
	left := appendRuntimeAssistantToolCall(t, repo, sess, "call_read", "read", `{"path":"left.md"}`)
	if err := sess.MoveTo(root); err != nil {
		t.Fatal(err)
	}
	right := appendRuntimeMessage(t, repo, sess, "right")
	if err := sess.MoveTo(left.ID); err != nil {
		t.Fatal(err)
	}

	resp := serveAuthed(t, server, jwtToken, http.MethodPost, "/api/sessions/rt-summary/branch", `{"leafId":"`+right.ID+`","summary":"left branch read left.md"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("branch status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var snapshot agentruntime.SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode branch: %v", err)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(snapshot.Messages))
	}
	if snapshot.EditorText != "right" {
		t.Fatalf("editor text = %q, want right", snapshot.EditorText)
	}
	if runtimeMessageText(t, snapshot.Messages[1]) != "Branch summary:\n\nleft branch read left.md" {
		t.Fatalf("summary message = %q", runtimeMessageText(t, snapshot.Messages[1]))
	}
	last := snapshot.Entries[len(snapshot.Entries)-1]
	if last.Type != agentruntime.EntryBranchSummary || last.ParentID != root {
		t.Fatalf("last entry = %#v", last)
	}
}

func TestRuntimeSessionBranchSummaryUsesResolvedToolCallLeaf(t *testing.T) {
	repo := agentruntime.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-summary-tool", "proj-1", "root")
	assistant := appendRuntimeAssistantToolCall(t, repo, sess, "call_read", "read", `{"path":"left.md"}`)
	result := appendRuntimeToolResult(t, repo, sess, "call_read", "read", "file")
	appendRuntimeMessage(t, repo, sess, "after")

	resp := serveAuthed(t, server, jwtToken, http.MethodPost, "/api/sessions/rt-summary-tool/branch", `{"leafId":"`+assistant.ID+`","summary":"tool branch"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("branch status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var snapshot agentruntime.SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode branch: %v", err)
	}
	last := snapshot.Entries[len(snapshot.Entries)-1]
	if last.Type != agentruntime.EntryBranchSummary || last.ParentID != result.ID {
		t.Fatalf("last entry = %#v, want branch summary under result %q", last, result.ID)
	}
}

func newRuntimeSessionTestServer(t *testing.T, repo *agentruntime.Repository) (*Server, string) {
	t.Helper()
	userStore, tokenService, jwtToken := testAuthSetup(t)
	return &Server{
		agentRepo:    repo,
		UserStore:    userStore,
		TokenService: tokenService,
		runManager:   newRunManager(),
	}, jwtToken
}

func createRuntimeSession(t *testing.T, repo *agentruntime.Repository, id, projectID, firstMessage string) *agentruntime.Session {
	t.Helper()
	sess := repo.Create(id)
	info, err := sess.AppendSessionInfoWithProject("/tmp/project", "work", projectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(sess.ID(), info); err != nil {
		t.Fatal(err)
	}
	appendRuntimeMessage(t, repo, sess, firstMessage)
	return sess
}

func appendRuntimeMessage(t *testing.T, repo *agentruntime.Repository, sess *agentruntime.Session, text string) agentruntime.Entry {
	t.Helper()
	entry, err := sess.AppendMessage(protocol.UserMessage{
		Content:   protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(sess.ID(), entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func appendRuntimeAssistantToolCall(t *testing.T, repo *agentruntime.Repository, sess *agentruntime.Session, callID, name, args string) agentruntime.Entry {
	t.Helper()
	entry, err := sess.AppendMessage(protocol.AssistantMessage{
		Content: protocol.ContentList{
			protocol.NewToolCallContent(callID, name, json.RawMessage(args)),
		},
		StopReason: protocol.StopReasonToolUse,
		Timestamp:  time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(sess.ID(), entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func appendRuntimeToolResult(t *testing.T, repo *agentruntime.Repository, sess *agentruntime.Session, callID, name, text string) agentruntime.Entry {
	t.Helper()
	entry, err := sess.AppendMessage(protocol.ToolResultMessage{
		ToolCallID: callID,
		ToolName:   name,
		Content:    protocol.ContentList{protocol.NewTextContent(text)},
		Timestamp:  time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveEntry(sess.ID(), entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func serveAuthed(t *testing.T, server *Server, jwtToken, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	resp := httptest.NewRecorder()
	server.Routes().ServeHTTP(resp, req)
	return resp
}

func runtimeMessageText(t *testing.T, message protocol.AgentMessage) string {
	t.Helper()
	var content protocol.ContentList
	switch value := message.(type) {
	case protocol.UserMessage:
		content = value.Content
	case protocol.AssistantMessage:
		content = value.Content
	case protocol.ToolResultMessage:
		content = value.Content
	default:
		t.Fatalf("unexpected message type %T", message)
	}
	for _, item := range content {
		if text, ok := item.(protocol.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
