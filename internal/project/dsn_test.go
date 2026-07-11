package project

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	runtimestore "oops/internal/store/runtime"
)

func TestDSNStorePersistsAndCopiesValues(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	runtime1, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime1: %v", err)
	}
	store1, err := NewDSNStore(runtime1)
	if err != nil {
		t.Fatalf("NewDSNStore: %v", err)
	}
	pairs := map[string]string{"host": "127.0.0.1", "port": "6379"}
	if err := store1.Set("n1", "c1", pairs); err != nil {
		t.Fatalf("Set: %v", err)
	}
	pairs["host"] = "mutated"
	got := store1.Get("n1", "c1")
	if got["host"] != "127.0.0.1" {
		t.Fatalf("Set retained caller map: %+v", got)
	}
	got["port"] = "mutated"
	if current := store1.Get("n1", "c1"); current["port"] != "6379" {
		t.Fatalf("Get leaked mutable map: %+v", current)
	}
	if err := store1.Set("n1", "c1", map[string]string{"host": "10.0.0.1"}); err != nil {
		t.Fatalf("replace Set: %v", err)
	}
	if got := store1.Get("n1", "c1"); got["host"] != "10.0.0.1" || len(got) != 1 {
		t.Fatalf("Set did not replace the whole container: %+v", got)
	}
	if err := runtime1.Close(); err != nil {
		t.Fatalf("close runtime1: %v", err)
	}

	runtime2, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime2: %v", err)
	}
	defer runtime2.Close()
	store2, err := NewDSNStore(runtime2)
	if err != nil {
		t.Fatalf("NewDSNStore reload: %v", err)
	}
	if got := store2.Get("n1", "c1"); got["host"] != "10.0.0.1" || len(got) != 1 {
		t.Fatalf("persisted dsn = %+v", got)
	}
	if err := store2.Delete("n1", "c1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := store2.Get("n1", "c1"); got != nil {
		t.Fatalf("dsn after delete = %+v", got)
	}
}

func TestDSNStoreConcurrentContainerUpdates(t *testing.T) {
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	store, err := NewDSNStore(runtime)
	if err != nil {
		t.Fatalf("NewDSNStore: %v", err)
	}

	const containers = 12
	var wg sync.WaitGroup
	for i := range containers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			containerID := fmt.Sprintf("container-%02d", i)
			if err := store.Set("nodelet", containerID, map[string]string{"host": containerID}); err != nil {
				t.Errorf("Set(%s): %v", containerID, err)
			}
		}()
	}
	wg.Wait()

	for i := range containers {
		containerID := fmt.Sprintf("container-%02d", i)
		if got := store.Get("nodelet", containerID); got["host"] != containerID {
			t.Fatalf("Get(%s) = %+v", containerID, got)
		}
	}
}
