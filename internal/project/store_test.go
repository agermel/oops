package project

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	runtimestore "oops/internal/store/runtime"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	runtime, err := runtimestore.Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	store, err := NewStore(runtime)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func TestStoreCRUDAndDeepCopy(t *testing.T) {
	store := newTestStore(t)

	project := Project{
		ID:                    "p1",
		Name:                  "Project 1",
		NodeletIDs:            []string{"n1"},
		ExcludedContainerRefs: []string{"n1/c1"},
	}
	if err := store.Add(project); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(project); err == nil {
		t.Fatal("expected duplicate project error")
	}

	got := store.Get("p1")
	if got == nil || got.Name != "Project 1" {
		t.Fatalf("Get: got %v", got)
	}
	got.NodeletIDs[0] = "mutated"
	got.ExcludedContainerRefs[0] = "mutated/c1"
	if current := store.Get("p1"); current.NodeletIDs[0] != "n1" || current.ExcludedContainerRefs[0] != "n1/c1" {
		t.Fatalf("Get leaked mutable data: %+v", current)
	}

	listed := store.List()
	listed[0].NodeletIDs[0] = "mutated-list"
	if current := store.Get("p1"); current.NodeletIDs[0] != "n1" {
		t.Fatalf("List leaked mutable data: %+v", current)
	}

	project.Name = "Project 1 Updated"
	project.NodeletIDs = []string{"n1", "n2"}
	if err := store.Update(project); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if current := store.Get("p1"); current.Name != "Project 1 Updated" || len(current.NodeletIDs) != 2 {
		t.Fatalf("updated project: %+v", current)
	}

	if err := store.AddNodelet("p1", "n3"); err != nil {
		t.Fatalf("AddNodelet: %v", err)
	}
	if err := store.RemoveNodelet("p1", "n2"); err != nil {
		t.Fatalf("RemoveNodelet: %v", err)
	}
	if err := store.ExcludeContainer("p1", "n1/c2"); err != nil {
		t.Fatalf("ExcludeContainer: %v", err)
	}
	if err := store.IncludeContainer("p1", "n1/c1"); err != nil {
		t.Fatalf("IncludeContainer: %v", err)
	}
	current := store.Get("p1")
	if len(current.NodeletIDs) != 2 || current.NodeletIDs[0] != "n1" || current.NodeletIDs[1] != "n3" {
		t.Fatalf("nodelets after mutations: %+v", current.NodeletIDs)
	}
	if len(current.ExcludedContainerRefs) != 1 || current.ExcludedContainerRefs[0] != "n1/c2" {
		t.Fatalf("exclusions after mutations: %+v", current.ExcludedContainerRefs)
	}

	if err := store.Remove("p1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(store.List()) != 0 {
		t.Fatal("expected empty store after remove")
	}
}

func TestStoreUpdateCollectionPresenceAndPersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	runtime1, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime1: %v", err)
	}
	store1, err := NewStore(runtime1)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store1.Add(Project{
		ID:                    "p1",
		Name:                  "Test",
		NodeletIDs:            []string{"n1", "n2"},
		ExcludedContainerRefs: []string{"n1/c1"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	original := store1.Get("p1")
	if err := store1.Update(Project{ID: "p1", Name: "Updated", Description: "desc"}); err != nil {
		t.Fatalf("Update preserve: %v", err)
	}
	preserved := store1.Get("p1")
	if len(preserved.NodeletIDs) != 2 || len(preserved.ExcludedContainerRefs) != 1 {
		t.Fatalf("nil collections were not preserved: %+v", preserved)
	}
	if !preserved.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("created at changed: %s -> %s", original.CreatedAt, preserved.CreatedAt)
	}
	if err := store1.Update(Project{ID: "p1", Name: "Updated", NodeletIDs: []string{}, ExcludedContainerRefs: []string{}}); err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	cleared := store1.Get("p1")
	if len(cleared.NodeletIDs) != 0 || len(cleared.ExcludedContainerRefs) != 0 {
		t.Fatalf("explicit empty collections were not applied: %+v", cleared)
	}

	if err := runtime1.Close(); err != nil {
		t.Fatalf("close runtime1: %v", err)
	}
	runtime2, err := runtimestore.Open(dbPath)
	if err != nil {
		t.Fatalf("open runtime2: %v", err)
	}
	defer runtime2.Close()
	store2, err := NewStore(runtime2)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	if got := store2.Get("p1"); got == nil || got.Name != "Updated" || len(got.NodeletIDs) != 0 {
		t.Fatalf("reloaded project: %+v", got)
	}
}

func TestStoreRejectsDuplicateNames(t *testing.T) {
	store := newTestStore(t)
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

func TestStoreConcurrentCollectionUpdates(t *testing.T) {
	store := newTestStore(t)
	if err := store.Add(Project{ID: "p1", Name: "Project"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	const updates = 12
	var wg sync.WaitGroup
	for i := range updates {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := store.AddNodelet("p1", fmt.Sprintf("nodelet-%02d", i)); err != nil {
				t.Errorf("AddNodelet: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := store.ExcludeContainer("p1", fmt.Sprintf("nodelet-%02d/container", i)); err != nil {
				t.Errorf("ExcludeContainer: %v", err)
			}
		}()
	}
	wg.Wait()

	got := store.Get("p1")
	if len(got.NodeletIDs) != updates || len(got.ExcludedContainerRefs) != updates {
		t.Fatalf("concurrent updates lost data: %+v", got)
	}
}
