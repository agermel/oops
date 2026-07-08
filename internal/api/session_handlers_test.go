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
	oldsession "oops/internal/llm/session"

	"github.com/cloudwego/eino/schema"
)

func TestSessionHandlersReturnCompatibleFields(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	store := oldsession.NewSessionStore()
	sess := store.Create("proj-1")
	store.AppendMessage(sess.ID, schema.UserMessage("hello"))
	store.AppendMessage(sess.ID, &schema.Message{
		Role:       schema.Tool,
		Content:    "done",
		ToolName:   "repo_sync",
		ToolCallID: "call-1",
	})
	server := &Server{
		sessionStore: store,
		UserStore:    userStore,
		TokenService: tokenService,
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/sessions?project_id=proj-1", nil)
	listReq.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	listResp := httptest.NewRecorder()
	server.Routes().ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listResp.Code, http.StatusOK)
	}
	var infos []oldsession.SessionInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len(infos) = %d, want 1", len(infos))
	}
	if infos[0].ID != sess.ID || infos[0].ProjectID != "proj-1" || infos[0].MessageCount != 2 {
		t.Fatalf("SessionInfo = %+v", infos[0])
	}

	detailReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sess.ID+"?include_messages=true", nil)
	detailReq.AddCookie(&http.Cookie{Name: "jwt", Value: jwtToken})
	detailResp := httptest.NewRecorder()
	server.Routes().ServeHTTP(detailResp, detailReq)
	if detailResp.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want %d", detailResp.Code, http.StatusOK)
	}
	var detail oldsession.SessionDetail
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != sess.ID || detail.ProjectID != "proj-1" || detail.MessageCount != 2 {
		t.Fatalf("SessionDetail header = %+v", detail.SessionInfo)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(detail.Messages))
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Content != "hello" {
		t.Fatalf("first message = %+v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "tool" || detail.Messages[1].ToolName != "repo_sync" || detail.Messages[1].ToolCallID != "call-1" {
		t.Fatalf("second message = %+v", detail.Messages[1])
	}
}

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
	var infos []oldsession.SessionInfo
	if err := json.NewDecoder(listResp.Body).Decode(&infos); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != sess.ID() || infos[0].ProjectID != "proj-1" || infos[0].MessageCount != 1 {
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
	if len(infos) != 1 || infos[0].ID != sess.ID() || infos[0].ProjectID != "proj-1" || infos[0].MessageCount != 1 {
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
	if snapshot.LeafID != left.ID {
		t.Fatalf("leaf = %q, want %q", snapshot.LeafID, left.ID)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(snapshot.Messages))
	}
	if runtimeMessageText(t, snapshot.Messages[0]) != "root" || runtimeMessageText(t, snapshot.Messages[1]) != "left" {
		t.Fatalf("message path = %q, %q", runtimeMessageText(t, snapshot.Messages[0]), runtimeMessageText(t, snapshot.Messages[1]))
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
	if len(snapshot.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(snapshot.Messages))
	}
	if runtimeMessageText(t, snapshot.Messages[2]) != "Branch summary:\n\nleft branch read left.md" {
		t.Fatalf("summary message = %q", runtimeMessageText(t, snapshot.Messages[2]))
	}
	last := snapshot.Entries[len(snapshot.Entries)-1]
	if last.Type != runtimesession.EntryBranchSummary || last.ParentID != right.ID {
		t.Fatalf("last entry = %#v", last)
	}
}

func newRuntimeSessionTestServer(t *testing.T, repo *runtimesession.Repository) (*Server, string) {
	t.Helper()
	userStore, tokenService, jwtToken := testAuthSetup(t)
	return &Server{
		agentRepo:    repo,
		sessionStore: oldsession.NewSessionStore(),
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
