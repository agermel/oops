package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"oops/internal/llm/session"

	"github.com/cloudwego/eino/schema"
)

func TestSessionHandlersReturnCompatibleFields(t *testing.T) {
	userStore, tokenService, jwtToken := testAuthSetup(t)
	store := session.NewSessionStore()
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
	var infos []session.SessionInfo
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
	var detail session.SessionDetail
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
