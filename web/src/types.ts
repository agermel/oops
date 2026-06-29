import AnsiConvertor from "ansi-to-html";
import {
  Sparkles,
  FolderKanban,
  LayoutDashboard,
  Wrench,
  Search,
  Server,
  Database,
  Globe,
  Layers,
  Terminal,
  Cog,
} from "lucide-react";

// ---- 项目 ----

export type Project = {
  id: string;
  name: string;
  description?: string;
  nodeletIds: string[];
  createdAt: string;
  updatedAt: string;
};

// ---- Nodelet / 服务器 ----

export type NodeletConfig = {
  id: string;
  name: string;
  address: string;
};

export type NodeletHostSummary = {
  available: boolean;
  dockerVersion: string;
  runtime: string;
  nCPU: number;
  memTotal: number;
};

export type ServerWithNodelet = {
  nodelet: NodeletConfig;
  host: NodeletHostSummary;
  error?: string;
};

// ---- 容器 ----

export type ContainerWithType = {
  id: string;
  name: string;
  image: string;
  state: string;
  health?: string;
  serviceType: string;
};

export type ContainerInspect = {
  id: string;
  name: string;
  image: string;
  state: string;
  env: string[];
  ports: PortMapping[];
  hostId: string;
  created: string;
};

export type PortMapping = {
  hostPort?: string;
  containerPort: number;
  protocol?: string;
};

// ---- 容器详情聚合 ----

export type DSNInfo = {
  host: string;
  port: number;
  database?: string;
  user?: string;
  raw?: string;
};

// DSNConfig is the response from GET /containers/:cid/dsn.
export type DSNConfig = {
  detected: Record<string, string>;
  overrides: Record<string, string>;
  merged: Record<string, string>;
  hasOverrides: boolean;
};

export type HealthResult = {
  status: "alive" | "dead" | "unknown";
  message?: string;
  latency: number;
};

export type MCPStatus = {
  connected: boolean;
  toolCount: number;
  error?: string;
  connectionId?: string;
};

// MCPConnectionConfig 对应后端 MCP 连接配置。
export type MCPConnectionConfig = {
  id: string;
  name: string;
  type: string;
  command: string;
  args: string[];
  env: string[];
  enabled: boolean;
  containerId?: string;
  nodeletId?: string;
};

// ToolInfo 对应后端 ToolInfo，表示一个 MCP 工具的元数据。
export type ToolInfo = {
  name: string;
  description: string;
};

// ToolTestResult 表示前端单个工具的测试状态。
export type ToolTestResult = {
  status: "untested" | "testing" | "ok" | "error";
  output?: string;
  error?: string;
};

// MCPConnectionStatus 是带运行时状态的 MCP 连接。
export type MCPConnectionStatus = MCPConnectionConfig & {
  status: "running" | "stopped" | "error";
  error?: string;
  toolCount: number;
  tools?: ToolInfo[];
};

// MCPPrefill 用于从容器 DSN 信息预填 MCP 连接表单。
export type MCPPrefill = {
  name: string;
  type: string;
  host?: string;
  port?: number;
  user?: string;
  database?: string;
  env: string[];
  containerId?: string;
  nodeletId?: string;
};

export type ContainerDetail = {
  container: ContainerInspect;
  serviceType: string;
  dsn?: DSNInfo;
  dsnOverrides?: Record<string, string>;
  hasDSNOverrides: boolean;
  health?: HealthResult;
  mcp?: MCPStatus;
};

// ---- 日志 ----

export type LogEntry = {
  timestamp: string;
  containerId: string;
  stream: string;
  message: string;
  rawMessage?: string;
  level?: "fatal" | "error" | "warn" | "info" | "debug" | "trace" | "unknown";
};

// ---- LLM 对话 ----

export type StepEvent = {
  type: "thinking" | "tool_call" | "tool_result" | "answer" | "error" | "session";
  content: string;
  toolName?: string;
  toolArgs?: string;
  toolCallId?: string;
  // session 事件专用
  sessionId?: string;
};

export type ChatExchange = {
  question: string;
  steps: StepEvent[];
  answer?: string;
  error?: string;
};

export type SessionInfo = {
  id: string;
  projectId?: string;
  messageCount: number;
  createdAt: number;
  updatedAt: number;
};

export type SessionMessage = {
  role: "user" | "assistant" | "tool" | "thinking" | "tool_call";
  content: string;
  toolCallId?: string;
  toolName?: string;
  toolArgs?: string;
};

export type SessionDetail = SessionInfo & {
  messages: SessionMessage[];
};

// ---- Nodelet 选择项 (添加服务器弹窗用) ----

export type NodeletItem = ServerWithNodelet;

// ---- 全局导航 ----

export const navigation = [
  { id: "projects", label: "项目", icon: FolderKanban },
  { id: "console", label: "控制台", icon: Terminal },
] as const;

// ---- 项目内导航 ----

export const projectNavigation = [
  { id: "overview", label: "概览", icon: LayoutDashboard },
  { id: "mcp", label: "MCP 连接", icon: Wrench },
  { id: "tools", label: "工具管理", icon: Cog },
  { id: "chat", label: "助手", icon: Sparkles },
  { id: "console", label: "控制台", icon: Terminal },
] as const;

// ---- 服务类型图标映射 ----

export const serviceTypeIcons: Record<string, typeof Database> = {
  mysql: Database,
  redis: Layers,
  postgres: Database,
  mongo: Database,
  nginx: Globe,
  elasticsearch: Search,
  kafka: Layers,
  etcd: Layers,
  clickhouse: Database,
  minio: Database,
  consul: Globe,
  zookeeper: Layers,
  prometheus: Database,
  grafana: Search,
  influxdb: Database,
  memcached: Layers,
  cassandra: Database,
  neo4j: Database,
  caddy: Globe,
  unknown: Server,
};

export const serviceTypeLabels: Record<string, string> = {
  mysql: "MySQL",
  redis: "Redis",
  postgres: "PostgreSQL",
  mongo: "MongoDB",
  nginx: "Nginx",
  elasticsearch: "Elasticsearch",
  kafka: "Kafka",
  etcd: "Etcd",
  jaeger: "Jaeger",
  nacos: "Nacos",
  rabbitmq: "RabbitMQ",
  clickhouse: "ClickHouse",
  minio: "MinIO",
  consul: "Consul",
  zookeeper: "ZooKeeper",
  prometheus: "Prometheus",
  grafana: "Grafana",
  influxdb: "InfluxDB",
  memcached: "Memcached",
  cassandra: "Cassandra",
  neo4j: "Neo4j",
  caddy: "Caddy",
  unknown: "未知",
};

// shortImageName 从完整镜像名中提取可读的简短名称。
// "ghcr.io/myorg/myapp:latest" → "myapp:latest"
// "kicbase:v0.0.50@sha256:eb4fec..." → "kicbase:v0.0.50"
// "mysql:8.0" → "mysql:8.0"
export function shortImageName(image: string): string {
  // 去掉 @sha256:... 摘要后缀
  const atIndex = image.indexOf("@");
  const cleaned = atIndex >= 0 ? image.slice(0, atIndex) : image;
  // 去掉 registry 前缀，取最后一个 / 之后的部分
  const lastSlash = cleaned.lastIndexOf("/");
  const name = lastSlash >= 0 ? cleaned.slice(lastSlash + 1) : cleaned;
  return name || image;
}

// serviceLabel 返回服务类型的中文标签；若无法识别则返回空字符串。
export function serviceLabel(serviceType: string): string {
  if (serviceType && serviceType !== "unknown") {
    return serviceTypeLabels[serviceType] || serviceType;
  }
  return "";
}

// ---- 常量 ----

export const MAX_LOGS = 2000;
export const LOG_FLUSH_MS = 250;
export const LOG_MAX_WAIT_MS = 1000;

export const ansiConvertor = new AnsiConvertor({
  escapeXML: true,
  fg: "#f5f7fa",
  bg: "#1f2430",
});

export const mcpStatusLabel: Record<string, string> = {
  running: "运行中",
  stopped: "已停止",
  error: "异常",
};
