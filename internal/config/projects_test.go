package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")

	store, err := NewProjectStore(path)
	if err != nil {
		t.Fatalf("NewProjectStore: %v", err)
	}

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
	path := filepath.Join(dir, "projects.json")

	// 创建并写入。
	store1, err := NewProjectStore(path)
	if err != nil {
		t.Fatalf("NewProjectStore: %v", err)
	}
	if err := store1.Add(Project{ID: "p1", Name: "Test"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// 重新加载。
	store2, err := NewProjectStore(path)
	if err != nil {
		t.Fatalf("NewProjectStore (reload): %v", err)
	}
	got := store2.Get("p1")
	if got == nil || got.Name != "Test" {
		t.Fatalf("reloaded project: %v", got)
	}
}

func TestProjectStoreEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects.json")

	// 创建空文件。
	if err := os.WriteFile(path, []byte(`{"projects":[]}`), 0600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}

	store, err := NewProjectStore(path)
	if err != nil {
		t.Fatalf("NewProjectStore: %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("expected empty store")
	}
}
