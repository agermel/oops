package api

import "testing"

// TestSplitAgentResourcePathContainers 验证中心端容器列表路径解析。
func TestSplitAgentResourcePathContainers(t *testing.T) {
	agentID, containerID, action, ok := splitAgentResourcePath("/api/agents/local/containers")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if agentID != "local" {
		t.Fatalf("agentID = %q, want %q", agentID, "local")
	}
	if containerID != "" {
		t.Fatalf("containerID = %q, want empty", containerID)
	}
	if action != "containers" {
		t.Fatalf("action = %q, want %q", action, "containers")
	}
}

// TestSplitAgentResourcePathLogs 验证中心端容器日志路径解析。
func TestSplitAgentResourcePathLogs(t *testing.T) {
	agentID, containerID, action, ok := splitAgentResourcePath("/api/agents/local/containers/container-1/logs")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if agentID != "local" {
		t.Fatalf("agentID = %q, want %q", agentID, "local")
	}
	if containerID != "container-1" {
		t.Fatalf("containerID = %q, want %q", containerID, "container-1")
	}
	if action != "logs" {
		t.Fatalf("action = %q, want %q", action, "logs")
	}
}
