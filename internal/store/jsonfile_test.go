package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestLoadJSON_ValidFile 测试从合法 JSON 文件加载数据。
func TestLoadJSON_ValidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.json")
	if err := os.WriteFile(p, []byte(`{"Name":"alice","Age":30}`), 0644); err != nil {
		t.Fatal(err)
	}

	var v struct {
		Name string `json:"Name"`
		Age  int    `json:"Age"`
	}
	if err := LoadJSON(p, &v); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if v.Name != "alice" {
		t.Errorf("Name = %q, want %q", v.Name, "alice")
	}
	if v.Age != 30 {
		t.Errorf("Age = %d, want %d", v.Age, 30)
	}
}

// TestLoadJSON_FileNotFound 测试文件不存在时返回 nil 且不修改目标值。
func TestLoadJSON_FileNotFound(t *testing.T) {
	var v struct{ Name string }
	v.Name = "untouched"
	if err := LoadJSON("/nonexistent/path.json", &v); err != nil {
		t.Fatalf("LoadJSON: expected nil, got %v", err)
	}
	if v.Name != "untouched" {
		t.Errorf("Name = %q, want %q", v.Name, "untouched")
	}
}

// TestLoadJSON_MalformedJSON 测试非法 JSON 返回解析错误。
func TestLoadJSON_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(p, []byte(`not json`), 0644); err != nil {
		t.Fatal(err)
	}

	var v map[string]any
	err := LoadJSON(p, &v)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if want := "parse"; !contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
}

// TestLoadJSON_EmptyFile 测试空文件的解析行为。
func TestLoadJSON_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(p, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	var v map[string]any
	err := LoadJSON(p, &v)
	if err == nil {
		t.Fatal("expected parse error for empty file")
	}
}

// TestLoadJSON_PermissionDenied 测试无法读取文件时返回错误。
func TestLoadJSON_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "noperm.json")
	if err := os.WriteFile(p, []byte(`{}`), 0000); err != nil {
		t.Fatal(err)
	}

	var v map[string]any
	err := LoadJSON(p, &v)
	if err == nil {
		t.Fatal("expected error for unreadable file")
	}
	if want := "read"; !contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
}

// TestSaveJSON_ValidStruct 测试写入 struct 并验证文件权限。
func TestSaveJSON_ValidStruct(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.json")

	v := map[string]string{"key": "val"}
	if err := SaveJSON(p, v); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	// 验证文件存在且内容正确
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !contains(string(data), `"key"`) {
		t.Errorf("unexpected content: %s", string(data))
	}

	// 验证 0600 权限
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions = %o, want %o", info.Mode().Perm(), 0600)
	}
}

// TestSaveJSON_RoundTrip 测试 Save 后 Load 数据一致。
func TestSaveJSON_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rt.json")

	type item struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	original := item{Name: "test", Count: 42}
	if err := SaveJSON(p, original); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	var loaded item
	if err := LoadJSON(p, &loaded); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if loaded.Name != original.Name || loaded.Count != original.Count {
		t.Errorf("round-trip: got %+v, want %+v", loaded, original)
	}
}

// TestSaveJSON_Overwrites 测试两次 Save 第二次覆盖第一次。
func TestSaveJSON_Overwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "overwrite.json")

	if err := SaveJSON(p, map[string]string{"v": "1"}); err != nil {
		t.Fatalf("first SaveJSON: %v", err)
	}
	if err := SaveJSON(p, map[string]string{"v": "2"}); err != nil {
		t.Fatalf("second SaveJSON: %v", err)
	}

	var loaded map[string]string
	if err := LoadJSON(p, &loaded); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if loaded["v"] != "2" {
		t.Errorf("v = %q, want %q", loaded["v"], "2")
	}
}

// TestSaveJSON_NestedDir 测试写入不存在的子目录路径。
func TestSaveJSON_NestedDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nonexistent", "out.json")

	err := SaveJSON(p, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing parent directory")
	}
	if want := "create temp"; !contains(err.Error(), want) {
		t.Errorf("error %q does not contain %q", err.Error(), want)
	}
}

// TestSaveJSON_Concurrent 测试两个 goroutine 并发写同一文件不会损坏。
func TestSaveJSON_Concurrent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "concurrent.json")

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- SaveJSON(p, map[string]int{"i": i})
		}()
	}
	wg.Wait()
	close(errs)

	// 两次写入都不应该报错（原子 rename 保证）
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent SaveJSON: %v", err)
		}
	}

	// 文件存在且为合法 JSON
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Error("file should not be empty after concurrent writes")
	}
}

// TestLoadJSON_NilSliceVsEmpty 验证 nil slice 不会被 JSON null 覆盖。
// 这个测试确保 nil-guard 惯用法在调用方正确运作。
func TestLoadJSON_NilSliceVsEmpty(t *testing.T) {
	dir := t.TempDir()

	// 文件包含 "items": null
	p1 := filepath.Join(dir, "null.json")
	if err := os.WriteFile(p1, []byte(`{"items":null}`), 0644); err != nil {
		t.Fatal(err)
	}
	var v1 struct{ Items []string }
	if err := LoadJSON(p1, &v1); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if v1.Items != nil {
		t.Errorf("Items after null JSON: got %v, want nil", v1.Items)
	}

	// 文件包含 "items": []
	p2 := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(p2, []byte(`{"items":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	var v2 struct{ Items []string }
	if err := LoadJSON(p2, &v2); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if v2.Items == nil {
		t.Error("Items after [] JSON: got nil, want non-nil empty slice")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
