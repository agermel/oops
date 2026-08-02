package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oops/internal/agent/runtime/skills"
)

func TestHandleSkillsUpdateUsesLoadedFilePathAndUpdatesStore(t *testing.T) {
	store := newTestSkillStore(t)
	server := &Server{skillStore: store}
	original, ok := store.Get("diagnose")
	if !ok {
		t.Fatal("diagnose skill missing")
	}

	request := httptest.NewRequest(http.MethodPut, "/api/skills/diagnose", strings.NewReader(`{
		"description":"Updated diagnosis",
		"content":"Inspect carefully.",
		"icon":"Search",
		"label":"Diagnose",
		"color":"blue",
		"enabled":true
	}`))
	request.SetPathValue("name", "diagnose")
	recorder := httptest.NewRecorder()

	server.handleSkillsUpdate(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	updated, ok := store.Get("diagnose")
	if !ok {
		t.Fatal("updated skill missing")
	}
	if updated.FilePath != original.FilePath || updated.Description != "Updated diagnosis" || updated.Content != "Inspect carefully." {
		t.Fatalf("updated skill = %#v", updated)
	}
	data, err := os.ReadFile(original.FilePath)
	if err != nil {
		t.Fatalf("read updated skill: %v", err)
	}
	for _, want := range []string{"description: Updated diagnosis", "Inspect carefully."} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("updated file missing %q:\n%s", want, data)
		}
	}
}

func TestHandleSkillsUpdateKeepsStoreValueWhenWriteFails(t *testing.T) {
	store := newTestSkillStore(t)
	existing, ok := store.Get("diagnose")
	if !ok {
		t.Fatal("diagnose skill missing")
	}
	existing.FilePath = t.TempDir()
	if !store.Update(existing) {
		t.Fatal("replace test skill path")
	}
	server := &Server{skillStore: store}

	request := httptest.NewRequest(http.MethodPut, "/api/skills/diagnose", strings.NewReader(`{
		"description":"Should not persist",
		"content":"changed",
		"enabled":true
	}`))
	request.SetPathValue("name", "diagnose")
	recorder := httptest.NewRecorder()

	server.handleSkillsUpdate(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
	got, ok := store.Get("diagnose")
	if !ok {
		t.Fatal("diagnose skill missing after failed update")
	}
	if got.Description != existing.Description || got.Content != existing.Content {
		t.Fatalf("store changed after failed write: %#v", got)
	}
}

func TestHandleSkillsUpdateAndDeleteRejectRedirectedDirectorySymlink(t *testing.T) {
	root := t.TempDir()
	insideDir := filepath.Join(root, ".targets", "linked")
	outsideDir := t.TempDir()
	for path, content := range map[string]string{
		filepath.Join(insideDir, "SKILL.md"):  "---\nname: linked\ndescription: Inside\nenabled: false\n---\nInside body.\n",
		filepath.Join(outsideDir, "SKILL.md"): "---\nname: linked\ndescription: Outside\nenabled: false\n---\nOutside body.\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	linkPath := filepath.Join(root, "linked")
	if err := os.Symlink(filepath.Join(".targets", "linked"), linkPath); err != nil {
		t.Fatal(err)
	}
	store, err := skills.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if _, ok := store.Get("linked"); !ok {
		t.Fatal("linked skill missing before redirection")
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(outsideDir, "SKILL.md")
	outsideBefore, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{skillStore: store}
	update := httptest.NewRequest(http.MethodPut, "/api/skills/linked", strings.NewReader(`{
		"description":"Changed",
		"content":"Changed body.",
		"enabled":false
	}`))
	update.SetPathValue("name", "linked")
	updateRecorder := httptest.NewRecorder()
	server.handleSkillsUpdate(updateRecorder, update)
	if updateRecorder.Code != http.StatusConflict {
		t.Fatalf("update status = %d, want %d; body=%s", updateRecorder.Code, http.StatusConflict, updateRecorder.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/skills/linked", nil)
	deleteRequest.SetPathValue("name", "linked")
	deleteRecorder := httptest.NewRecorder()
	server.handleSkillsDelete(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusConflict {
		t.Fatalf("delete status = %d, want %d; body=%s", deleteRecorder.Code, http.StatusConflict, deleteRecorder.Body.String())
	}
	outsideAfter, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(outsideAfter) != string(outsideBefore) {
		t.Fatalf("outside skill changed:\n%s", outsideAfter)
	}
}

func TestHandleSkillsDeleteAllowsDisabledSkillWithOneEnabledSkill(t *testing.T) {
	store := newTestSkillStore(t)
	disabled, ok := store.Get("disabled")
	if !ok {
		t.Fatal("disabled skill missing")
	}
	server := &Server{skillStore: store}
	request := httptest.NewRequest(http.MethodDelete, "/api/skills/disabled", nil)
	request.SetPathValue("name", "disabled")
	recorder := httptest.NewRecorder()

	server.handleSkillsDelete(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if _, err := os.Stat(disabled.FilePath); !os.IsNotExist(err) {
		t.Fatalf("deleted skill path still exists: %v", err)
	}
}
