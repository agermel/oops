package docker

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"oops/internal/nodelet"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

// API 是本项目使用到的 Docker Engine API 最小集合。
type API interface {
	// ContainerList 返回当前 Docker daemon 上的容器列表。
	ContainerList(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)

	// ContainerLogs 返回指定容器的日志读取流。
	ContainerLogs(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)

	// Info 返回 Docker daemon 和主机的基础信息。
	Info(context.Context, client.InfoOptions) (client.SystemInfoResult, error)

	// Ping 检查 Docker daemon 是否可达。
	Ping(context.Context, client.PingOptions) (client.PingResult, error)

	// ServerVersion 返回 Docker daemon 版本组件。
	ServerVersion(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error)
}

// Client 通过 Docker Engine API 读取本机容器运行环境。
type Client struct {
	api     API
	address string
}

// NewClient 创建连接本机 Docker daemon 的客户端。
func NewClient(address string) (*Client, error) {
	api, err := client.New(client.FromEnv, client.WithUserAgent("Oops-Nodelet"))
	if err != nil {
		return nil, err
	}
	return NewClientWithAPI(api, address), nil
}

// NewClientWithAPI 用指定 Docker API 创建客户端，主要用于测试。
func NewClientWithAPI(api API, address string) *Client {
	return &Client{api: api, address: address}
}

// Host 返回当前机器和 Docker daemon 的基础信息。
func (c *Client) Host(r *http.Request) (nodelet.Host, error) {
	if _, err := c.api.Ping(r.Context(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nodelet.Host{}, err
	}

	infoResult, err := c.api.Info(r.Context(), client.InfoOptions{})
	if err != nil {
		return nodelet.Host{}, err
	}

	info := infoResult.Info

	return nodelet.Host{
		ID:            hostID(info),
		Name:          info.Name,
		Address:       c.address,
		Available:     true,
		DockerVersion: info.ServerVersion,
		Runtime:       c.runtime(r.Context(), info),
		NCPU:          info.NCPU,
		MemTotal:      info.MemTotal,
	}, nil
}

// Containers 返回当前机器上的容器列表。
func (c *Client) Containers(r *http.Request) ([]nodelet.Container, error) {
	if _, err := c.api.Ping(r.Context(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nil, err
	}

	infoResult, err := c.api.Info(r.Context(), client.InfoOptions{})
	if err != nil {
		return nil, err
	}
	id := hostID(infoResult.Info)

	result, err := c.api.ContainerList(r.Context(), client.ContainerListOptions{All: true})
	if err != nil {
		return nil, err
	}

	containers := make([]nodelet.Container, 0, len(result.Items))
	for _, item := range result.Items {
		containers = append(containers, nodelet.Container{
			ID:      item.ID,
			Name:    containerName(item.Names),
			Image:   item.Image,
			State:   string(item.State),
			Health:  healthStatus(item.Health),
			HostID:  id,
			Created: time.Unix(item.Created, 0),
		})
	}
	return containers, nil
}

// ContainerLogs 返回指定容器的历史日志。
func (c *Client) ContainerLogs(r *http.Request, containerID string) ([]nodelet.LogEntry, error) {
	if _, err := c.api.Ping(r.Context(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nil, err
	}

	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}
	if _, err := strconv.Atoi(tail); err != nil {
		tail = "100"
	}

	reader, err := c.api.ContainerLogs(r.Context(), containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       tail,
	})
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return parseLogs(containerID, data), nil
}

// ContainerLogsStream 返回指定容器的实时日志流。
func (c *Client) ContainerLogsStream(r *http.Request, containerID string) (<-chan nodelet.LogEntry, error) {
	if _, err := c.api.Ping(r.Context(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nil, err
	}

	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = "100"
	}
	if _, err := strconv.Atoi(tail); err != nil {
		tail = "100"
	}

	reader, err := c.api.ContainerLogs(r.Context(), containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Timestamps: true,
		Tail:       tail,
		Follow:     true,
	})
	if err != nil {
		return nil, err
	}
	return streamLogs(r.Context(), containerID, reader), nil
}

// hostID 返回面板中稳定使用的机器 ID。
func hostID(info system.Info) string {
	if info.Swarm.NodeID != "" {
		return info.Swarm.NodeID
	}
	if info.ID != "" {
		return info.ID
	}
	return info.Name
}

// containerName 返回 Docker 容器的主名称。
func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// healthStatus 返回容器健康检查状态。
func healthStatus(health *containertypes.HealthSummary) string {
	if health == nil {
		return ""
	}
	return string(health.Status)
}

// runtime 判断当前容器运行时是 docker 还是 podman。
func (c *Client) runtime(ctx context.Context, info system.Info) string {
	version, err := c.api.ServerVersion(ctx, client.ServerVersionOptions{})
	if err == nil {
		for _, component := range version.Components {
			if strings.Contains(strings.ToLower(component.Name), "podman") {
				return "podman"
			}
		}
		if strings.Contains(strings.ToLower(version.Platform.Name), "podman") {
			return "podman"
		}
	}
	if strings.Contains(strings.ToLower(info.OperatingSystem), "podman") {
		return "podman"
	}
	return "docker"
}
