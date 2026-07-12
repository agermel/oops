package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oops/internal/llm/skills"
	"oops/internal/nodelet"
)

func TestContainerExecToolBehavior(t *testing.T) {
	tool, err := NewContainerExecTool(&fakeOpsData{
		execResult: nodelet.ExecResult{ExitCode: 7, Stdout: "output", Stderr: "warning\n"},
	})
	if err != nil {
		t.Fatalf("NewContainerExecTool() error = %v", err)
	}

	emptyResult, err := tool.InvokableRun(context.Background(), mustJSON(containerExecInput{
		NodeletID: "node", ContainerID: "container",
	}))
	if err != nil {
		t.Fatalf("InvokableRun(empty command) error = %v", err)
	}
	if emptyResult != "命令不能为空" {
		t.Fatalf("empty command result = %q", emptyResult)
	}

	result, err := tool.InvokableRun(context.Background(), mustJSON(containerExecInput{
		NodeletID: "node", ContainerID: "container", Command: []string{"sh", "-c", "echo output"},
	}))
	if err != nil {
		t.Fatalf("InvokableRun() error = %v", err)
	}
	for _, want := range []string{"退出码: 7", "--- stdout ---", "output\n", "--- stderr ---", "warning\n"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result = %q, want %q", result, want)
		}
	}

	errorTool, err := NewContainerExecTool(&fakeOpsData{err: errors.New("permission denied")})
	if err != nil {
		t.Fatalf("NewContainerExecTool(error) error = %v", err)
	}
	errorResult, err := errorTool.InvokableRun(context.Background(), mustJSON(containerExecInput{
		NodeletID: "node", ContainerID: "container", Command: []string{"id"},
	}))
	if err != nil {
		t.Fatalf("InvokableRun(error) error = %v", err)
	}
	if !strings.Contains(errorResult, "命令执行失败（已重试3次）：permission denied") {
		t.Fatalf("error result = %q", errorResult)
	}
}

func TestFormatExecResultWithoutOutput(t *testing.T) {
	got := formatExecResult(nodelet.ExecResult{ExitCode: 0})
	if got != "退出码: 0\n" {
		t.Fatalf("formatExecResult() = %q", got)
	}
}

func TestSkillToolLoadsEnabledSkillAndReportsUnavailableSkills(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "enabled.md"), "---\nname: inspect\ndescription: inspect a service\nenabled: true\n---\nInspect the service.\n")
	writeTestFile(t, filepath.Join(dir, "disabled.md"), "---\nname: archive\ndescription: archive a service\nenabled: false\n---\nArchive the service.\n")

	store, err := skills.NewSkillStore(dir)
	if err != nil {
		t.Fatalf("NewSkillStore() error = %v", err)
	}
	t.Cleanup(store.Close)

	tool, err := NewSkillTool(store)
	if err != nil {
		t.Fatalf("NewSkillTool() error = %v", err)
	}

	result, err := tool.InvokableRun(context.Background(), mustJSON(skillInput{Name: "inspect"}))
	if err != nil {
		t.Fatalf("InvokableRun(enabled skill) error = %v", err)
	}
	if !strings.Contains(result, "<skill_content name=\"inspect\">") || !strings.Contains(result, "Inspect the service.") {
		t.Fatalf("enabled skill result = %q", result)
	}

	disabledResult, err := tool.InvokableRun(context.Background(), mustJSON(skillInput{Name: "archive"}))
	if err != nil {
		t.Fatalf("InvokableRun(disabled skill) error = %v", err)
	}
	if disabledResult != "Skill \"archive\" is disabled." {
		t.Fatalf("disabled skill result = %q", disabledResult)
	}

	missingResult, err := tool.InvokableRun(context.Background(), mustJSON(skillInput{Name: "missing"}))
	if err != nil {
		t.Fatalf("InvokableRun(missing skill) error = %v", err)
	}
	if !strings.Contains(missingResult, "Skill \"missing\" not found.") || !strings.Contains(missingResult, "inspect") {
		t.Fatalf("missing skill result = %q", missingResult)
	}

	allTools, err := NewTools(&fakeOpsData{repoURL: "https://example.invalid/repository"}, store)
	if err != nil {
		t.Fatalf("NewTools() error = %v", err)
	}
	if len(allTools) != 9 {
		t.Fatalf("len(NewTools()) = %d, want 9", len(allTools))
	}
}

func TestRepoHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := repoDir("project-a")
	if err != nil {
		t.Fatalf("repoDir() error = %v", err)
	}
	if want := filepath.Join(home, ".oops", "repos", "project-a"); dir != want {
		t.Fatalf("repoDir() = %q, want %q", dir, want)
	}

	if err := safePath("src/main.go"); err != nil {
		t.Fatalf("safePath(valid) error = %v", err)
	}
	if err := safePath("src/../secret"); err == nil {
		t.Fatal("safePath(path traversal) error = nil")
	}

	if got := truncateTail("你好世界", 2); got != "...(前段已截断)\n世界" {
		t.Fatalf("truncateTail() = %q", got)
	}
	if got := truncateTail("short", 10); got != "short" {
		t.Fatalf("truncateTail(short) = %q", got)
	}

	if got := displayPath(""); got != "/ (根目录)" {
		t.Fatalf("displayPath(empty) = %q", got)
	}
	if got := displayPath("src"); got != "src" {
		t.Fatalf("displayPath() = %q", got)
	}
	if got := formatSize(1024); !strings.Contains(got, "K") {
		t.Fatalf("formatSize(1024) = %q", got)
	}
	if got := formatSize(1024 * 1024); !strings.Contains(got, "M") {
		t.Fatalf("formatSize(1MiB) = %q", got)
	}
	if got := langFromExt(".go"); got != "Go" {
		t.Fatalf("langFromExt(.go) = %q", got)
	}
	if got := langFromExt(".unknown"); got != "" {
		t.Fatalf("langFromExt(.unknown) = %q", got)
	}
}

func TestRepoListAndReadToolsBehavior(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := repoDir("project-a")
	if err != nil {
		t.Fatalf("repoDir() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0755); err != nil {
		t.Fatalf("MkdirAll(empty) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nested"), 0755); err != nil {
		t.Fatalf("MkdirAll(nested) error = %v", err)
	}
	writeTestFile(t, filepath.Join(dir, "main.go"), "package main\n")
	writeTestFile(t, filepath.Join(dir, "large.txt"), strings.Repeat("界", 8001))

	ops := &fakeOpsData{repoURL: "https://example.invalid/repository"}
	listTool, err := NewRepoListDirTool(ops)
	if err != nil {
		t.Fatalf("NewRepoListDirTool() error = %v", err)
	}

	listResult, err := listTool.InvokableRun(context.Background(), mustJSON(repoListDirInput{ProjectID: "project-a"}))
	if err != nil {
		t.Fatalf("list root error = %v", err)
	}
	for _, want := range []string{"目录: / (根目录)", "main.go", "目录  ", "文件  "} {
		if !strings.Contains(listResult, want) {
			t.Fatalf("list result = %q, want %q", listResult, want)
		}
	}

	emptyResult, err := listTool.InvokableRun(context.Background(), mustJSON(repoListDirInput{ProjectID: "project-a", Path: "empty"}))
	if err != nil {
		t.Fatalf("list empty error = %v", err)
	}
	if emptyResult != "目录 \"empty\" 为空。" {
		t.Fatalf("empty directory result = %q", emptyResult)
	}

	invalidResult, err := listTool.InvokableRun(context.Background(), mustJSON(repoListDirInput{ProjectID: "project-a", Path: "../secret"}))
	if err != nil {
		t.Fatalf("list invalid path error = %v", err)
	}
	if !strings.Contains(invalidResult, "路径无效") {
		t.Fatalf("invalid path result = %q", invalidResult)
	}

	missingResult, err := listTool.InvokableRun(context.Background(), mustJSON(repoListDirInput{ProjectID: "project-a", Path: "missing"}))
	if err != nil {
		t.Fatalf("list missing path error = %v", err)
	}
	if !strings.Contains(missingResult, "目录 \"missing\" 不存在") {
		t.Fatalf("missing directory result = %q", missingResult)
	}

	readTool, err := NewRepoReadFileTool(ops)
	if err != nil {
		t.Fatalf("NewRepoReadFileTool() error = %v", err)
	}

	readResult, err := readTool.InvokableRun(context.Background(), mustJSON(repoReadFileInput{ProjectID: "project-a", FilePath: "main.go"}))
	if err != nil {
		t.Fatalf("read Go file error = %v", err)
	}
	for _, want := range []string{"文件: main.go (Go)", "package main"} {
		if !strings.Contains(readResult, want) {
			t.Fatalf("read result = %q, want %q", readResult, want)
		}
	}

	largeResult, err := readTool.InvokableRun(context.Background(), mustJSON(repoReadFileInput{ProjectID: "project-a", FilePath: "large.txt"}))
	if err != nil {
		t.Fatalf("read large file error = %v", err)
	}
	if !strings.Contains(largeResult, "...(前段已截断)") {
		t.Fatalf("large file result was not truncated")
	}

	readInvalidResult, err := readTool.InvokableRun(context.Background(), mustJSON(repoReadFileInput{ProjectID: "project-a", FilePath: "../secret"}))
	if err != nil {
		t.Fatalf("read invalid path error = %v", err)
	}
	if !strings.Contains(readInvalidResult, "路径无效") {
		t.Fatalf("invalid read result = %q", readInvalidResult)
	}

	readMissingResult, err := readTool.InvokableRun(context.Background(), mustJSON(repoReadFileInput{ProjectID: "project-a", FilePath: "missing.txt"}))
	if err != nil {
		t.Fatalf("read missing file error = %v", err)
	}
	if !strings.Contains(readMissingResult, "文件 \"missing.txt\" 不存在") {
		t.Fatalf("missing file result = %q", readMissingResult)
	}

	readDirectoryResult, err := readTool.InvokableRun(context.Background(), mustJSON(repoReadFileInput{ProjectID: "project-a", FilePath: "nested"}))
	if err != nil {
		t.Fatalf("read directory error = %v", err)
	}
	if !strings.Contains(readDirectoryResult, "读取文件失败") {
		t.Fatalf("directory read result = %q", readDirectoryResult)
	}

	missingRepoTool, err := NewRepoReadFileTool(&fakeOpsData{})
	if err != nil {
		t.Fatalf("NewRepoReadFileTool(missing repo) error = %v", err)
	}
	missingRepoResult, err := missingRepoTool.InvokableRun(context.Background(), mustJSON(repoReadFileInput{ProjectID: "project-a", FilePath: "main.go"}))
	if err != nil {
		t.Fatalf("read missing repo error = %v", err)
	}
	if !strings.Contains(missingRepoResult, "获取仓库地址失败") {
		t.Fatalf("missing repo result = %q", missingRepoResult)
	}
}

func TestRepoFetchToolBehavior(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/text":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("hello from server"))
		case "/large":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(strings.Repeat("x", 50001)))
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte{0, 1, 2})
		}
	}))
	t.Cleanup(server.Close)

	tool, err := NewRepoFetchTool(nil)
	if err != nil {
		t.Fatalf("NewRepoFetchTool() error = %v", err)
	}

	emptyResult, err := tool.InvokableRun(context.Background(), mustJSON(repoFetchInput{URL: "  "}))
	if err != nil {
		t.Fatalf("fetch empty URL error = %v", err)
	}
	if emptyResult != "URL 不能为空" {
		t.Fatalf("empty URL result = %q", emptyResult)
	}

	invalidResult, err := tool.InvokableRun(context.Background(), mustJSON(repoFetchInput{URL: "http://[::1"}))
	if err != nil {
		t.Fatalf("fetch invalid URL error = %v", err)
	}
	if !strings.Contains(invalidResult, "URL 无效") {
		t.Fatalf("invalid URL result = %q", invalidResult)
	}

	textResult, err := tool.InvokableRun(context.Background(), mustJSON(repoFetchInput{URL: server.URL + "/text"}))
	if err != nil {
		t.Fatalf("fetch text error = %v", err)
	}
	for _, want := range []string{"HTTP 200", "Content-Type: text/plain", "hello from server"} {
		if !strings.Contains(textResult, want) {
			t.Fatalf("text result = %q, want %q", textResult, want)
		}
	}

	binaryResult, err := tool.InvokableRun(context.Background(), mustJSON(repoFetchInput{URL: server.URL + "/binary"}))
	if err != nil {
		t.Fatalf("fetch binary error = %v", err)
	}
	if !strings.Contains(binaryResult, "(非文本响应，共 3 字节)") {
		t.Fatalf("binary result = %q", binaryResult)
	}

	largeResult, err := tool.InvokableRun(context.Background(), mustJSON(repoFetchInput{URL: server.URL + "/large"}))
	if err != nil {
		t.Fatalf("fetch large text error = %v", err)
	}
	if !strings.Contains(largeResult, "...(前段已截断)") {
		t.Fatalf("large text result was not truncated")
	}
}

func TestRepoSyncToolClonesFetchesAndPulls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := filepath.Join(t.TempDir(), "source")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "tests@example.invalid")
	runGit(t, source, "config", "user.name", "Tools Tests")
	writeTestFile(t, filepath.Join(source, "README.md"), "first\n")
	runGit(t, source, "add", "README.md")
	runGit(t, source, "commit", "-m", "initial")

	tool, err := NewRepoSyncTool(&fakeOpsData{repoURL: source})
	if err != nil {
		t.Fatalf("NewRepoSyncTool() error = %v", err)
	}

	cloneResult, err := tool.InvokableRun(context.Background(), mustJSON(repoSyncInput{ProjectID: "project-a"}))
	if err != nil {
		t.Fatalf("clone sync error = %v", err)
	}
	if !strings.Contains(cloneResult, "状态: 已是最新") {
		t.Fatalf("clone result = %q", cloneResult)
	}

	writeTestFile(t, filepath.Join(source, "README.md"), "second\n")
	runGit(t, source, "add", "README.md")
	runGit(t, source, "commit", "-m", "update")

	behindResult, err := tool.InvokableRun(context.Background(), mustJSON(repoSyncInput{ProjectID: "project-a"}))
	if err != nil {
		t.Fatalf("fetch sync error = %v", err)
	}
	if !strings.Contains(behindResult, "状态: 落后远端 1 个 commit") {
		t.Fatalf("behind result = %q", behindResult)
	}

	pullResult, err := tool.InvokableRun(context.Background(), mustJSON(repoSyncInput{ProjectID: "project-a", Pull: true}))
	if err != nil {
		t.Fatalf("pull sync error = %v", err)
	}
	if !strings.Contains(pullResult, "状态: 已是最新") {
		t.Fatalf("pull result = %q", pullResult)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}
