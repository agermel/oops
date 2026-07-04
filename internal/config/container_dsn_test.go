package config

import (
	"path/filepath"
	"testing"

	runtimestore "oops/internal/store/runtime"
)

func TestContainerDSNStoreRuntimePersistsValues(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")

	runtime1, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime1: %v", err)
	}
	store1, err := NewContainerDSNStoreWithRuntime(runtime1)
	if err != nil {
		t.Fatalf("NewContainerDSNStoreWithRuntime: %v", err)
	}
	if err := store1.Set("n1", "c1", map[string]string{"host": "127.0.0.1", "port": "6379"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := runtime1.Close(); err != nil {
		t.Fatalf("close runtime1: %v", err)
	}

	runtime2, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime2: %v", err)
	}
	defer runtime2.Close()
	store2, err := NewContainerDSNStoreWithRuntime(runtime2)
	if err != nil {
		t.Fatalf("NewContainerDSNStoreWithRuntime reload: %v", err)
	}
	got := store2.Get("n1", "c1")
	if got["host"] != "127.0.0.1" || got["port"] != "6379" {
		t.Fatalf("persisted dsn = %+v", got)
	}
	if err := store2.Delete("n1", "c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := store2.Get("n1", "c1"); got != nil {
		t.Fatalf("dsn after delete = %+v", got)
	}
}
