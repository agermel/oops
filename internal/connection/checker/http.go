package checker

import (
	"context"
	"net/http"
	"strings"
	"time"

	"oops/internal/connection"
)

// HTTPChecker 通过 HTTP GET 判断目标组件是否可访问。
type HTTPChecker struct {
	connectionType string
	path           string
	client         *http.Client
}

// NewHTTPChecker 创建一个通用 HTTP 健康检查器。
func NewHTTPChecker(connectionType string, path string) *HTTPChecker {
	return &HTTPChecker{
		connectionType: connectionType,
		path:           path,
		client:         &http.Client{Timeout: 5 * time.Second},
	}
}

// Type 返回该 Checker 支持的连接类型。
func (c *HTTPChecker) Type() string {
	return c.connectionType
}

// Check 对 Connection.Address + path 发起 GET 请求，并根据响应状态码判断可用性。
func (c *HTTPChecker) Check(ctx context.Context, conn connection.Connection) connection.Result {
	start := time.Now()
	result := connection.Result{
		ConnectionID: conn.ID,
		Status:       connection.StatusUnknown,
		CheckedAt:    start,
	}

	// 构造带 context 的请求，方便上层取消或设置超时。
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalizeHTTPAddress(conn.Address)+c.path, nil)
	if err != nil {
		result.Status = connection.StatusDead
		result.Message = err.Error()
		return result
	}

	// 发送请求并记录实际耗时。
	resp, err := c.client.Do(req)
	result.Latency = time.Since(start).Milliseconds()
	if err != nil {
		result.Status = connection.StatusDead
		result.Message = err.Error()
		return result
	}
	defer resp.Body.Close()

	// 2xx 响应代表目标组件当前可访问。
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Status = connection.StatusAlive
		result.Message = resp.Status
		return result
	}

	result.Status = connection.StatusDead
	result.Message = resp.Status
	return result
}

// normalizeHTTPAddress 为只写 host:port 的配置补齐 http 协议。
func normalizeHTTPAddress(address string) string {
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return address
	}
	return "http://" + address
}
