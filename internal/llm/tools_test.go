package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"oops/internal/nodelet"
)

// fakeOpsData 是 OpsData 的测试用假实现。
type fakeOpsData struct {
	nodelets    []NodeletSummary
	containers  []nodelet.Container
	logs        []nodelet.LogEntry
	repoURL     string
	execResult  nodelet.ExecResult
	err         error
}

func (f *fakeOpsData) ListNodelets(_ context.Context) ([]NodeletSummary, error) {
	return f.nodelets, f.err
}

func (f *fakeOpsData) ListContainers(_ context.Context, _ string, _ string, _ string) ([]nodelet.Container, error) {
	return f.containers, f.err
}

func (f *fakeOpsData) GetLogs(_ context.Context, _, _ string, _ int) ([]nodelet.LogEntry, error) {
	return f.logs, f.err
}

func (f *fakeOpsData) GetProjectRepo(_ context.Context, projectID string) (string, error) {
	if f.repoURL == "" {
		return "", errors.New("no github repo configured")
	}
	return f.repoURL, nil
}

func (f *fakeOpsData) ContainerExec(_ context.Context, _, _ string, _ []string) (nodelet.ExecResult, error) {
	return f.execResult, f.err
}

// mustJSON 将 v 序列化为 JSON 字符串，失败时 panic。
func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// TestNewTools 验证所有工具全部创建成功。
func TestNewTools(t *testing.T) {
	ops := &fakeOpsData{repoURL: "https://github.com/test/repo"}
	tools, err := NewTools(ops, nil)
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}
	if len(tools) != 8 {
		t.Fatalf("len(tools) = %d, want 8", len(tools))
	}
	expected := []string{"list_nodelets", "list_containers", "get_logs", "container_exec",
		"repo_sync", "repo_list_dir", "repo_read_file", "repo_fetch"}
	for i, want := range expected {
		info, err := tools[i].Info(context.Background())
		if err != nil {
			t.Fatalf("Info() error = %v", err)
		}
		if info.Name != want {
			t.Fatalf("tools[%d] = %q, want %q", i, info.Name, want)
		}
	}
}

// TestListNodeletsTool 验证机器列表工具。
func TestListNodeletsTool(t *testing.T) {
	ops := &fakeOpsData{
		nodelets: []NodeletSummary{
			{ID: "local", Name: "本机", Available: true},
		},
	}
	tool, err := NewListNodeletsTool(ops)
	if err != nil {
		t.Fatalf("NewListNodeletsTool() error = %v", err)
	}
	result, err := tool.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(result, "本机") {
		t.Fatalf("result = %s, want '本机'", result)
	}
}

// TestListContainersTool 验证容器列表工具。
func TestListContainersTool(t *testing.T) {
	ops := &fakeOpsData{
		containers: []nodelet.Container{
			{ID: "c1", Name: "api", Image: "api:latest", State: "running"},
		},
	}
	tool, err := NewListContainersTool(ops)
	if err != nil {
		t.Fatalf("NewListContainersTool() error = %v", err)
	}
	result, err := tool.InvokableRun(context.Background(), mustJSON(listContainersInput{NodeletID: "local"}))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(result, "api") {
		t.Fatalf("result = %s, want 'api'", result)
	}
}

// TestListContainersToolError 验证容器列表工具在失败时返回消息而不是错误。
func TestListContainersToolError(t *testing.T) {
	ops := &fakeOpsData{err: errors.New("nodelet unreachable")}
	tool, err := NewListContainersTool(ops)
	if err != nil {
		t.Fatalf("NewListContainersTool() error = %v", err)
	}
	result, err := tool.InvokableRun(context.Background(), mustJSON(listContainersInput{NodeletID: "local"}))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(result, "nodelet unreachable") {
		t.Fatalf("result = %s, want error message", result)
	}
}

// TestGetLogsTool 验证日志工具 tail 默认值。
func TestGetLogsTool(t *testing.T) {
	ops := &fakeOpsData{}
	tool, err := NewGetLogsTool(ops)
	if err != nil {
		t.Fatalf("NewGetLogsTool() error = %v", err)
	}
	result, err := tool.InvokableRun(context.Background(), mustJSON(getLogsInput{
		NodeletID: "local", ContainerID: "c1", Tail: 0,
	}))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	if !strings.Contains(result, "没有日志") {
		t.Fatalf("result = %s, want empty message", result)
	}
}

