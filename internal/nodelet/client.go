package nodelet

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"oops/internal/logutil"
	"go.uber.org/zap"
)

// Client 调用远端 oops-nodelet HTTP 接口。
type Client struct {
	httpClient *http.Client
}

// NewClient 创建 Nodelet HTTP 客户端。
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	return &Client{httpClient: httpClient}
}

// Host 读取远端 Nodelet 所在机器信息。
func (c *Client) Host(ctx context.Context, address string, token string) (Host, error) {
	var host Host
	if err := c.get(ctx, address, HostPath, token, &host); err != nil {
		return Host{}, err
	}
	return host, nil
}

// Containers 读取远端 Nodelet 上的容器列表。
func (c *Client) Containers(ctx context.Context, address string, token string) ([]Container, error) {
	var containers []Container
	if err := c.get(ctx, address, ContainersPath, token, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// ContainerLogs 读取远端 Nodelet 上某个容器的历史日志。
func (c *Client) ContainerLogs(ctx context.Context, address string, token string, containerID string, tail string) ([]LogEntry, error) {
	route := ContainerLogsPath(containerID)
	if tail != "" {
		route += "?tail=" + url.QueryEscape(tail)
	}

	var logs []LogEntry
	if err := c.get(ctx, address, route, token, &logs); err != nil {
		return nil, err
	}
	return logs, nil
}

// InspectContainer 读取远端 Nodelet 上某个容器的详细信息（环境变量、端口等）。
func (c *Client) InspectContainer(ctx context.Context, address string, token string, containerID string) (ContainerInspect, error) {
	var detail ContainerInspect
	if err := c.get(ctx, address, ContainerInspectPath(containerID), token, &detail); err != nil {
		return ContainerInspect{}, err
	}
	return detail, nil
}

// ContainerLogsStream 读取远端 Nodelet 上某个容器的实时日志 SSE 流。
func (c *Client) ContainerLogsStream(ctx context.Context, address string, token string, containerID string, tail string) (io.ReadCloser, error) {
	route := ContainerLogsStreamPath(containerID)
	if tail != "" {
		route += "?tail=" + url.QueryEscape(tail)
	}
	return c.stream(ctx, address, route, token)
}

// get 发起 GET 请求并反序列化 JSON 响应。
func (c *Client) get(ctx context.Context, address string, route string, token string, out any) error {
	endpoint, err := joinURL(address, route)
	if err != nil {
		return err
	}

	start := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.httpClient.Do(request)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		logutil.Error("nodelet client: request failed",
			zap.String("method", "GET"),
			zap.String("path", route),
			zap.String("endpoint", endpoint),
			zap.Int64("latencyMs", latency),
			zap.Error(err),
		)
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(response.Body)
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		logutil.Error("nodelet client: non-2xx response",
			zap.String("method", "GET"),
			zap.String("path", route),
			zap.Int("status", response.StatusCode),
			zap.Int64("latencyMs", latency),
		)
		return fmt.Errorf("nodelet returned HTTP %d: %s", response.StatusCode, message)
	}

	logutil.Info("nodelet client: request",
		zap.String("method", "GET"),
		zap.String("path", route),
		zap.Int("status", response.StatusCode),
		zap.Int64("latencyMs", latency),
	)
	return json.NewDecoder(response.Body).Decode(out)
}

// stream 发起 GET 请求并返回响应体，调用方负责关闭。
func (c *Client) stream(ctx context.Context, address string, route string, token string) (io.ReadCloser, error) {
	endpoint, err := joinURL(address, route)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := c.httpClient
	if httpClient.Timeout != 0 {
		clone := *httpClient
		clone.Timeout = 0
		httpClient = &clone
	}

	response, err := httpClient.Do(request)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		logutil.Error("nodelet client: stream failed",
			zap.String("method", "GET"),
			zap.String("path", route),
			zap.String("endpoint", endpoint),
			zap.Int64("latencyMs", latency),
			zap.Error(err),
		)
		return nil, err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		logutil.Error("nodelet client: non-2xx stream response",
			zap.String("method", "GET"),
			zap.String("path", route),
			zap.Int("status", response.StatusCode),
			zap.Int64("latencyMs", latency),
		)
		return nil, fmt.Errorf("nodelet returned HTTP %d: %s", response.StatusCode, message)
	}

	logutil.Debug("nodelet client: stream connected",
		zap.String("method", "GET"),
		zap.String("path", route),
		zap.Int("status", response.StatusCode),
		zap.Int64("latencyMs", latency),
	)
	return response.Body, nil
}

// joinURL 拼接 Nodelet 地址和协议路径。
func joinURL(address string, route string) (string, error) {
	base, err := url.Parse(address)
	if err != nil {
		return "", err
	}

	routePath := route
	if index := strings.Index(route, "?"); index >= 0 {
		routePath = route[:index]
		base.RawQuery = route[index+1:]
	}

	base.Path = path.Join(base.Path, strings.TrimPrefix(routePath, "/"))
	if strings.HasSuffix(routePath, "/") && !strings.HasSuffix(base.Path, "/") {
		base.Path += "/"
	}
	return base.String(), nil
}
