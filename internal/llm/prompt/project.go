package prompt

import (
	"fmt"
	"strings"
)

// ProjectContext 包含注入 system prompt 的项目元数据。
type ProjectContext struct {
	ID          string
	Name        string
	Description string
	GitHubRepo  string
	NodeletIDs  []string
}

// FormatProjectContext 将项目元数据格式化为 system prompt 中的中文段落。
func FormatProjectContext(p *ProjectContext) string {
	var b strings.Builder
	b.WriteString("## 当前项目\n")
	fmt.Fprintf(&b, "- ID: %s\n", p.ID)
	fmt.Fprintf(&b, "- 名称: %s\n", p.Name)
	if p.Description != "" {
		fmt.Fprintf(&b, "- 描述: %s\n", p.Description)
	}
	if p.GitHubRepo != "" {
		fmt.Fprintf(&b, "- GitHub 仓库: %s\n", p.GitHubRepo)
		b.WriteString("- 提示: 可使用 repo_sync、repo_list_dir、repo_read_file 工具检查仓库源码，使用 repo_fetch 抓取网页或 API\n")
	}
	if len(p.NodeletIDs) > 0 {
		fmt.Fprintf(&b, "- 关联服务器: %s\n", strings.Join(p.NodeletIDs, ", "))
	}
	return b.String()
}
