package nodelet

import "time"

// Health 表示 Nodelet 自身是否存活。
type Health struct {
	Status string `json:"status"`
}

// Host 表示一台运行 Nodelet 的服务器。
type Host struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	Available     bool   `json:"available"`
	DockerVersion string `json:"dockerVersion"`
	Runtime       string `json:"runtime"`
	NCPU          int    `json:"nCPU"`
	MemTotal      int64  `json:"memTotal"`
}

// Container 表示某台服务器上的一个 Docker 容器。
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Image     string    `json:"image"`
	State     string    `json:"state"`
	Health    string    `json:"health,omitempty"`
	HostID    string    `json:"hostId"`
	Created   time.Time `json:"created"`
	StartedAt time.Time `json:"startedAt"`
}

// LogEntry 表示一条容器日志。
type LogEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	ContainerID string    `json:"containerId"`
	Stream      string    `json:"stream"`
	Message     string    `json:"message"`
}
