package mcp

import (
	"path/filepath"
	"testing"

	runtimestore "oops/internal/store/runtime"
)

func TestManagerRuntimeLoadsSQLiteConnections(t *testing.T) {
	dir := t.TempDir()

	runtime, err := runtimestore.Open(filepath.Join(dir, "runtime.db"))
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	defer runtime.Close()
	if err := runtime.ReplaceMCPConnections(t.Context(), []runtimestore.MCPConnectionRecord{
		{
			ID:          "mysql-1",
			Name:        "mysql",
			Type:        "mysql",
			Transport:   "stdio",
			Command:     "mysql-mcp-server",
			Args:        []string{"--flag"},
			Env:         []string{"MYSQL_DSN=x"},
			Enabled:     false,
			ContainerID: "c1",
			NodeletID:   "n1",
		},
	}); err != nil {
		t.Fatalf("seed runtime mcp connection: %v", err)
	}

	manager, err := NewManagerWithRuntime(runtime, nil)
	if err != nil {
		t.Fatalf("NewManagerWithRuntime: %v", err)
	}
	list := manager.List()
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if list[0].ID != "mysql-1" || list[0].Args[0] != "--flag" || list[0].Env[0] != "MYSQL_DSN=x" {
		t.Fatalf("imported connection = %+v", list[0])
	}
	if list[0].Status != "stopped" {
		t.Fatalf("status = %q, want stopped", list[0].Status)
	}
}
