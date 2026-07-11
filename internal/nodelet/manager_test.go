package nodelet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	runtimestore "oops/internal/store/runtime"

	"github.com/cenkalti/backoff/v4"
)

func newTestNodeletManager(t *testing.T) *NodeletManager {
	t.Helper()
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	m, err := NewNodeletManagerWithRuntime(runtime)
	if err != nil {
		t.Fatalf("NewNodeletManagerWithRuntime: %v", err)
	}
	return m
}

// ---- 内存模式 ----

// TestNodeletManager_NewInMemory 测试空路径创建纯内存 manager。
func TestNodeletManager_NewInMemory(t *testing.T) {
	m := newTestNodeletManager(t)
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	list := m.List()
	if len(list) != 0 {
		t.Errorf("List len = %d, want 0", len(list))
	}
}

// TestNodeletManager_Add 测试新增 nodelet。
func TestNodeletManager_Add(t *testing.T) {
	m := newTestNodeletManager(t)
	cfg := NodeletConfig{ID: "node-1", Name: "node-1", Address: "http://10.0.0.1:8080", Token: "secret"}
	if err := m.Add(&cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if list[0].Name != "node-1" {
		t.Errorf("Name = %q, want %q", list[0].Name, "node-1")
	}
	if !list[0].HasToken {
		t.Error("HasToken should be true when Token is set")
	}

	found, ok := m.Find("node-1")
	if !ok {
		t.Fatal("Find returned false for existing name")
	}
	if found.Name != "node-1" {
		t.Errorf("found.Name = %q, want %q", found.Name, "node-1")
	}
}

// TestNodeletManager_Add_Duplicate 测试重复名称被拒绝。
func TestNodeletManager_Add_Duplicate(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "dup", Name: "dup", Token: "t1"})
	err := m.Add(&NodeletConfig{ID: "dup", Name: "dup", Token: "t2"})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !containsStr(err.Error(), "already exists") {
		t.Errorf("error %q does not contain 'already exists'", err.Error())
	}
}

// TestNodeletManager_Add_EmptyToken 测试空 Token 被拒绝。
func TestNodeletManager_Add_EmptyToken(t *testing.T) {
	m := newTestNodeletManager(t)
	err := m.Add(&NodeletConfig{ID: "no-token", Name: "no-token"})
	if err == nil {
		t.Fatal("expected error for empty token")
	}
	if !containsStr(err.Error(), "token is required") {
		t.Errorf("error %q does not contain 'token is required'", err.Error())
	}
}

// TestNodeletManager_Add_EmptyName 测试空名称被拒绝。
func TestNodeletManager_Add_EmptyName(t *testing.T) {
	m := newTestNodeletManager(t)
	err := m.Add(&NodeletConfig{Name: "", Token: "t"})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !containsStr(err.Error(), "name is required") {
		t.Errorf("error %q does not contain 'name is required'", err.Error())
	}
}

// TestNodeletManager_List_CopySemantics 测试 List 返回的是副本。
func TestNodeletManager_List_CopySemantics(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "original", Name: "original", Token: "t1"})

	list1 := m.List()
	list1[0].Name = "modified" // 修改返回的副本

	list2 := m.List()
	if list2[0].Name != "original" {
		t.Errorf("Name = %q, want %q — List should return a copy", list2[0].Name, "original")
	}
}

// TestNodeletManager_Find_NotFound 测试查找不存在的 ID。
func TestNodeletManager_Find_NotFound(t *testing.T) {
	m := newTestNodeletManager(t)
	_, ok := m.Find("nonexistent")
	if ok {
		t.Error("Find should return false for nonexistent ID")
	}
}

// TestNodeletManager_Update 测试更新已有 nodelet。
func TestNodeletManager_Update(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Address: "http://old:8080", Token: "t1"})

	err := m.Update(NodeletConfig{ID: "n1", Name: "n1", Address: "http://new:8080", Token: "t"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	found, _ := m.Find("n1")
	if found.Address != "http://new:8080" {
		t.Errorf("Address = %q, want %q", found.Address, "http://new:8080")
	}
}

// TestNodeletManager_Update_NotFound 测试更新不存在的名称。
func TestNodeletManager_Update_NotFound(t *testing.T) {
	m := newTestNodeletManager(t)
	err := m.Update(NodeletConfig{ID: "ghost", Name: "ghost", Token: "t"})
	if err == nil {
		t.Fatal("expected error for updating nonexistent ID")
	}
	if !containsStr(err.Error(), "not found") {
		t.Errorf("error %q does not contain 'not found'", err.Error())
	}
}

// TestNodeletManager_Remove 测试删除已有 nodelet。
func TestNodeletManager_Remove(t *testing.T) {
	m := newTestNodeletManager(t)
	_ = m.Add(&NodeletConfig{ID: "n1", Name: "n1", Token: "t1"})

	if err := m.Remove("n1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(m.List()) != 0 {
		t.Error("List should be empty after remove")
	}
	_, ok := m.Find("n1")
	if ok {
		t.Error("Find should return false after remove")
	}
}

// TestNodeletManager_Remove_NotFound 测试删除不存在的 ID。
func TestNodeletManager_Remove_NotFound(t *testing.T) {
	m := newTestNodeletManager(t)
	err := m.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent ID")
	}
	if !containsStr(err.Error(), "not found") {
		t.Errorf("error %q does not contain 'not found'", err.Error())
	}
}

// ---- SQLite 持久化 ----

func TestNodeletManager_RuntimeStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	runtime, err := runtimestore.Open(filepath.Join(dir, "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()

	m, err := NewNodeletManagerWithRuntime(runtime)
	if err != nil {
		t.Fatalf("NewNodeletManagerWithRuntime: %v", err)
	}
	if len(m.List()) != 0 {
		t.Error("List should be empty when runtime DB is empty")
	}
}

func TestNodeletManager_RuntimePersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")

	runtime1, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime1: %v", err)
	}
	m1, err := NewNodeletManagerWithRuntime(runtime1)
	if err != nil {
		t.Fatalf("first NewNodeletManagerWithRuntime: %v", err)
	}
	_ = m1.Add(&NodeletConfig{ID: "first", Name: "first", Token: "t1"})
	_ = m1.Add(&NodeletConfig{ID: "second", Name: "second", Token: "t2"})
	_ = m1.Update(NodeletConfig{ID: "first", Name: "first", Address: "http://updated:8080", Token: "t1"})
	_ = m1.Remove("second")
	if err := runtime1.Close(); err != nil {
		t.Fatalf("close runtime1: %v", err)
	}

	runtime2, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime2: %v", err)
	}
	defer runtime2.Close()
	m2, err := NewNodeletManagerWithRuntime(runtime2)
	if err != nil {
		t.Fatalf("second NewNodeletManagerWithRuntime: %v", err)
	}

	list := m2.List()
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if list[0].Name != "first" {
		t.Errorf("Name = %q, want %q", list[0].Name, "first")
	}
	if list[0].Address != "http://updated:8080" {
		t.Errorf("Address = %q, want %q", list[0].Address, "http://updated:8080")
	}
}

// ---- Test() 方法 ----

// TestNodeletManager_Test_Success 测试 /host + Bearer 返回 200。
func TestNodeletManager_Test_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/host" && r.Header.Get("Authorization") == "Bearer secret" {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	m := newTestNodeletManager(t)
	err := m.Test(context.Background(), NodeletConfig{Address: srv.URL, Token: "secret"})
	if err != nil {
		t.Fatalf("Test: expected nil, got %v", err)
	}
}

// TestNodeletManager_Test_Unauthorized 测试 /host 返回 401 即 token 错误。
func TestNodeletManager_Test_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	m := newTestNodeletManager(t)
	err := m.Test(context.Background(), NodeletConfig{Address: srv.URL, Token: "wrong"})
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
	if !containsStr(err.Error(), "unauthorized") {
		t.Errorf("error %q should contain 'unauthorized'", err.Error())
	}
}

// TestNodeletManager_Test_Non200 测试 /host 返回非 200/401 且有重试。
func TestNodeletManager_Test_Non200(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	m := newTestNodeletManager(t)
	err := m.testWithBackoff(context.Background(), NodeletConfig{Address: srv.URL, Token: "t"}, backoff.NewConstantBackOff(0))
	if err == nil {
		t.Fatal("expected error for non-200 host")
	}
	if !containsStr(err.Error(), "503") {
		t.Errorf("error %q should contain status code 503", err.Error())
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

// TestNodeletManager_Test_BadAddress 测试非法地址立即失败。
func TestNodeletManager_Test_BadAddress(t *testing.T) {
	m := newTestNodeletManager(t)
	err := m.testWithBackoff(context.Background(), NodeletConfig{Address: "://invalid", Token: "t"}, backoff.NewConstantBackOff(0))
	if err == nil {
		t.Fatal("expected error for bad address")
	}
	if !containsStr(err.Error(), "bad address") {
		t.Errorf("error %q should contain 'bad address'", err.Error())
	}
}

func TestNodeletManager_Test_PermanentClientError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	m := newTestNodeletManager(t)
	err := m.testWithBackoff(context.Background(), NodeletConfig{Address: srv.URL, Token: "t"}, backoff.NewConstantBackOff(0))
	if err == nil || !containsStr(err.Error(), "403") {
		t.Fatalf("error = %v, want 403", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestNodeletManager_Test_ContextCanceled(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newTestNodeletManager(t)
	errCh := make(chan error, 1)
	go func() {
		errCh <- m.testWithBackoff(ctx, NodeletConfig{Address: srv.URL, Token: "t"}, backoff.NewConstantBackOff(0))
	}()
	<-started
	cancel()

	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
