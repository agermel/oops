package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"oops/internal/llm/ai/protocol"
	"oops/internal/llm/runtime/harness"
	runtimesession "oops/internal/llm/runtime/session"
)

func TestRuntimeSessionHandlersListDetailAndDelete(t *testing.T) {
	storage, err := runtimesession.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := runtimesession.NewRepository(storage)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-1", "proj-1", "hello")
	createRuntimeSession(t, repo, "rt-2", "proj-2", "other")

	listResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/sessions?project_id=proj-1", "")
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var infos []sessionInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != sess.ID() || infos[0].ProjectID != "proj-1" || infos[0].MessageCount != 1 || infos[0].Summary != "hello" {
		t.Fatalf("infos = %+v", infos)
	}

	projectListResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/projects/proj-1/sessions", "")
	if projectListResp.Code != http.StatusOK {
		t.Fatalf("project list status = %d, want %d, body = %s", projectListResp.Code, http.StatusOK, projectListResp.Body.String())
	}
	infos = nil
	if err := json.NewDecoder(projectListResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode project list: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != sess.ID() || infos[0].ProjectID != "proj-1" || infos[0].MessageCount != 1 || infos[0].Summary != "hello" {
		t.Fatalf("project infos = %+v", infos)
	}

	detailResp := serveAuthed(t, server, jwtToken, http.MethodGet, "/api/sessions/rt-1?include_messages=true", "")
	if detailResp.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d, body = %s", detailResp.Code, http.StatusOK, detailResp.Body.String())
	}
	var snapshot harness.SessionSnapshot
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

func TestRuntimeSessionHandlersRename(t *testing.T) {
	storage, err := runtimesession.NewFileStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := runtimesession.NewRepository(storage)
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

func TestRuntimeSessionBranchNavigatesLeaf(t *testing.T) {
	repo := runtimesession.NewRepository(nil)
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
	var snapshot harness.SessionSnapshot
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
	repo := runtimesession.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-edit-user", "proj-1", "root")
	root := sess.LeafID()
	user := appendRuntimeMessage(t, repo, sess, "edit this")

	resp := serveAuthed(t, server, jwtToken, http.MethodPost, "/api/sessions/rt-edit-user/branch", `{"leafId":"`+user.ID+`"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("branch status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var snapshot harness.SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode branch: %v", err)
	}
	if snapshot.LeafID != root || snapshot.EditorText != "edit this" {
		t.Fatalf("snapshot leaf/editor = %q/%q, want %q/edit this", snapshot.LeafID, snapshot.EditorText, root)
	}
}

func TestRuntimeSessionBranchCanAppendSummary(t *testing.T) {
	repo := runtimesession.NewRepository(nil)
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
	var snapshot harness.SessionSnapshot
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
	if last.Type != runtimesession.EntryBranchSummary || last.ParentID != root {
		t.Fatalf("last entry = %#v", last)
	}
}

func TestRuntimeSessionBranchSummaryUsesResolvedToolCallLeaf(t *testing.T) {
	repo := runtimesession.NewRepository(nil)
	server, jwtToken := newRuntimeSessionTestServer(t, repo)
	sess := createRuntimeSession(t, repo, "rt-summary-tool", "proj-1", "root")
	assistant := appendRuntimeAssistantToolCall(t, repo, sess, "call_read", "read", `{"path":"left.md"}`)
	result := appendRuntimeToolResult(t, repo, sess, "call_read", "read", "file")
	appendRuntimeMessage(t, repo, sess, "after")

	resp := serveAuthed(t, server, jwtToken, http.MethodPost, "/api/sessions/rt-summary-tool/branch", `{"leafId":"`+assistant.ID+`","summary":"tool branch"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("branch status = %d, want %d, body = %s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var snapshot harness.SessionSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode branch: %v", err)
	}
	last := snapshot.Entries[len(snapshot.Entries)-1]
	if last.Type != runtimesession.EntryBranchSummary || last.ParentID != result.ID {
		t.Fatalf("last entry = %#v, want branch summary under result %q", last, result.ID)
	}
}

func newRuntimeSessionTestServer(t *testing.T, repo *runtimesession.Repository) (*Server, string) {
	t.Helper()
	userStore, tokenService, jwtToken := testAuthSetup(t)
	return &Server{
		agentRepo:    repo,
		UserStore:    userStore,
		TokenService: tokenService,
		runManager:   newRunManager(),
	}, jwtToken
}

func createRuntimeSession(t *testing.T, repo *runtimesession.Repository, id, projectID, firstMessage string) *runtimesession.Session {
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

func appendRuntimeMessage(t *testing.T, repo *runtimesession.Repository, sess *runtimesession.Session, text string) runtimesession.Entry {
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

func appendRuntimeAssistantToolCall(t *testing.T, repo *runtimesession.Repository, sess *runtimesession.Session, callID, name, args string) runtimesession.Entry {
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

func appendRuntimeToolResult(t *testing.T, repo *runtimesession.Repository, sess *runtimesession.Session, callID, name, text string) runtimesession.Entry {
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
