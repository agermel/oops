package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"oops/internal/nodelet"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// OpsData 是 LLM 工具读取运维数据的抽象接口。
// 由 api.Server 实现，避免 llm 包直接依赖 api 包。
type OpsData interface {
	// ListNodelets 返回所有 Nodelet 的概要信息。
	ListNodelets(ctx context.Context) ([]NodeletSummary, error)

	// ListContainers 返回指定 Nodelet 上的容器列表。
	ListContainers(ctx context.Context, nodeletID string) ([]nodelet.Container, error)

	// GetLogs 返回指定容器的历史日志。
	GetLogs(ctx context.Context, nodeletID, containerID string, tail int) ([]nodelet.LogEntry, error)

	// CheckConnections 执行所有连接健康检查。
	CheckConnections(ctx context.Context) ([]ConnectionStatus, error)
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

// ConnectionStatus 是 LLM 可见的连接健康状态。
type ConnectionStatus struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Latency int64  `json:"latency"`
	Error   string `json:"error,omitempty"`
}

// ---------- 工具输入类型 ----------

type listNodeletsInput struct{}

type listContainersInput struct {
	NodeletID string `json:"nodelet_id" jsonschema:"required,description=要查询的 Nodelet ID"`
}

type getLogsInput struct {
	NodeletID   string `json:"nodelet_id" jsonschema:"required,description=目标 Nodelet ID"`
	ContainerID string `json:"container_id" jsonschema:"required,description=目标容器 ID"`
	Tail        int    `json:"tail" jsonschema:"description=返回最近的日志行数,default=50"`
}

type checkConnectionsInput struct{}

// ---------- 单个工具构造函数 ----------

// NewListNodeletsTool 创建 list_nodelets 工具——列出所有受监控的机器及其主机信息。
func NewListNodeletsTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("list_nodelets", ""+
		"列出所有已配置的 Nodelet（受监控的机器）。"+
		"返回每台机器的 ID、名称、地址、可用状态、Docker 版本、运行时、CPU 数量和内存总量。",
		func(ctx context.Context, _ *listNodeletsInput) (string, error) {
			items, err := ops.ListNodelets(ctx)
			if err != nil {
				return fmt.Sprintf("机器列表查询失败：%v", err), nil
			}
			return formatItems(items), nil
		})
}

// NewListContainersTool 创建 list_containers 工具——列出指定机器上的容器。
func NewListContainersTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("list_containers", ""+
		"列出指定 Nodelet 上运行的所有 Docker 容器。"+
		"返回容器 ID、名称、镜像、状态（running/stopped 等）、健康状态和创建时间。"+
		"最多返回 200 条记录。",
		func(ctx context.Context, input *listContainersInput) (string, error) {
			containers, err := ops.ListContainers(ctx, input.NodeletID)
			if err != nil {
				return fmt.Sprintf("查询失败：%v", err), nil
			}
			if len(containers) == 0 {
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
		"最多返回 500 条记录。",
		func(ctx context.Context, input *getLogsInput) (string, error) {
			tail := input.Tail
			if tail <= 0 {
				tail = 50
			}
			logs, err := ops.GetLogs(ctx, input.NodeletID, input.ContainerID, tail)
			if err != nil {
				return fmt.Sprintf("日志查询失败：%v", err), nil
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

// NewCheckConnectionsTool 创建 check_connections 工具——对所有连接执行健康检查。
func NewCheckConnectionsTool(ops OpsData) (tool.InvokableTool, error) {
	return utils.InferTool("check_connections", ""+
		"对所有已配置的连接（MySQL、Redis、Elasticsearch、Kafka 等）执行健康检查。"+
		"返回每个连接的状态（alive/dead/unknown）、延迟和错误消息。",
		func(ctx context.Context, _ *checkConnectionsInput) (string, error) {
			items, err := ops.CheckConnections(ctx)
			if err != nil {
				return fmt.Sprintf("连接检查失败：%v", err), nil
			}
			return formatItems(items), nil
		})
}

// ---------- 组合入口 ----------

// NewTools 创建所有 LLM 可调用的运维工具。
func NewTools(ops OpsData) ([]tool.InvokableTool, error) {
	factories := []func(OpsData) (tool.InvokableTool, error){
		NewListNodeletsTool,
		NewListContainersTool,
		NewGetLogsTool,
		NewCheckConnectionsTool,
	}

	tools := make([]tool.InvokableTool, 0, len(factories))
	for _, fn := range factories {
		t, err := fn(ops)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
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
