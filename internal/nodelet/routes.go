package nodelet

import "net/url"

const (
	// HealthPath 是 Nodelet 存活检查接口。
	HealthPath = "/health"

	// HostPath 是 Nodelet 返回本机信息的接口。
	HostPath = "/host"

	// ContainersPath 是 Nodelet 返回本机容器列表的接口。
	ContainersPath = "/containers"
)

// ContainerLogsPath 返回指定容器的历史日志接口路径。
func ContainerLogsPath(containerID string) string {
	return "/containers/" + url.PathEscape(containerID) + "/logs"
}

// ContainerLogsStreamPath 返回指定容器的实时日志接口路径。
func ContainerLogsStreamPath(containerID string) string {
	return "/containers/" + url.PathEscape(containerID) + "/logs/stream"
}

// ContainerInspectPath 返回指定容器的 inspect 接口路径。
func ContainerInspectPath(containerID string) string {
	return "/containers/" + url.PathEscape(containerID) + "/inspect"
}
