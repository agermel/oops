package config

import (
	"path/filepath"
	"testing"

	runtimestore "oops/internal/store/runtime"
)

func newTestProjectStore(t *testing.T) *ProjectStore {
	t.Helper()
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	store, err := NewProjectStoreWithRuntime(runtime)
	if err != nil {
		t.Fatalf("NewProjectStoreWithRuntime: %v", err)
	}
	return store
}

func TestProjectStoreCRUD(t *testing.T) {
	store := newTestProjectStore(t)

	// 初始为空。
	if len(store.List()) != 0 {
		t.Fatal("expected empty store")
	}

	// Add。
	p1 := Project{ID: "p1", Name: "Project 1", NodeletIDs: []string{"n1"}}
	if err := store.Add(p1); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(store.List()) != 1 {
		t.Fatal("expected 1 project after add")
	}

	// Add duplicate。
	if err := store.Add(p1); err == nil {
		t.Fatal("expected error on duplicate add")
	}

	// Get。
	got := store.Get("p1")
	if got == nil || got.Name != "Project 1" {
		t.Fatalf("Get: got %v", got)
	}
	if len(got.NodeletIDs) != 1 || got.NodeletIDs[0] != "n1" {
		t.Fatalf("NodeletIDs: %v", got.NodeletIDs)
	}

	// Update。
	p1.Name = "Project 1 Updated"
	p1.NodeletIDs = []string{"n1", "n2"}
	if err := store.Update(p1); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got = store.Get("p1")
	if got.Name != "Project 1 Updated" || len(got.NodeletIDs) != 2 {
		t.Fatalf("after update: %v", got)
	}

	// AddNodelet.
	if err := store.AddNodelet("p1", "n3"); err != nil {
		t.Fatalf("AddNodelet: %v", err)
	}
	got = store.Get("p1")
	if len(got.NodeletIDs) != 3 {
		t.Fatalf("after AddNodelet: %v", got.NodeletIDs)
	}

	// RemoveNodelet.
	if err := store.RemoveNodelet("p1", "n2"); err != nil {
		t.Fatalf("RemoveNodelet: %v", err)
	}
	got = store.Get("p1")
	if len(got.NodeletIDs) != 2 || got.NodeletIDs[0] != "n1" || got.NodeletIDs[1] != "n3" {
		t.Fatalf("after RemoveNodelet: %v", got.NodeletIDs)
	}

	// Remove.
	if err := store.Remove("p1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("expected empty store after remove")
	}
}

func TestProjectStorePersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")

	runtime1, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime1: %v", err)
	}
	store1, err := NewProjectStoreWithRuntime(runtime1)
	if err != nil {
		t.Fatalf("NewProjectStoreWithRuntime: %v", err)
	}
	if err := store1.Add(Project{ID: "p1", Name: "Test"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := runtime1.Close(); err != nil {
		t.Fatalf("close runtime1: %v", err)
	}

	runtime2, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime2: %v", err)
	}
	defer runtime2.Close()
	store2, err := NewProjectStoreWithRuntime(runtime2)
	if err != nil {
		t.Fatalf("NewProjectStoreWithRuntime reload: %v", err)
	}
	got := store2.Get("p1")
	if got == nil || got.Name != "Test" {
		t.Fatalf("reloaded project: %v", got)
	}
}

func TestProjectStoreUpdatePreservesNodelets(t *testing.T) {
	store := newTestProjectStore(t)

	// 创建带 server 的项目。
	if err := store.Add(Project{ID: "p1", Name: "Test", NodeletIDs: []string{"n1", "n2"}}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// 模拟前端只发 name + description（不带 nodeletIds）的更新。
	update := Project{ID: "p1", Name: "Updated", Description: "desc"}
	if err := store.Update(update); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got := store.Get("p1")
	if got == nil {
		t.Fatal("project not found after update")
	}
	if got.Name != "Updated" {
		t.Fatalf("name not updated: %q", got.Name)
	}
	if got.Description != "desc" {
		t.Fatalf("description not updated: %q", got.Description)
	}
	if len(got.NodeletIDs) != 2 || got.NodeletIDs[0] != "n1" || got.NodeletIDs[1] != "n2" {
		t.Fatalf("NodeletIDs were lost after partial update: %v", got.NodeletIDs)
	}
}

func TestProjectStoreRejectsDuplicateNames(t *testing.T) {
	store := newTestProjectStore(t)

	if err := store.Add(Project{ID: "p1", Name: "Project"}); err != nil {
		t.Fatalf("Add p1: %v", err)
	}
	if err := store.Add(Project{ID: "p2", Name: "Project"}); err == nil {
		t.Fatal("expected duplicate name error on add")
	}
	if err := store.Add(Project{ID: "p2", Name: "Project 2"}); err != nil {
		t.Fatalf("Add p2: %v", err)
	}
	if err := store.Update(Project{ID: "p2", Name: "Project"}); err == nil {
		t.Fatal("expected duplicate name error on update")
	}
}

func TestProjectStoreRuntimeStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	runtime, err := runtimestore.Open(filepath.Join(dir, "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	store, err := NewProjectStoreWithRuntime(runtime)
	if err != nil {
		t.Fatalf("NewProjectStoreWithRuntime: %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("expected empty store")
	}
}
