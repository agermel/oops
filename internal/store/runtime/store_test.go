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

func TestRuntimeStoreProjectAndDSNCRUD(t *testing.T) {
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

	late := ProjectRecord{
		ID:                    "p1",
		Name:                  "Project",
		NodeletIDs:            []string{"2", "1"},
		ExcludedContainerRefs: []string{"1/c2", "1/c1"},
		CreatedAt:             time.UnixMilli(2000),
		UpdatedAt:             time.UnixMilli(2000),
	}
	early := ProjectRecord{ID: "p0", Name: "Earlier", CreatedAt: time.UnixMilli(1000), UpdatedAt: time.UnixMilli(1000)}
	if err := store.CreateProject(ctx, late); err != nil {
		t.Fatalf("CreateProject late: %v", err)
	}
	if err := store.CreateProject(ctx, early); err != nil {
		t.Fatalf("CreateProject early: %v", err)
	}
	gotProjects, err := store.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(gotProjects) != 2 || gotProjects[0].ID != "p0" || gotProjects[1].NodeletIDs[1] != "1" || gotProjects[1].ExcludedContainerRefs[0] != "1/c1" {
		t.Fatalf("projects = %+v", gotProjects)
	}
	late.Name = "Project Updated"
	late.NodeletIDs = []string{"3"}
	if err := store.UpdateProject(ctx, late); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	if got, err := store.GetProject(ctx, "p1"); err != nil || got == nil || got.Name != "Project Updated" || len(got.NodeletIDs) != 1 || got.NodeletIDs[0] != "3" {
		t.Fatalf("GetProject = %+v, %v", got, err)
	}

	if err := store.SetDSNRecord(ctx, DSNRecord{NodeletID: "1", ContainerID: "c1", Pairs: map[string]string{"port": "6379", "host": "127.0.0.1"}}); err != nil {
		t.Fatalf("SetDSNRecord c1: %v", err)
	}
	if err := store.SetDSNRecord(ctx, DSNRecord{NodeletID: "1", ContainerID: "c2", Pairs: map[string]string{"host": "127.0.0.2"}}); err != nil {
		t.Fatalf("SetDSNRecord c2: %v", err)
	}
	if err := store.UpsertDSNEntry(ctx, "1", "c1", "user", "root"); err != nil {
		t.Fatalf("UpsertDSNEntry: %v", err)
	}
	if err := store.DeleteDSNEntry(ctx, "1", "c1", "port"); err != nil {
		t.Fatalf("DeleteDSNEntry: %v", err)
	}
	gotDSNs, err := store.ListDSNRecords(ctx)
	if err != nil {
		t.Fatalf("ListDSNRecords: %v", err)
	}
	if len(gotDSNs) != 2 || gotDSNs[0].ContainerID != "c1" || gotDSNs[0].Pairs["host"] != "127.0.0.1" || gotDSNs[0].Pairs["user"] != "root" {
		t.Fatalf("dsns = %+v", gotDSNs)
	}
}

func TestRuntimeStoreProjectUpdateRollsBack(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	original := ProjectRecord{
		ID:         "p1",
		Name:       "Original",
		NodeletIDs: []string{"nodelet-1"},
		CreatedAt:  time.UnixMilli(1000),
		UpdatedAt:  time.UnixMilli(1000),
	}
	if err := store.CreateProject(ctx, original); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_project_nodelet
		BEFORE INSERT ON project_nodelets
		WHEN NEW.nodelet_id = 'reject'
		BEGIN
			SELECT RAISE(ABORT, 'reject nodelet');
		END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}

	updated := original
	updated.Name = "Changed"
	updated.NodeletIDs = []string{"reject"}
	updated.UpdatedAt = time.UnixMilli(2000)
	if err := store.UpdateProject(ctx, updated); err == nil {
		t.Fatal("expected UpdateProject error")
	}
	got, err := store.GetProject(ctx, "p1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.Name != "Original" || len(got.NodeletIDs) != 1 || got.NodeletIDs[0] != "nodelet-1" {
		t.Fatalf("transaction leaked partial update: %+v", got)
	}
}

func TestRuntimeStoreDSNReplaceRollsBack(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := store.SetDSNRecord(ctx, DSNRecord{
		NodeletID:   "nodelet",
		ContainerID: "container",
		Pairs:       map[string]string{"host": "old"},
	}); err != nil {
		t.Fatalf("SetDSNRecord: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_dsn_entry
		BEFORE INSERT ON container_dsn_entries
		WHEN NEW.key = 'reject'
		BEGIN
			SELECT RAISE(ABORT, 'reject dsn');
		END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	if err := store.SetDSNRecord(ctx, DSNRecord{
		NodeletID:   "nodelet",
		ContainerID: "container",
		Pairs:       map[string]string{"host": "changed", "reject": "value"},
	}); err == nil {
		t.Fatal("expected SetDSNRecord error")
	}
	got, err := store.GetDSNRecord(ctx, "nodelet", "container")
	if err != nil {
		t.Fatalf("GetDSNRecord: %v", err)
	}
	if got == nil || len(got.Pairs) != 1 || got.Pairs["host"] != "old" {
		t.Fatalf("transaction leaked partial update: %+v", got)
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

func TestRuntimeStoreAgentSettingsDefaultAndPersist(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	got, err := store.GetAgentSettings(ctx)
	if err != nil {
		t.Fatalf("GetAgentSettings default: %v", err)
	}
	if got.MaxTurns != DefaultAgentMaxTurns {
		t.Fatalf("default max turns = %d, want %d", got.MaxTurns, DefaultAgentMaxTurns)
	}

	updatedAt := time.UnixMilli(3000)
	if err := store.UpdateAgentSettings(ctx, AgentSettingsRecord{MaxTurns: 7, UpdatedAt: updatedAt}); err != nil {
		t.Fatalf("UpdateAgentSettings: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	got, err = reopened.GetAgentSettings(ctx)
	if err != nil {
		t.Fatalf("GetAgentSettings persisted: %v", err)
	}
	if got.MaxTurns != 7 {
		t.Fatalf("max turns = %d, want 7", got.MaxTurns)
	}
	if !got.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("updated at = %s, want %s", got.UpdatedAt, updatedAt)
	}
}

func TestRuntimeStoreAgentSettingsRejectsInvalidMaxTurns(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	for _, maxTurns := range []int{0, -1, AgentMaxTurnsMax + 1} {
		if err := store.UpdateAgentSettings(ctx, AgentSettingsRecord{MaxTurns: maxTurns}); err == nil {
			t.Fatalf("UpdateAgentSettings(%d) succeeded, want error", maxTurns)
		}
	}
}
