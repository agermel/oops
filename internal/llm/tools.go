package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oops/internal/nodelet"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// OpsData 是 LLM 工具读取运维数据的抽象接口。
// 由 api.Server 实现，避免 llm 包直接依赖 api 包。
type OpsData interface {
	// ListNodelets 返回所有 Nodelet 的概要信息。
	ListNodelets(ctx context.Context) ([]NodeletSummary, error)

	// ListContainers 返回指定 Nodelet 上的容器列表。status 为空时返回全部。
	ListContainers(ctx context.Context, nodeletID, status string) ([]nodelet.Container, error)

	// GetLogs 返回指定容器的历史日志。
	GetLogs(ctx context.Context, nodeletID, containerID string, tail int) ([]nodelet.LogEntry, error)

	// GetProjectRepo 返回指定项目的 GitHub 仓库 URL。
	// projectID 不存在或未配置仓库时返回空字符串和错误。
	GetProjectRepo(ctx context.Context, projectID string) (string, error)
}

// NodeletSummary 是 LLM 可见的 Nodelet 概要。
type NodeletSummary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Available     bool   `json:"available"`
	DockerVersion string `json:"dockerVersion"`
	Runtime       string `json:"runtime"`
	NCPU          int    `json:"nCPU"`
	MemTotal      int64  `json:"memTotal"`
	Error         string `json:"error,omitempty"`
}

// ---------- 工具输入类型 ----------

type listNodeletsInput struct{}

type listContainersInput struct {
	NodeletID string `json:"nodelet_id" jsonschema:"required,description=要查询的 Nodelet ID"`
	Status    string `json:"status,omitempty" jsonschema:"description=按状态过滤（running | stopped | restarting 等），不传返回全部。建议先查 running 缩小范围"`
}

type getLogsInput struct {
	NodeletID   string `json:"nodelet_id" jsonschema:"required,description=目标 Nodelet ID"`
	ContainerID string `json:"container_id" jsonschema:"required,description=目标容器 ID"`
	Tail        int    `json:"tail" jsonschema:"description=返回最近的日志行数,default=50"`
}

// ---------- 单个工具构造函数 ----------

// NewListNodeletsTool 创建 list_nodelets 工具——列出所有受监控的机器及其主机信息。
func NewListNodeletsTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("list_nodelets", ""+
		"列出所有已配置的 Nodelet（受监控的机器）。"+
		"返回每台机器的 ID、名称、地址、可用状态、Docker 版本、运行时、CPU 数量和内存总量。",
		func(ctx context.Context, _ *listNodeletsInput) (string, error) {
			var items []NodeletSummary
			err := retryOpsCall(ctx, DefaultRetryPolicy, func() error {
				var callErr error
				items, callErr = ops.ListNodelets(ctx)
				return callErr
			})
			if err != nil {
				return formatRetryError("机器列表查询失败", err, DefaultRetryPolicy.MaxRetries), nil
			}
			return formatItems(items), nil
		})
}

// NewListContainersTool 创建 list_containers 工具——列出指定机器上的容器。
func NewListContainersTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("list_containers", ""+
		"列出指定 Nodelet 上的 Docker 容器。"+
		"返回容器 ID、名称、镜像、状态（running/stopped 等）、健康状态和创建时间。"+
		"建议通过 status 参数先过滤 running 容器缩小范围。"+
		"最多返回 200 条记录。",
		func(ctx context.Context, input *listContainersInput) (string, error) {
			var containers []nodelet.Container
			err := retryOpsCall(ctx, DefaultRetryPolicy, func() error {
				var callErr error
				containers, callErr = ops.ListContainers(ctx, input.NodeletID, input.Status)
				return callErr
			})
			if err != nil {
				return formatRetryError("查询失败", err, DefaultRetryPolicy.MaxRetries), nil
			}
			if len(containers) == 0 {
				if input.Status != "" {
					return fmt.Sprintf("该 Nodelet 上没有状态为 %q 的容器。", input.Status), nil
				}
				return "该 Nodelet 上没有容器。", nil
			}
			if len(containers) > 200 {
				containers = containers[:200]
			}
			return formatItems(containers), nil
		})
}

// NewGetLogsTool 创建 get_logs 工具——获取指定容器的历史日志。
func NewGetLogsTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("get_logs", ""+
		"获取指定容器最近的历史日志。"+
		"返回日志时间戳、输出流（stdout/stderr）、日志消息和日志级别。"+
		"tail 参数控制返回行数（默认 50），建议从 20 开始，需要更多细节时再增大。"+
		"最多返回 500 条记录。",
		func(ctx context.Context, input *getLogsInput) (string, error) {
			tail := input.Tail
			if tail <= 0 {
				tail = 50
			}
			var logs []nodelet.LogEntry
			err := retryOpsCall(ctx, DefaultRetryPolicy, func() error {
				var callErr error
				logs, callErr = ops.GetLogs(ctx, input.NodeletID, input.ContainerID, tail)
				return callErr
			})
			if err != nil {
				return formatRetryError("日志查询失败", err, DefaultRetryPolicy.MaxRetries), nil
			}
			if len(logs) == 0 {
				return "该容器没有日志记录。", nil
			}
			if len(logs) > 500 {
				logs = logs[:500]
			}
			return formatItems(logs), nil
		})
}

// ---------- skill 工具 ----------

type skillInput struct {
	Name string `json:"name" jsonschema:"required,description=要加载的技能名称"`
}

// NewSkillTool 创建 skill 工具——加载指定技能的详细指导内容。
// LLM 在判断用户意图后自主调用，无需预路由。
func NewSkillTool(store *SkillStore) (tool.InvokableTool, error) {
	return utils.InferTool("skill",
		"Load a specialized skill when the task at hand matches one of the available skills listed in the system prompt. "+
			"Use this tool to inject the skill's instructions into the current conversation. "+
			"The skill name must match one of the available skills.",
		func(ctx context.Context, input *skillInput) (string, error) {
			skill, ok := store.Get(input.Name)
			if !ok {
				var names []string
				for _, s := range store.Enabled() {
					names = append(names, s.Name)
				}
				return fmt.Sprintf("Skill %q not found. Available skills: %s", input.Name, strings.Join(names, ", ")), nil
			}
			if !skill.Enabled {
				return fmt.Sprintf("Skill %q is disabled.", input.Name), nil
			}
			return formatSkillContent(skill), nil
		})
}

// formatSkillContent 按 OpenCode 风格将 Skill 内容格式化为 XML。
func formatSkillContent(skill *Skill) string {
	return strings.Join([]string{
		"<skill_content name=\"" + skill.Name + "\">",
		"# Skill: " + skill.Name,
		"",
		skill.Content,
		"</skill_content>",
	}, "\n")
}

// ---------- 组合入口 ----------

// NewTools 创建所有 LLM 可调用的运维工具（含 skill 工具）。
func NewTools(ops OpsData, store *SkillStore) ([]tool.InvokableTool, error) {
	factories := []func(OpsData) (tool.InvokableTool, error){
		NewListNodeletsTool,
		NewListContainersTool,
		NewGetLogsTool,
		NewRepoSyncTool,
		NewRepoListDirTool,
		NewRepoReadFileTool,
		NewRepoFetchTool,
	}

	toolList := make([]tool.InvokableTool, 0, len(factories)+1)
	for _, fn := range factories {
		t, err := fn(ops)
		if err != nil {
			return nil, err
		}
		toolList = append(toolList, t)
	}

	if store != nil {
		skillTool, err := NewSkillTool(store)
		if err != nil {
			return nil, err
		}
		toolList = append(toolList, skillTool)
	}

	return toolList, nil
}

// ---------- 工具函数 ----------

// formatItems 将任意切片序列化为带缩进的 JSON 字符串。
func formatItems(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}
