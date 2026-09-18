import type {
  AgentEvent,
  AgentMessage,
  AssistantMessage,
  ContentBlock,
  SessionDetail,
  SessionInfo,
  SessionMessage,
  SessionResponse,
  ToolCallContent,
  ToolResultMessage,
} from "../types";

export function sessionInfosForTabs(
  sessions: SessionInfo[],
  activeSessionId: string,
  activeFallback?: SessionInfo,
): SessionInfo[] {
  if (!activeSessionId || sessions.some((session) => session.id === activeSessionId)) {
    return sessions;
  }
  return activeFallback ? [activeFallback, ...sessions] : sessions;
}

export const EMPTY_AGENT_SESSION: SessionResponse = {
  sessionId: "",
  leafId: "",
  messages: [],
  events: [],
  tools: [],
  entries: [],
};

export const RUN_EVENT_TYPES = [
  "agent_start",
  "agent_end",
  "turn_start",
  "turn_end",
  "message_start",
  "message_update",
  "message_end",
  "tool_execution_start",
  "tool_execution_update",
  "tool_execution_end",
  "run_done",
  "run_error",
] as const;

export function sessionFromDetail(detail: SessionDetail): SessionResponse {
  return {
    ...EMPTY_AGENT_SESSION,
    sessionId: detail.id,
    messages: agentMessagesFromDetail(detail.messages),
  };
}

type SessionResponseInput = Omit<SessionResponse, "messages" | "events" | "tools" | "entries"> & {
  messages?: SessionResponse["messages"] | null;
  events?: SessionResponse["events"] | null;
  tools?: SessionResponse["tools"] | null;
  entries?: SessionResponse["entries"] | null;
};

function normalizeSessionResponse(detail: SessionResponseInput): SessionResponse {
  return {
    ...EMPTY_AGENT_SESSION,
    ...detail,
    messages: detail.messages ?? [],
    events: detail.events ?? [],
    tools: detail.tools ?? [],
    entries: detail.entries ?? [],
  };
}

export function sessionFromAPI(detail: SessionResponseInput | SessionDetail): SessionResponse {
  if ("sessionId" in detail) {
    return normalizeSessionResponse(detail);
  }
  return sessionFromDetail(detail);
}

export function applyAgentEventToSession(session: SessionResponse, event: AgentEvent): SessionResponse {
  const next: SessionResponse = {
    ...session,
    events: [...session.events, event],
  };
  switch (event.type) {
    case "message_start":
    case "message_update":
    case "message_end":
      if (event.message) {
        next.messages = upsertMessage(next.messages, event.message);
      }
      break;
  }
  return next;
}

export function applyTerminalSnapshotToSession(
  session: SessionResponse,
  terminal: SessionResponseInput,
): SessionResponse {
  const snapshot = normalizeSessionResponse(terminal);
  return {
    ...session,
    ...snapshot,
    sessionId: snapshot.sessionId || session.sessionId,
    leafId: snapshot.leafId || session.leafId,
    // Bounded terminal snapshots omit payloads already received through the stream.
    messages: snapshot.messages.length > 0 ? snapshot.messages : session.messages,
    events: snapshot.events.length > 0 ? snapshot.events : session.events,
    tools: snapshot.tools.length > 0 ? snapshot.tools : session.tools,
    entries: snapshot.entries.length > 0 ? snapshot.entries : session.entries,
  };
}

export function textFromContent(content: ContentBlock[] | undefined): string {
  if (!content) return "";
  return content.map(contentText).filter(Boolean).join("");
}

export function toolCallsFromMessage(message: AgentMessage): ToolCallContent[] {
  if (message.role !== "assistant") return [];
  return message.content.filter((item): item is ToolCallContent => item.type === "toolCall");
}

export function messageKey(message: AgentMessage, index: number): string {
  if (message.role === "toolResult") {
    return `tool-${message.toolCallId}-${message.timestamp || index}`;
  }
  return `${message.role}-${message.timestamp || index}-${textFromContent(message.content).slice(0, 24)}`;
}

function upsertMessage(messages: AgentMessage[], message: AgentMessage): AgentMessage[] {
  const out = [...messages];
  const last = out[out.length - 1];
  if (last && sameMessageSlot(last, message)) {
    out[out.length - 1] = message;
    return out;
  }
  out.push(message);
  return out;
}

function sameMessageSlot(left: AgentMessage, right: AgentMessage): boolean {
  if (left.role !== right.role) return false;
  if (left.role === "toolResult" && right.role === "toolResult") {
    return left.toolCallId === right.toolCallId;
  }
  if (left.timestamp || right.timestamp) {
    return left.timestamp === right.timestamp;
  }
  return true;
}

function agentMessagesFromDetail(messages: SessionMessage[]): AgentMessage[] {
  const out: AgentMessage[] = [];
  const pendingToolCalls = new Map<string, ToolCallContent>();
  for (const [index, msg] of messages.entries()) {
    const timestamp = index + 1;
    if (msg.role === "user") {
      out.push({
        role: "user",
        content: [{ type: "text", text: msg.content }],
        timestamp,
      });
      continue;
    }
    if (msg.role === "tool_call") {
      const call: ToolCallContent = {
        type: "toolCall",
        id: msg.toolCallId || `msg-${out.length}`,
        name: msg.toolName || msg.content || "tool",
        arguments: parseObject(msg.toolArgs),
      };
      pendingToolCalls.set(call.id, call);
      out.push({
        role: "assistant",
        content: [call],
        usage: emptyUsage(),
        stopReason: "toolUse",
        timestamp,
      });
      continue;
    }
    if (msg.role === "tool") {
      const result: ToolResultMessage = {
        role: "toolResult",
        toolCallId: msg.toolCallId || `msg-${out.length}`,
        toolName: msg.toolName || pendingToolCalls.get(msg.toolCallId || "")?.name || "tool",
        content: [{ type: "text", text: msg.content }],
        isError: false,
        timestamp,
      };
      out.push(result);
      continue;
    }
    if (msg.role === "assistant" || msg.role === "thinking") {
      const assistant: AssistantMessage = {
        role: "assistant",
        content: [{ type: msg.role === "thinking" ? "thinking" : "text", ...(msg.role === "thinking" ? { thinking: msg.content } : { text: msg.content }) } as ContentBlock],
        usage: emptyUsage(),
        stopReason: "stop",
        timestamp,
      };
      out.push(assistant);
    }
  }
  return out;
}

function contentText(content: ContentBlock): string {
  if (content.type === "text") return content.text;
  if (content.type === "thinking") return content.thinking;
  if (content.type === "toolCall") return `${content.name} ${JSON.stringify(content.arguments)}`;
  if (content.url) return content.url;
  return "";
}

function parseObject(raw: string | undefined): Record<string, unknown> {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    return {};
  }
  return {};
}

function emptyUsage() {
  return { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0 };
}
