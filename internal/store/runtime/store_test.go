package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigratesRuntimeDB(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	count, err := store.CountNodelets(context.Background())
	if err != nil {
		t.Fatalf("CountNodelets: %v", err)
	}
	if count != 0 {
		t.Fatalf("CountNodelets = %d, want 0", count)
	}
}

func TestRuntimeStoreReplaceAndList(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	nodelets := []NodeletRecord{{ID: "1", Name: "n1", Address: "http://nodelet", Token: "secret"}}
	if err := store.ReplaceNodelets(ctx, nodelets); err != nil {
		t.Fatalf("ReplaceNodelets: %v", err)
	}
	gotNodelets, err := store.ListNodelets(ctx)
	if err != nil {
		t.Fatalf("ListNodelets: %v", err)
	}
	if len(gotNodelets) != 1 || gotNodelets[0].Name != "n1" || gotNodelets[0].Token != "secret" {
		t.Fatalf("nodelets = %+v", gotNodelets)
	}

	projects := []ProjectRecord{{
		ID:                    "p1",
		Name:                  "Project",
		NodeletIDs:            []string{"1", "2"},
		ExcludedContainerRefs: []string{"1/c1"},
		CreatedAt:             time.UnixMilli(1000),
		UpdatedAt:             time.UnixMilli(2000),
	}}
	if err := store.ReplaceProjects(ctx, projects); err != nil {
		t.Fatalf("ReplaceProjects: %v", err)
	}
	gotProjects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(gotProjects) != 1 || gotProjects[0].NodeletIDs[1] != "2" || gotProjects[0].ExcludedContainerRefs[0] != "1/c1" {
		t.Fatalf("projects = %+v", gotProjects)
	}

	dsns := []DSNRecord{{NodeletID: "1", ContainerID: "c1", Pairs: map[string]string{"host": "127.0.0.1"}}}
	if err := store.ReplaceDSNRecords(ctx, dsns); err != nil {
		t.Fatalf("ReplaceDSNRecords: %v", err)
	}
	gotDSNs, err := store.ListDSNRecords(ctx)
	if err != nil {
		t.Fatalf("ListDSNRecords: %v", err)
	}
	if len(gotDSNs) != 1 || gotDSNs[0].Pairs["host"] != "127.0.0.1" {
		t.Fatalf("dsns = %+v", gotDSNs)
	}
}

func TestRuntimeStoreMCPContainerBindingUnique(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	err = store.ReplaceMCPConnections(ctx, []MCPConnectionRecord{
		{ID: "a", NodeletID: "n1", ContainerID: "c1"},
		{ID: "b", NodeletID: "n1", ContainerID: "c1"},
	})
	if err == nil {
		t.Fatal("expected unique binding error")
	}
}
