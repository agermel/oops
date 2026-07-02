package nodelet

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// ---- 内存模式（configPath=""）----

// TestNodeletManager_NewInMemory 测试空路径创建纯内存 manager。
func TestNodeletManager_NewInMemory(t *testing.T) {
	m, err := NewNodeletManager("")
	if err != nil {
		t.Fatalf("NewNodeletManager: %v", err)
	}
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
	_, ok := m.Find("nonexistent")
	if ok {
		t.Error("Find should return false for nonexistent ID")
	}
}

// TestNodeletManager_Update 测试更新已有 nodelet。
func TestNodeletManager_Update(t *testing.T) {
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
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
	m, _ := NewNodeletManager("")
	err := m.Remove("nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent ID")
	}
	if !containsStr(err.Error(), "not found") {
		t.Errorf("error %q does not contain 'not found'", err.Error())
	}
}

// ---- 持久化模式 ----

// TestNodeletManager_NewFromFile 测试从预写 JSON 文件加载。
func TestNodeletManager_NewFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nodelets.json")
	content := `{"nodelets":[{"id":"loaded","name":"loaded","address":"http://a:1","token":"t1"}]}`
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := NewNodeletManager(p)
	if err != nil {
		t.Fatalf("NewNodeletManager: %v", err)
	}

	list := m.List()
	if len(list) != 1 {
		t.Fatalf("List len = %d, want 1", len(list))
	}
	if list[0].Name != "loaded" {
		t.Errorf("Name = %q, want %q", list[0].Name, "loaded")
	}
}

// TestNodeletManager_NewFromMissingFile 测试文件不存在时成功初始化为空。
func TestNodeletManager_NewFromMissingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "missing.json")

	m, err := NewNodeletManager(p)
	if err != nil {
		t.Fatalf("NewNodeletManager: %v", err)
	}
	if len(m.List()) != 0 {
		t.Error("List should be empty when file is missing")
	}
}

// TestNodeletManager_NewFromMalformedFile 测试非法 JSON 返回错误。
func TestNodeletManager_NewFromMalformedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(`garbage`), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := NewNodeletManager(p)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

// TestNodeletManager_PersistRoundTrip 测试增删改后重新加载一致。
func TestNodeletManager_PersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nodelets.json")

	// 第一次创建并修改
	m1, err := NewNodeletManager(p)
	if err != nil {
		t.Fatalf("first NewNodeletManager: %v", err)
	}
	_ = m1.Add(&NodeletConfig{ID: "first", Name: "first", Token: "t1"})
	_ = m1.Add(&NodeletConfig{ID: "second", Name: "second", Token: "t2"})
	_ = m1.Update(NodeletConfig{ID: "first", Name: "first", Address: "http://updated:8080", Token: "t1"})
	_ = m1.Remove("second")

	// 第二次从同一文件加载
	m2, err := NewNodeletManager(p)
	if err != nil {
		t.Fatalf("second NewNodeletManager: %v", err)
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

	m, _ := NewNodeletManager("")
	err := m.Test(NodeletConfig{Address: srv.URL, Token: "secret"})
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

	m, _ := NewNodeletManager("")
	err := m.Test(NodeletConfig{Address: srv.URL, Token: "wrong"})
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
	if !containsStr(err.Error(), "unauthorized") {
		t.Errorf("error %q should contain 'unauthorized'", err.Error())
	}
}

// TestNodeletManager_Test_Non200 测试 /host 返回非 200/401 且有重试。
func TestNodeletManager_Test_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	m, _ := NewNodeletManager("")
	err := m.Test(NodeletConfig{Address: srv.URL, Token: "t"})
	if err == nil {
		t.Fatal("expected error for non-200 host")
	}
	if !containsStr(err.Error(), "503") {
		t.Errorf("error %q should contain status code 503", err.Error())
	}
}

// TestNodeletManager_Test_BadAddress 测试非法地址立即失败。
func TestNodeletManager_Test_BadAddress(t *testing.T) {
	m, _ := NewNodeletManager("")
	err := m.Test(NodeletConfig{Address: "://invalid", Token: "t"})
	if err == nil {
		t.Fatal("expected error for bad address")
	}
	if !containsStr(err.Error(), "bad address") {
		t.Errorf("error %q should contain 'bad address'", err.Error())
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
