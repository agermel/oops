import AnsiConvertor from "ansi-to-html";
import {
  Gauge,
  TerminalSquare,
  Sparkles,
  CheckCircle2,
  XCircle,
  AlertTriangle,
} from "lucide-react";

// Connection 对应后端返回的连接配置。
export type Connection = {
  id: string;
  name: string;
  type: string;
  address: string;
};

// Result 对应后端一次健康探测的结果。
export type Result = {
  connectionId: string;
  status: "alive" | "dead" | "unknown";
  message?: string;
  latency: number;
  checkedAt: string;
};

// StatusItem 是连接状态接口的一行数据。
export type StatusItem = {
  connection: Connection;
  result: Result;
  error?: string;
};

// NodeletConfig 对应中心端配置的一台 nodelet。
export type NodeletConfig = {
  id: string;
  name: string;
  address: string;
};

// Host 对应 nodelet 返回的机器信息。
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

// NodeletItem 是中心端机器列表接口的一行数据。
export type NodeletItem = {
  nodelet: NodeletConfig;
  host: Host;
  available: boolean;
  error?: string;
};

// Container 对应某台机器上的一个容器。
export type Container = {
  id: string;
  name: string;
  image: string;
  state: string;
  health?: string;
  hostId: string;
  created: string;
  startedAt: string;
};

// LogEntry 对应一条容器日志。
export type LogEntry = {
  timestamp: string;
  containerId: string;
  stream: string;
  message: string;
  rawMessage?: string;
  level?: "fatal" | "error" | "warn" | "info" | "debug" | "trace" | "unknown";
};

// StepEvent 对应 Agent 执行过程中的单个步骤（SSE 事件）。
export type StepEvent = {
  type: "thinking" | "tool_call" | "tool_result" | "answer" | "error";
  content: string;
  toolName?: string;
  toolArgs?: string;
};

// ChatExchange 是一轮完整的对话记录（用户问题 + Agent 步骤 + 最终答案）。
export type ChatExchange = {
  question: string;
  steps: StepEvent[];
  answer?: string;
  error?: string;
};

export const navigation = [
  { id: "connections", label: "连接", icon: Gauge },
  { id: "fleet", label: "容器", icon: TerminalSquare },
  { id: "chat", label: "助手", icon: Sparkles },
] as const;

export const statusIcon = {
  alive: CheckCircle2,
  dead: XCircle,
  unknown: AlertTriangle,
};

export const MAX_LOGS = 2000;
export const LOG_FLUSH_MS = 250;
export const LOG_MAX_WAIT_MS = 1000;

export const ansiConvertor = new AnsiConvertor({
  escapeXML: true,
  fg: "#f5f7fa",
  bg: "#1f2430",
});
