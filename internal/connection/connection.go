package connection

import (
	"context"
	"time"
)

// Status 表示一次连接探测后的可用性状态。
type Status string

const (
	// StatusAlive 表示目标组件当前可访问。
	StatusAlive Status = "alive"

	// StatusDead 表示目标组件当前不可访问。
	StatusDead Status = "dead"

	// StatusUnknown 表示尚未探测或状态无法判断。
	StatusUnknown Status = "unknown"
)

// Connection 表示 GUI 里配置的一条外部服务连接。
type Connection struct {
	// ID 是连接的唯一标识。
	ID string `json:"id"`

	// Name 是给用户看的连接名称。
	Name string `json:"name"`

	// Type 是连接类型，例如 elasticsearch、jaeger、nacos。
	Type string `json:"type"`

	// Address 是目标组件的基础地址。
	Address string `json:"address"`
}

// Result 是一次连接探测的结果。
type Result struct {
	// ConnectionID 对应被探测的连接 ID。
	ConnectionID string `json:"connectionId"`

	// Status 是本次探测得到的连接状态。
	Status Status `json:"status"`

	// Message 保存状态码、错误信息或其他诊断文本。
	Message string `json:"message,omitempty"`

	// Latency 是本次探测耗时，单位毫秒。
	Latency int64 `json:"latency"`

	// CheckedAt 是本次探测开始的时间。
	CheckedAt time.Time `json:"checkedAt"`
}

// Checker 负责判断一种连接是否可用。
type Checker interface {
	// Type 返回该 Checker 支持的连接类型。
	Type() string

	// Check 执行一次连接探测，并返回探测结果。
	Check(ctx context.Context, conn Connection) Result
}
