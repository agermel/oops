package api

import "testing"

// TestSplitNodeletResourcePathContainers 验证中心端容器列表路径解析。
func TestSplitNodeletResourcePathContainers(t *testing.T) {
	nodeletID, containerID, action, ok := splitNodeletResourcePath("/api/nodelets/local/containers")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if nodeletID != "local" {
		t.Fatalf("nodeletID = %q, want %q", nodeletID, "local")
	}
	if containerID != "" {
		t.Fatalf("containerID = %q, want empty", containerID)
	}
	if action != "containers" {
		t.Fatalf("action = %q, want %q", action, "containers")
	}
}

// TestSplitNodeletResourcePathLogs 验证中心端容器日志路径解析。
func TestSplitNodeletResourcePathLogs(t *testing.T) {
	nodeletID, containerID, action, ok := splitNodeletResourcePath("/api/nodelets/local/containers/container-1/logs")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if nodeletID != "local" {
		t.Fatalf("nodeletID = %q, want %q", nodeletID, "local")
	}
	if containerID != "container-1" {
		t.Fatalf("containerID = %q, want %q", containerID, "container-1")
	}
	if action != "logs" {
		t.Fatalf("action = %q, want %q", action, "logs")
	}
}
