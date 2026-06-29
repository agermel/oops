package docker

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"oops/internal/nodelet"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// inspectClient 是 docker client 提供的 inspect 接口的子集。
type inspectClient interface {
	ContainerInspect(ctx context.Context, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
}

// DSNInfo 保存从容器环境变量中提取的连接信息。
type DSNInfo struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database,omitempty"`
	User     string `json:"user,omitempty"`
	Raw      string `json:"raw,omitempty"`
}

// dsnExtractor 定义一种 DSN 提取规则。
type dsnExtractor struct {
	parse func(env map[string]string) *DSNInfo
}

// dsnExtractors 按服务类型注册 DSN 提取规则。
var dsnExtractors = map[ServiceType]dsnExtractor{
	ServiceMySQL:         {parse: extractMySQLDSN},
	ServiceRedis:         {parse: extractRedisDSN},
	ServicePostgres:      {parse: extractPostgresDSN},
	ServiceMongo:         {parse: extractMongoDSN},
	ServiceElasticsearch: {parse: extractElasticsearchDSN},
}

// ContainerInspect 对指定容器执行 docker inspect，返回详细元数据。
func (c *Client) ContainerInspect(r *http.Request, containerID string) (nodelet.ContainerInspect, error) {
	if _, err := c.api.Ping(r.Context(), client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		return nodelet.ContainerInspect{}, err
	}

	infoResult, err := c.api.Info(r.Context(), client.InfoOptions{})
	if err != nil {
		return nodelet.ContainerInspect{}, err
	}
	hostID := hostID(infoResult.Info)

	insp, ok := c.api.(inspectClient)
	if !ok {
		return nodelet.ContainerInspect{}, fmt.Errorf("docker client does not support container inspect")
	}

	result, err := insp.ContainerInspect(r.Context(), containerID, client.ContainerInspectOptions{})
	if err != nil {
		return nodelet.ContainerInspect{}, err
	}

	resp := result.Container
	ports := collectPorts(resp.NetworkSettings.Ports)
	created, _ := time.Parse(time.RFC3339Nano, resp.Created)

	env := resp.Config.Env
	if env == nil {
		env = []string{}
	}

	return nodelet.ContainerInspect{
		ID:      resp.ID,
		Name:    resp.Name,
		Image:   resp.Config.Image,
		State:   string(resp.State.Status),
		Env:     env,
		Ports:   ports,
		HostID:  hostID,
		Created: created,
	}, nil
}

// ExtractDSN 从容器 inspect 信息和推断的服务类型中提取 DSN。
func ExtractDSN(stype ServiceType, detail nodelet.ContainerInspect) *DSNInfo {
	extractor, ok := dsnExtractors[stype]
	if !ok {
		return nil
	}

	envMap := parseEnvList(detail.Env)
	dsn := extractor.parse(envMap)
	if dsn == nil {
		return nil
	}

	// 从容器端口映射中补充 Port 信息。
	if dsn.Port == 0 {
		if port := findContainerPort(detail.Ports, stype); port > 0 {
			dsn.Port = port
		}
	}

	return dsn
}

// --- internal helpers ---

// collectPorts 把 Docker 端口映射转换为统一的 PortMapping 列表。
func collectPorts(ports network.PortMap) []nodelet.PortMapping {
	if ports == nil {
		return []nodelet.PortMapping{}
	}
	result := make([]nodelet.PortMapping, 0, len(ports))
	for portKey, bindings := range ports {
		pm := nodelet.PortMapping{
			ContainerPort: int(portKey.Num()),
			Protocol:      string(portKey.Proto()),
		}
		if len(bindings) > 0 {
			pm.HostPort = bindings[0].HostPort
		}
		result = append(result, pm)
	}
	return result
}

// parseEnvList 把 ["KEY=VALUE", ...] 格式的环境变量列表转为 map。
func parseEnvList(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, e := range env {
		key, value, ok := strings.Cut(e, "=")
		if ok {
			result[key] = value
		} else {
			result[e] = ""
		}
	}
	return result
}

// findContainerPort 根据服务类型查找容器暴露的端口。
func findContainerPort(ports []nodelet.PortMapping, stype ServiceType) int {
	defaultPorts := map[ServiceType]int{
		ServiceMySQL:         3306,
		ServiceRedis:         6379,
		ServicePostgres:      5432,
		ServiceMongo:         27017,
		ServiceElasticsearch: 9200,
	}

	defaultPort := defaultPorts[stype]
	for _, p := range ports {
		if p.ContainerPort == defaultPort {
			return p.ContainerPort
		}
	}
	if len(ports) > 0 {
		return ports[0].ContainerPort
	}
	return 0
}

// --- DSN extractors per service type ---

var mysqlDSNPattern = regexp.MustCompile(`([^:]+):([^@]+)@tcp\(([^)]+)\)/([^?]*)`)

func extractMySQLDSN(env map[string]string) *DSNInfo {
	dsn := firstNonEmpty(env, "MYSQL_DSN", "MYSQL_URL", "DATABASE_URL")
	if dsn == "" {
		return nil
	}

	matches := mysqlDSNPattern.FindStringSubmatch(dsn)
	if len(matches) != 5 {
		return &DSNInfo{Raw: dsn}
	}

	host, portStr := splitHostPort(matches[3], "3306")
	port, _ := strconv.Atoi(portStr)

	return &DSNInfo{
		Host:     host,
		Port:     port,
		User:     matches[1],
		Database: matches[4],
		Raw:      dsn,
	}
}

var redisAddrPattern = regexp.MustCompile(`^([^:]+):(\d+)$`)

func extractRedisDSN(env map[string]string) *DSNInfo {
	host := firstNonEmpty(env, "REDIS_HOST", "REDIS_ADDR")
	port := firstNonEmpty(env, "REDIS_PORT", "6379")
	password := firstNonEmpty(env, "REDIS_PASSWORD", "REDIS_PWD")

	if host == "" {
		// Try REDIS_URL or REDIS_ADDR as a full address (e.g. "redis://host:port" or "host:port").
		addr := firstNonEmpty(env, "REDIS_URL", "REDIS_ADDR")
		if addr != "" {
			matches := redisAddrPattern.FindStringSubmatch(addr)
			if len(matches) == 3 {
				host = matches[1]
				port = matches[2]
			} else {
				return &DSNInfo{Raw: addr}
			}
		}
		// No explicit connection env vars — still return a DSN so the UI can
		// offer one-click MCP setup.  Host stays empty; the caller fills the
		// port from container port mappings (default 6379).
	}

	portNum, _ := strconv.Atoi(port)
	raw := fmt.Sprintf("redis://%s:%d", host, portNum)
	if host == "" {
		raw = fmt.Sprintf("redis://127.0.0.1:%d", portNum)
	}
	if password != "" {
		raw = fmt.Sprintf("redis://:%s@%s:%d", password, host, portNum)
		if host == "" {
			raw = fmt.Sprintf("redis://:%s@127.0.0.1:%d", password, portNum)
		}
	}

	return &DSNInfo{
		Host: host,
		Port: portNum,
		Raw:  raw,
	}
}

var pgDSNPattern = regexp.MustCompile(`(?:postgres(?:ql)?://)([^:]+):([^@]+)@([^:]+):(\d+)/([^?]*)`)

func extractPostgresDSN(env map[string]string) *DSNInfo {
	dsn := firstNonEmpty(env, "DATABASE_URL", "POSTGRES_URL", "PGDATABASE")
	if dsn == "" {
		return nil
	}

	matches := pgDSNPattern.FindStringSubmatch(dsn)
	if len(matches) == 6 {
		port, _ := strconv.Atoi(matches[4])
		return &DSNInfo{
			Host:     matches[3],
			Port:     port,
			User:     matches[1],
			Database: matches[5],
			Raw:      dsn,
		}
	}

	host := firstNonEmpty(env, "PGHOST", "POSTGRES_HOST")
	portStr := firstNonEmpty(env, "PGPORT", "5432")
	db := firstNonEmpty(env, "PGDATABASE", "POSTGRES_DB")
	user := firstNonEmpty(env, "PGUSER", "POSTGRES_USER")

	if host != "" {
		port, _ := strconv.Atoi(portStr)
		return &DSNInfo{
			Host:     host,
			Port:     port,
			User:     user,
			Database: db,
			Raw:      dsn,
		}
	}

	return &DSNInfo{Raw: dsn}
}

func extractMongoDSN(env map[string]string) *DSNInfo {
	dsn := firstNonEmpty(env, "MONGO_URI", "MONGODB_URI", "DATABASE_URL")
	if dsn == "" {
		return nil
	}
	return &DSNInfo{Raw: dsn}
}

func extractElasticsearchDSN(env map[string]string) *DSNInfo {
	raw := firstNonEmpty(env, "ELASTICSEARCH_URL", "ES_URL")
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return &DSNInfo{Raw: raw}
	}

	host, portStr := splitHostPort(u.Host, "9200")
	port, _ := strconv.Atoi(portStr)

	user := ""
	if u.User != nil {
		user = u.User.Username()
	}

	return &DSNInfo{
		Host: host,
		Port: port,
		User: user,
		Raw:  raw,
	}
}

// --- generic helpers ---

func firstNonEmpty(env map[string]string, keys ...string) string {
	for _, key := range keys {
		if val := env[key]; val != "" {
			return val
		}
	}
	return ""
}

func splitHostPort(addr string, defaultPort string) (string, string) {
	host, port, err := netSplit(addr)
	if err != nil {
		return addr, defaultPort
	}
	return host, strconv.Itoa(port)
}

func netSplit(hostport string) (string, int, error) {
	colon := strings.LastIndex(hostport, ":")
	if colon < 0 {
		return hostport, 0, fmt.Errorf("no port in %q", hostport)
	}
	host := hostport[:colon]
	port, err := strconv.Atoi(hostport[colon+1:])
	if err != nil {
		return hostport, 0, err
	}
	return host, port, nil
}
