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

export type Host = {
  id: string;
  name: string;
  address: string;
  available: boolean;
  dockerVersion: string;
  runtime: string;
  nCPU: number;
  memTotal: number;
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

export type HealthResult = {
  status: "alive" | "dead" | "unknown";
  message?: string;
  latency: number;
};

export type MCPStatus = {
  connected: boolean;
  toolCount: number;
  error?: string;
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
};

// MCPConnectionStatus 是带运行时状态的 MCP 连接。
export type MCPConnectionStatus = MCPConnectionConfig & {
  status: "running" | "stopped" | "error";
  error?: string;
  toolCount: number;
};

// MCPPrefill 用于从容器 DSN 信息预填 MCP 连接表单。
export type MCPPrefill = {
  name: string;
  type: string;
  env: string[];
};

export type ContainerDetail = {
  container: ContainerInspect;
  serviceType: string;
  dsn?: DSNInfo;
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
  type: "thinking" | "tool_call" | "tool_result" | "answer" | "error";
  content: string;
  toolName?: string;
  toolArgs?: string;
  toolCallId?: string;
};

export type ChatExchange = {
  question: string;
  steps: StepEvent[];
  answer?: string;
  error?: string;
};

// ---- 兼容旧组件 ----

export type NodeletItem = {
  nodelet: NodeletConfig;
  host: Host;
  available: boolean;
  error?: string;
};

// ---- 全局导航 ----

export const navigation = [
  { id: "projects", label: "项目", icon: FolderKanban },
] as const;

// ---- 项目内导航 ----

export const projectNavigation = [
  { id: "overview", label: "概览", icon: LayoutDashboard },
  { id: "mcp", label: "MCP 连接", icon: Wrench },
  { id: "chat", label: "助手", icon: Sparkles },
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
  unknown: "未知",
};

// ---- 常量 ----

export const MAX_LOGS = 2000;
export const LOG_FLUSH_MS = 250;
export const LOG_MAX_WAIT_MS = 1000;

export const ansiConvertor = new AnsiConvertor({
  escapeXML: true,
  fg: "#f5f7fa",
  bg: "#1f2430",
});
