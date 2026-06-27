package agent

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
)

// Client 调用远端 oops-agent HTTP 接口。
type Client struct {
	httpClient *http.Client
}

// NewClient 创建 Agent HTTP 客户端。
func NewClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 8 * time.Second}
	}
	return &Client{httpClient: httpClient}
}

// Host 读取远端 Agent 所在机器信息。
func (c *Client) Host(ctx context.Context, address string, token string) (Host, error) {
	var host Host
	if err := c.get(ctx, address, HostPath, token, &host); err != nil {
		return Host{}, err
	}
	return host, nil
}

// Containers 读取远端 Agent 上的容器列表。
func (c *Client) Containers(ctx context.Context, address string, token string) ([]Container, error) {
	var containers []Container
	if err := c.get(ctx, address, ContainersPath, token, &containers); err != nil {
		return nil, err
	}
	return containers, nil
}

// ContainerLogs 读取远端 Agent 上某个容器的历史日志。
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

// get 发起 GET 请求并反序列化 JSON 响应。
func (c *Client) get(ctx context.Context, address string, route string, token string, out any) error {
	endpoint, err := joinURL(address, route)
	if err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(response.Body)
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return fmt.Errorf("agent returned HTTP %d: %s", response.StatusCode, message)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// joinURL 拼接 Agent 地址和协议路径。
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
