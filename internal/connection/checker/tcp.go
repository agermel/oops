package checker

import (
	"context"
	"net"
	"time"

	"oops/internal/connection"
)

// TCPChecker 通过 TCP dial 判断目标组件端口是否可访问。
type TCPChecker struct {
	connectionType string
	timeout        time.Duration
}

// NewTCPChecker 创建通用 TCP 健康检查器。
func NewTCPChecker(connectionType string) *TCPChecker {
	return &TCPChecker{
		connectionType: connectionType,
		timeout:        5 * time.Second,
	}
}

// Type 返回该 Checker 支持的连接类型。
func (c *TCPChecker) Type() string {
	return c.connectionType
}

// Check 对 Connection.Address 发起 TCP 连接，并根据连接结果判断可用性。
func (c *TCPChecker) Check(ctx context.Context, conn connection.Connection) connection.Result {
	start := time.Now()
	result := connection.Result{
		ConnectionID: conn.ID,
		Status:       connection.StatusUnknown,
		CheckedAt:    start,
	}

	dialer := net.Dialer{Timeout: c.timeout}
	tcpConn, err := dialer.DialContext(ctx, "tcp", conn.Address)
	result.Latency = time.Since(start).Milliseconds()
	if err != nil {
		result.Status = connection.StatusDead
		result.Message = err.Error()
		return result
	}
	defer tcpConn.Close()

	result.Status = connection.StatusAlive
	result.Message = "tcp connected"
	return result
}
