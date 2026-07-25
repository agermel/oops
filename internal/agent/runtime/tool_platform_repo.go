package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ---------- 仓库工具输入类型 ----------

type repoSyncInput struct {
	ProjectID string `json:"project_id" jsonschema:"required,description=项目 ID"`
	Pull      bool   `json:"pull,omitempty" jsonschema:"description=是否拉取最新代码（默认 false，仅检查更新）"`
}

type repoListDirInput struct {
	ProjectID string `json:"project_id" jsonschema:"required,description=项目 ID"`
	Path      string `json:"path,omitempty" jsonschema:"description=仓库内的相对目录路径，默认为根目录"`
}

type repoReadFileInput struct {
	ProjectID string `json:"project_id" jsonschema:"required,description=项目 ID"`
	FilePath  string `json:"file_path" jsonschema:"required,description=仓库内的文件路径，如 docker-compose.yml 或 src/main.go"`
}

type repoFetchInput struct {
	URL string `json:"url" jsonschema:"required,description=要抓取的 URL（GitHub API / raw 文件 / 文档等）"`
}

// ---------- 辅助函数 ----------

// repoDir 返回项目的本地 clone 目录。
func repoDir(projectID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法获取用户目录: %w", err)
	}
	return filepath.Join(home, ".oops", "repos", projectID), nil
}

// safePath 校验路径不包含 ".."，防止路径穿越。
func safePath(p string) error {
	if strings.Contains(p, "..") {
		return fmt.Errorf("路径不允许包含 '..'")
	}
	return nil
}

// truncateTail 保留文本的最后 maxChars 个字符。
func truncateRepoTail(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return "...(前段已截断)\n" + string(runes[len(runes)-maxChars:])
}

// gitCmd 在 repo 目录中执行 git 命令，返回合并的 stdout+stderr。
func gitCmd(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------- repo_sync 工具 ----------

// NewRepoSyncTool 创建 repo_sync 工具——克隆/同步项目仓库并检测更新。
func NewRepoSyncTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("repo_sync",
		"同步项目的 GitHub 仓库到本地。首次调用时 clone，后续调用时 fetch 并检测远端是否有更新。\n"+
			"返回 clone 路径、当前分支、HEAD SHA、是否最新、落后远端 commit 数。\n"+
			"设置 pull=true 可拉取最新代码（仅 fast-forward）。",
		func(ctx context.Context, input *repoSyncInput) (string, error) {
			repoURL, err := ops.GetProjectRepo(ctx, input.ProjectID)
			if err != nil {
				return fmt.Sprintf("获取仓库地址失败: %v", err), nil
			}

			dir, err := repoDir(input.ProjectID)
			if err != nil {
				return fmt.Sprintf("获取本地目录失败: %v", err), nil
			}

			// 检查是否已 clone。
			gitDir := filepath.Join(dir, ".git")
			if _, err := os.Stat(gitDir); os.IsNotExist(err) {
				// 首次 clone。
				if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
					return fmt.Sprintf("创建目录失败: %v", err), nil
				}
				cloneCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
				defer cancel()
				cmd := exec.CommandContext(cloneCtx, "git", "clone", repoURL, dir)
				out, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Sprintf("clone 失败: %v\n%s", err, string(out)), nil
				}
			} else {
				// 已存在，先 fetch。
				fetchCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
				defer cancel()
				if _, err := gitCmd(fetchCtx, dir, "fetch", "origin"); err != nil {
					return fmt.Sprintf("fetch 失败: %v", err), nil
				}

				// 如果要求 pull。
				if input.Pull {
					pullCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
					defer cancel()
					out, err := gitCmd(pullCtx, dir, "pull", "--ff-only")
					if err != nil {
						return fmt.Sprintf("pull 失败: %v\n%s", err, out), nil
					}
				}
			}

			// 检查是否最新。
			branch, _ := gitCmd(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
			head, _ := gitCmd(ctx, dir, "rev-parse", "--short", "HEAD")

			// 检测落后远端 commit 数。
			behind := -1
			behindOut, err := gitCmd(ctx, dir, "rev-list", "HEAD..@{u}", "--count")
			if err == nil {
				fmt.Sscanf(behindOut, "%d", &behind)
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("仓库路径: %s\n", dir))
			sb.WriteString(fmt.Sprintf("分支: %s\n", branch))
			sb.WriteString(fmt.Sprintf("HEAD: %s\n", head))
			if behind == 0 {
				sb.WriteString("状态: 已是最新\n")
			} else if behind > 0 {
				sb.WriteString(fmt.Sprintf("状态: 落后远端 %d 个 commit，使用 pull=true 拉取\n", behind))
			} else {
				sb.WriteString("状态: 无法检测远端（可能需要先 push 当前分支或设置 upstream）\n")
			}
			return sb.String(), nil
		})
}

// ---------- repo_list_dir 工具 ----------

// NewRepoListDirTool 创建 repo_list_dir 工具——列出仓库目录内容。
func NewRepoListDirTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("repo_list_dir",
		"列出项目仓库中指定目录的内容。返回文件名、大小、类型（文件/目录）和修改时间。\n"+
			"最多返回 200 条。使用 repo_sync 确保仓库是最新的。",
		func(ctx context.Context, input *repoListDirInput) (string, error) {
			if _, err := ops.GetProjectRepo(ctx, input.ProjectID); err != nil {
				return fmt.Sprintf("获取仓库地址失败: %v", err), nil
			}

			dir, err := repoDir(input.ProjectID)
			if err != nil {
				return fmt.Sprintf("获取本地目录失败: %v", err), nil
			}

			targetPath := input.Path
			if err := safePath(targetPath); err != nil {
				return fmt.Sprintf("路径无效: %v", err), nil
			}

			fullPath := filepath.Join(dir, targetPath)
			entries, err := os.ReadDir(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Sprintf("目录 %q 不存在。请先使用 repo_sync 同步仓库。", targetPath), nil
				}
				return fmt.Sprintf("读取目录失败: %v", err), nil
			}

			if len(entries) == 0 {
				return fmt.Sprintf("目录 %q 为空。", targetPath), nil
			}

			if len(entries) > 200 {
				entries = entries[:200]
			}

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("目录: %s (共 %d 项)\n", displayPath(targetPath), len(entries)))
			for _, e := range entries {
				info, _ := e.Info()
				size := int64(0)
				modTime := ""
				if info != nil {
					size = info.Size()
					modTime = info.ModTime().Format("2006-01-02 15:04")
				}
				typ := "文件"
				if e.IsDir() {
					typ = "目录"
				}
				sb.WriteString(fmt.Sprintf("  %s  %s  %s  %s\n",
					typ, formatSize(size), modTime, e.Name()))
			}
			return sb.String(), nil
		})
}

// ---------- repo_read_file 工具 ----------

// NewRepoReadFileTool 创建 repo_read_file 工具——读取仓库内文件内容。
func NewRepoReadFileTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("repo_read_file",
		"读取项目仓库中指定文件的内容。超长文件（>8000 字符）会截断保留尾部。\n"+
			"返回时标注文件语言。使用 repo_sync 确保仓库是最新的。",
		func(ctx context.Context, input *repoReadFileInput) (string, error) {
			if _, err := ops.GetProjectRepo(ctx, input.ProjectID); err != nil {
				return fmt.Sprintf("获取仓库地址失败: %v", err), nil
			}

			dir, err := repoDir(input.ProjectID)
			if err != nil {
				return fmt.Sprintf("获取本地目录失败: %v", err), nil
			}

			if err := safePath(input.FilePath); err != nil {
				return fmt.Sprintf("路径无效: %v", err), nil
			}

			fullPath := filepath.Join(dir, input.FilePath)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Sprintf("文件 %q 不存在。请先使用 repo_sync 同步仓库，或使用 repo_list_dir 查看文件列表。", input.FilePath), nil
				}
				return fmt.Sprintf("读取文件失败: %v", err), nil
			}

			content := string(data)
			lang := langFromExt(filepath.Ext(input.FilePath))
			maxChars := 8000

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("文件: %s", input.FilePath))
			if lang != "" {
				sb.WriteString(fmt.Sprintf(" (%s)", lang))
			}
			sb.WriteString(fmt.Sprintf("\n大小: %s\n---\n", formatSize(int64(len(data)))))

			runes := []rune(content)
			if len(runes) > maxChars {
				sb.WriteString(truncateRepoTail(content, maxChars))
			} else {
				sb.WriteString(content)
			}
			return sb.String(), nil
		})
}

// ---------- repo_fetch 工具 ----------

// NewRepoFetchTool 创建 repo_fetch 工具——联网抓取 URL 内容。
func NewRepoFetchTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("repo_fetch",
		"联网抓取 URL 的内容。可用于查询 GitHub API、读取远端文件、查看文档等。\n"+
			"返回文本内容（最多 50000 字符）。非文本响应会注明。\n"+
			"注意 GitHub API 未认证时有频率限制（60 次/小时）。",
		func(ctx context.Context, input *repoFetchInput) (string, error) {
			rawURL := strings.TrimSpace(input.URL)
			if rawURL == "" {
				return "URL 不能为空", nil
			}

			// 校验 URL。
			if _, err := url.Parse(rawURL); err != nil {
				return fmt.Sprintf("URL 无效: %v", err), nil
			}

			fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(fetchCtx, "GET", rawURL, nil)
			if err != nil {
				return fmt.Sprintf("创建请求失败: %v", err), nil
			}
			req.Header.Set("User-Agent", "oops/1.0")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Sprintf("请求失败: %v", err), nil
			}
			defer resp.Body.Close()

			// 限制读取大小。
			limited := io.LimitReader(resp.Body, 1<<20) // 1MB
			data, err := io.ReadAll(limited)
			if err != nil {
				return fmt.Sprintf("读取响应失败: %v", err), nil
			}

			contentType := resp.Header.Get("Content-Type")
			isText := strings.Contains(contentType, "text/") ||
				strings.Contains(contentType, "application/json") ||
				strings.Contains(contentType, "application/xml") ||
				strings.Contains(contentType, "application/javascript")

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("HTTP %d\n", resp.StatusCode))
			sb.WriteString(fmt.Sprintf("Content-Type: %s\n", contentType))
			sb.WriteString(fmt.Sprintf("Content-Length: %d\n---\n", len(data)))

			if !isText {
				sb.WriteString(fmt.Sprintf("(非文本响应，共 %d 字节)", len(data)))
				return sb.String(), nil
			}

			content := string(data)
			maxChars := 50000
			runes := []rune(content)
			if len(runes) > maxChars {
				sb.WriteString(truncateRepoTail(content, maxChars))
			} else {
				sb.WriteString(content)
			}
			return sb.String(), nil
		})
}

// ---------- 显示辅助 ----------

func displayPath(p string) string {
	if p == "" || p == "." {
		return "/ (根目录)"
	}
	return p
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%4d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%4.1f %c", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// langFromExt 根据文件扩展名返回语言提示。
func langFromExt(ext string) string {
	m := map[string]string{
		".go":         "Go",
		".ts":         "TypeScript",
		".tsx":        "TypeScript+React",
		".js":         "JavaScript",
		".jsx":        "JavaScript+React",
		".py":         "Python",
		".rs":         "Rust",
		".java":       "Java",
		".c":          "C",
		".cpp":        "C++",
		".h":          "C/C++ Header",
		".yaml":       "YAML",
		".yml":        "YAML",
		".json":       "JSON",
		".xml":        "XML",
		".md":         "Markdown",
		".sql":        "SQL",
		".sh":         "Shell",
		".bash":       "Bash",
		".html":       "HTML",
		".css":        "CSS",
		".toml":       "TOML",
		".proto":      "Protobuf",
		".tf":         "Terraform",
		".dockerfile": "Dockerfile",
		".makefile":   "Makefile",
	}
	if lang, ok := m[ext]; ok {
		return lang
	}
	return ""
}
