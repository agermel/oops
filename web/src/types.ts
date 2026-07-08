import AnsiConvertor from "ansi-to-html";
import {
  Sparkles,
  FolderKanban,
  LayoutDashboard,
  Search,
  Server,
  Database,
  Globe,
  Layers,
  Terminal,
  Cog,
  BookOpen,
} from "lucide-react";

// ---- 项目 ----

export type Project = {
  id: string;
  name: string;
  description?: string;
  githubRepo?: string;
  nodeletIds: string[];
  excludedContainerRefs?: string[];
  createdAt: string;
  updatedAt: string;
};

// ---- Nodelet / 服务器 ----

export type NodeletConfig = {
  id: string;
  name: string;
  address: string;
  token?: string;
  hasToken?: boolean;
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

export type ProbeStatus = "unknown" | "probing" | "healthy" | "unhealthy" | "dead";

export type ProbeResult = {
  nodeletId: string;
  status: ProbeStatus;
  consecutiveFails: number;
  lastProbeAt: string;
  lastError?: string;
  latencyMs: number;
};

export type NodeletStatusItem = {
  nodelet: NodeletConfig;
  status: ProbeStatus;
  available: boolean;
  lastProbeAt: string;
  latencyMs: number;
  error?: string;
};

// ---- 容器 ----

export type ContainerWithType = {
  id: string;
  name: string;
  image: string;
  command?: string;
  state: string;
  status?: string;
  health?: string;
  ports?: PortMapping[];
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
  transport: string; // "stdio" (default) | "sse"
  command: string;
  args: string[];
  env: string[];
  url: string;       // sse endpoint
  enabled: boolean;
  containerId?: string;
  nodeletId?: string;
};

// ToolInfo 对应后端 ToolInfo，表示一个 MCP 工具的元数据。
export type ToolInfo = {
  name: string;
  description: string;
  originalName?: string;
  modelName?: string;
  connectionType?: string;
};

// ToolTestResult 表示前端单个工具的测试状态。
export type ToolTestResult = {
  status: "untested" | "testing" | "ok" | "error" | "unavailable" | "transport_error";
  output?: string;
  error?: string;
};

// MCPConnectionStatus 是带运行时状态的 MCP 连接。
export type MCPConnectionStatus = MCPConnectionConfig & {
  status: "running" | "starting" | "stopped" | "error";
  error?: string;
  toolCount: number;
  tools?: ToolInfo[];
};

// ProjectMCPConnection extends MCPConnectionStatus with a scope field that tells whether
// the connection is bound to a specific container or only to a server/nodelet.
export type ProjectMCPConnection = MCPConnectionStatus & {
  scope: "container" | "nodelet";
};

export type ProjectSelection =
  | { kind: "none" }
  | { kind: "nodelet"; nodeletId: string }
  | { kind: "container"; nodeletId: string; containerId: string }
  | { kind: "mcp"; connectionId: string; nodeletId?: string; containerId?: string };

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

export type MCPContainerBindingOption =
  | {
      kind: "nodelet";
      nodeletId: string;
      nodeletName: string;
    }
  | {
      kind: "container";
      nodeletId: string;
      nodeletName: string;
      containerId: string;
      containerName: string;
      serviceType: string;
    };

export type ContainerDetail = {
  container: ContainerInspect;
  serviceType: string;
  dsn?: DSNInfo;
  dsnOverrides?: Record<string, string>;
  hasDSNOverrides: boolean;
  mcp?: MCPStatus;
};

// ---- 日志 ----

export type LogEntry = {
  timestamp: string;
  containerId?: string;
  connectionId?: string;
  stream: string;
  message: string;
  rawMessage?: string;
  level?: "fatal" | "error" | "warn" | "info" | "debug" | "trace" | "unknown";
};

// ---- LLM 对话 ----

export type TextContent = {
  type: "text";
  text: string;
  textSignature?: string;
};

export type ThinkingContent = {
  type: "thinking";
  thinking: string;
  thinkingSignature?: string;
  redacted?: boolean;
};

export type ImageContent = {
  type: "image";
  data?: string;
  mimeType?: string;
  url?: string;
  detail?: string;
};

export type ToolCallContent = {
  type: "toolCall";
  id: string;
  name: string;
  arguments: Record<string, unknown>;
  thoughtSignature?: string;
};

export type ContentBlock = TextContent | ThinkingContent | ImageContent | ToolCallContent;

export type UsageCost = {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  total: number;
};

export type Usage = {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  cacheWrite1h?: number;
  reasoning?: number;
  totalTokens: number;
  cost?: UsageCost;
};

export type StopReason = "stop" | "length" | "toolUse" | "error" | "aborted";

export type UserMessage = {
  role: "user";
  content: Array<TextContent | ImageContent>;
  timestamp: number;
};

export type AssistantMessage = {
  role: "assistant";
  content: ContentBlock[];
  provider?: string;
  model?: string;
  responseModel?: string;
  responseId?: string;
  usage: Usage;
  stopReason: StopReason;
  errorMessage?: string;
  timestamp: number;
};

export type ToolResultMessage = {
  role: "toolResult";
  toolCallId: string;
  toolName: string;
  content: Array<TextContent | ImageContent>;
  details?: unknown;
  isError: boolean;
  timestamp: number;
};

export type AgentMessage = UserMessage | AssistantMessage | ToolResultMessage;

export type ToolDefinition = {
  name: string;
  description?: string;
  parameters?: Record<string, unknown>;
};

export type ToolResult = {
  content: Array<TextContent | ImageContent>;
  details?: unknown;
  terminate?: boolean;
};

export type Context = {
  systemPrompt?: string;
  messages: AgentMessage[];
  tools?: ToolDefinition[];
};

export type AssistantMessageEvent =
  | { type: "start"; partial: AssistantMessage }
  | { type: "text_start"; contentIndex: number; partial: AssistantMessage }
  | { type: "text_delta"; contentIndex: number; delta: string; partial: AssistantMessage }
  | { type: "text_end"; contentIndex: number; content: string; partial: AssistantMessage }
  | { type: "thinking_start"; contentIndex: number; partial: AssistantMessage }
  | { type: "thinking_delta"; contentIndex: number; delta: string; partial: AssistantMessage }
  | { type: "thinking_end"; contentIndex: number; content: string; partial: AssistantMessage }
  | { type: "toolcall_start"; contentIndex: number; partial: AssistantMessage }
  | { type: "toolcall_delta"; contentIndex: number; delta: string; partial: AssistantMessage }
  | { type: "toolcall_end"; contentIndex: number; toolCall: ToolCallContent; partial: AssistantMessage }
  | { type: "done"; reason: Extract<StopReason, "stop" | "length" | "toolUse">; message: AssistantMessage }
  | { type: "error"; reason: Extract<StopReason, "error" | "aborted">; error: AssistantMessage };

export type AgentEvent =
  | { type: "agent_start" }
  | { type: "agent_end"; messages: AgentMessage[] }
  | { type: "turn_start"; turn: number }
  | { type: "turn_end"; turn: number; message: AssistantMessage; toolResults: ToolResultMessage[] }
  | { type: "message_start"; message: AgentMessage }
  | { type: "message_update"; message: AssistantMessage; assistantMessageEvent: AssistantMessageEvent; delta?: string }
  | { type: "message_end"; message: AgentMessage }
  | { type: "tool_execution_start"; toolCallId: string; toolName: string; args: Record<string, unknown> }
  | { type: "tool_execution_update"; toolCallId: string; toolName: string; delta: string }
  | { type: "tool_execution_end"; toolCallId: string; toolName: string; result: ToolResult; isError: boolean };

export type SessionEntryType =
  | "session_info"
  | "message"
  | "model_change"
  | "thinking_level_change"
  | "active_tools_change"
  | "compaction"
  | "branch_summary"
  | "custom"
  | "custom_message"
  | "label"
  | "leaf";

export type SessionEntry = {
  type: SessionEntryType;
  version?: number;
  id?: string;
  parentId?: string;
  timestamp?: string;
  cwd?: string;
  message?: AgentMessage;
  model?: string;
  provider?: string;
  reasoning?: string;
  toolNames?: string[];
  summary?: string;
  firstKeptEntryId?: string;
  tokensBefore?: number;
  details?: unknown;
  customType?: string;
  payload?: unknown;
  label?: string;
  name?: string;
  leafId?: string;
};

export type SessionResponse = {
  sessionId: string;
  leafId?: string;
  messages: AgentMessage[];
  events: AgentEvent[];
  tools: ToolDefinition[];
  entries: SessionEntry[];
};

export type CreateRunResponse = {
  runId: string;
  sessionId: string;
};

export type RunDoneEvent = {
  type: "run_done";
  session: SessionResponse;
};

export type RunErrorEvent = {
  type: "run_error";
  error: string;
  session: SessionResponse;
};

export type RunStreamEvent = AgentEvent | RunDoneEvent | RunErrorEvent;

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

// ---- Skills ----

export type Skill = {
  name: string;
  description: string;
  content: string;
  icon: string;
  label: string;
  color: string;
  enabled: boolean;
};

// ---- Nodelet 选择项 (添加服务器弹窗用) ----

export type NodeletItem = ServerWithNodelet;

// ---- 全局导航 ----

export const navigation = [
  { id: "projects", label: "项目", icon: FolderKanban },
  { id: "servers", label: "服务器", icon: Server },
  { id: "console", label: "控制台", icon: Terminal },
] as const;

// ---- 项目内导航 ----

export const projectNavigation = [
  { id: "overview", label: "概览", icon: LayoutDashboard },
  { id: "tools", label: "工具管理", icon: Cog },
  { id: "skills", label: "技能管理", icon: BookOpen },
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
  nacos: Globe,
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

export const SESSION_STORAGE_KEY = "oops_session_id";

/** Standard DSN field keys for display ordering in DSNInfoCard. */
export const DSN_STANDARD_FIELDS = ["host", "port", "user", "database", "raw"] as const;

/** Default max steps per skill name. Falls back to 15. */
export const SKILL_MAX_STEPS: Record<string, number> = {
  diagnose: 10,
  inspect: 5,
};
export const SKILL_DEFAULT_MAX_STEPS = 15;

/** Max token budget displayed in chat UI. */
export const CHAT_MAX_TOKENS = 64_000;

/** ConsolePanel max displayed entries. */
export const CONSOLE_MAX_ENTRIES = 500;

/** Max historical session badges in chat bar. */
export const CHAT_MAX_SESSION_BADGES = 8;

/** Default nodelet address placeholder. */
export const DEFAULT_NODELET_ADDRESS = "http://:8686";

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
  starting: "启动中",
  stopped: "已停止",
  error: "异常",
};
