import React from "react";
import {
  AlertTriangle,
  Bot,
  GitBranch,
  Hammer,
  MessageSquare,
  Plus,
  Send,
  Square,
  Trash2,
  User,
  Wrench,
} from "lucide-react";
import type {
  AgentEvent,
  AgentMessage,
  ContentBlock,
  SessionEntry,
  SessionInfo,
  SessionResponse,
  ToolCallContent,
  ToolDefinition,
} from "../types";
import { CHAT_MAX_SESSION_BADGES } from "../types";
import { messageKey, textFromContent, toolCallsFromMessage } from "../lib/session";
import { StreamingText } from "./StreamingText";
import { FormInput } from "./ui/FormInput";
import { Button } from "./ui/Button";

type TreeNode = {
  entry: SessionEntry;
  children: TreeNode[];
};

export function ChatView({
  session,
  chatInput,
  chatLoading,
  chatError,
  sessions,
  onInputChange,
  onSend,
  onAbort,
  onClear,
  onNewChat,
  onSelectSession,
}: {
  session: SessionResponse;
  chatInput: string;
  chatLoading: boolean;
  chatError: string;
  sessions: SessionInfo[];
  onInputChange: (value: string) => void;
  onSend: () => void;
  onAbort: () => void;
  onClear: () => void;
  onNewChat: () => void;
  onSelectSession: (id: string) => void;
}) {
  const sessionId = session.sessionId;
  const messages = session.messages || [];
  const events = session.events || [];
  const tree = React.useMemo(() => buildSessionTree(session.entries || []), [session.entries]);
  const runningTools = React.useMemo(() => currentToolStates(events), [events]);
  const otherSessions = sessions.filter((s) => s.id !== sessionId);
  const hasContent = messages.length > 0 || events.length > 0;
  const lastMessage = messages[messages.length - 1];

  return (
    <section className="chat-panel agent-runtime-panel" id="chat-section">
      <div className="chat-header">
        <div className="chat-header-left">
          <Bot size={18} />
          <span>Agent Runtime</span>
          {sessionId && <span className="runtime-session-id">{sessionId.slice(-10)}</span>}
          {chatLoading && <span className="runtime-status">运行中</span>}
        </div>
        <span className="chat-header-actions">
          {chatLoading && (
            <button onClick={onAbort} title="停止运行" aria-label="停止运行">
              <Square size={15} />
            </button>
          )}
          <button onClick={onNewChat} title="新建对话" aria-label="新建对话">
            <Plus size={16} />
          </button>
          {hasContent && (
            <button onClick={onClear} title="清空当前对话" aria-label="清空当前对话">
              <Trash2 size={16} />
            </button>
          )}
        </span>
      </div>

      {otherSessions.length > 0 && (
        <div className="chat-sessions-bar">
          <MessageSquare size={14} />
          <span className="chat-sessions-label">历史会话</span>
          {otherSessions.slice(0, CHAT_MAX_SESSION_BADGES).map((s) => (
            <button
              key={s.id}
              className="chat-session-badge"
              title={`${s.messageCount} 条消息`}
              onClick={() => onSelectSession(s.id)}
              disabled={chatLoading}
            >
              {(s.id || "").slice(-8)}
            </button>
          ))}
        </div>
      )}

      <div className="agent-runtime-body">
        <div className="chat-body agent-message-list" role="log" aria-live="polite">
          {messages.length === 0 && !chatLoading && (
            <div className="chat-empty">暂无消息</div>
          )}
          {messages.map((message, index) => (
            <AgentMessageView
              key={messageKey(message, index)}
              message={message}
              animate={chatLoading && index === messages.length - 1 && lastMessage?.role === "assistant"}
            />
          ))}
          {messages.length === 0 && chatLoading && (
            <div className="chat-msg assistant">
              <div className="chat-avatar"><Bot size={16} /></div>
              <div className="chat-content chat-thinking">运行中…</div>
            </div>
          )}
          {chatError && <div className="chat-error">{chatError}</div>}
        </div>

        <aside className="agent-runtime-sidebar" aria-label="运行状态">
          <RuntimePanel title="工具" icon={<Wrench size={15} />}>
            <ToolList tools={session.tools || []} />
          </RuntimePanel>
          <RuntimePanel title="执行中" icon={<Hammer size={15} />}>
            <ToolExecutionList states={runningTools} />
          </RuntimePanel>
          <RuntimePanel title="Session Tree" icon={<GitBranch size={15} />}>
            <SessionTree nodes={tree} activeLeafId={session.leafId || ""} />
          </RuntimePanel>
          <RuntimePanel title="事件时间线" icon={<MessageSquare size={15} />}>
            <EventTimeline events={events} />
          </RuntimePanel>
        </aside>
      </div>

      <div className="chat-footer">
        <div className="chat-footer-main">
          <FormInput
            multiline
            className="chat-input"
            placeholder="输入问题，Enter 发送，Shift+Enter 换行"
            value={chatInput}
            onChange={(e) => onInputChange(e.target.value)}
            onKeyDown={(e: React.KeyboardEvent) => {
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                e.preventDefault();
                onSend();
              }
            }}
            disabled={chatLoading}
          />
          <Button
            onClick={() => onSend()}
            disabled={chatLoading || !chatInput.trim()}
            title="发送"
            aria-label="发送消息"
          >
            <Send size={18} />
          </Button>
        </div>
      </div>
    </section>
  );
}

function AgentMessageView({ message, animate }: { message: AgentMessage; animate: boolean }) {
  if (message.role === "user") {
    return (
      <div className="chat-msg user">
        <div className="chat-avatar"><User size={16} /></div>
        <div className="chat-content">{textFromContent(message.content)}</div>
      </div>
    );
  }
  if (message.role === "toolResult") {
    return (
      <div className={`runtime-message tool-result ${message.isError ? "tool-error" : ""}`}>
        <div className="runtime-message-title">
          {message.isError ? <AlertTriangle size={14} /> : <Hammer size={14} />}
          <span>{message.toolName}</span>
        </div>
        <pre>{formatText(textFromContent(message.content))}</pre>
      </div>
    );
  }
  return (
    <div className="chat-msg assistant">
      <div className="chat-avatar"><Bot size={16} /></div>
      <div className="chat-content">
        {message.content.map((content, index) => (
          <ContentView key={`${content.type}-${index}`} content={content} animate={animate} />
        ))}
        {message.errorMessage && <div className="chat-error">{message.errorMessage}</div>}
      </div>
    </div>
  );
}

function ContentView({ content, animate }: { content: ContentBlock; animate: boolean }) {
  if (content.type === "text") {
    return <StreamingText text={content.text} animate={animate} />;
  }
  if (content.type === "thinking") {
    return <div className="runtime-thinking"><StreamingText text={content.thinking} animate={animate} /></div>;
  }
  if (content.type === "toolCall") {
    return <ToolCallView call={content} />;
  }
  if (content.url) {
    return <div className="runtime-media-link">{content.url}</div>;
  }
  return null;
}

function ToolCallView({ call }: { call: ToolCallContent }) {
  return (
    <div className="runtime-tool-call">
      <Wrench size={14} />
      <span>{call.name}</span>
      <code>{JSON.stringify(call.arguments)}</code>
    </div>
  );
}

function RuntimePanel({ title, icon, children }: { title: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="runtime-panel-section">
      <h3>{icon}<span>{title}</span></h3>
      {children}
    </section>
  );
}

function ToolList({ tools }: { tools: ToolDefinition[] }) {
  if (tools.length === 0) return <div className="runtime-empty">暂无工具</div>;
  return (
    <div className="runtime-tool-list">
      {tools.map((tool) => (
        <div key={tool.name} className="runtime-tool-item">
          <span>{tool.name}</span>
          {tool.description && <small>{tool.description}</small>}
        </div>
      ))}
    </div>
  );
}

function ToolExecutionList({ states }: { states: ToolState[] }) {
  if (states.length === 0) return <div className="runtime-empty">暂无执行</div>;
  return (
    <div className="runtime-event-list">
      {states.map((state) => (
        <div key={state.id} className={`runtime-event-row ${state.error ? "runtime-event-error" : ""}`}>
          <span>{state.name}</span>
          <small>{state.done ? (state.error ? "错误" : "完成") : "运行中"}</small>
        </div>
      ))}
    </div>
  );
}

function EventTimeline({ events }: { events: AgentEvent[] }) {
  if (events.length === 0) return <div className="runtime-empty">暂无事件</div>;
  return (
    <div className="runtime-event-list">
      {events.slice(-80).map((event, index) => (
        <div key={`${event.type}-${index}`} className="runtime-event-row">
          <span>{event.type}</span>
          <small>{eventSummary(event)}</small>
        </div>
      ))}
    </div>
  );
}

function SessionTree({ nodes, activeLeafId }: { nodes: TreeNode[]; activeLeafId: string }) {
  if (nodes.length === 0) return <div className="runtime-empty">暂无 entry</div>;
  return (
    <div className="runtime-tree">
      {nodes.map((node) => (
        <TreeNodeView key={node.entry.id || node.entry.timestamp || node.entry.type} node={node} activeLeafId={activeLeafId} />
      ))}
    </div>
  );
}

function TreeNodeView({ node, activeLeafId }: { node: TreeNode; activeLeafId: string }) {
  const id = node.entry.id || "";
  const active = activeLeafId && activeLeafId === id;
  return (
    <div className="runtime-tree-node">
      <div className={`runtime-tree-label ${active ? "active" : ""}`}>
        <span>{entryLabel(node.entry)}</span>
      </div>
      {node.children.length > 0 && (
        <div className="runtime-tree-children">
          {node.children.map((child) => (
            <TreeNodeView key={child.entry.id || child.entry.timestamp || child.entry.type} node={child} activeLeafId={activeLeafId} />
          ))}
        </div>
      )}
    </div>
  );
}

type ToolState = {
  id: string;
  name: string;
  done: boolean;
  error: boolean;
};

function currentToolStates(events: AgentEvent[]): ToolState[] {
  const states = new Map<string, ToolState>();
  for (const event of events) {
    if (event.type === "message_end" && event.message?.role === "assistant") {
      for (const call of toolCallsFromMessage(event.message)) {
        states.set(call.id, { id: call.id, name: call.name, done: false, error: false });
      }
    }
    if (event.type === "tool_execution_start") {
      states.set(event.toolCallId, { id: event.toolCallId, name: event.toolName, done: false, error: false });
    }
    if (event.type === "tool_execution_end") {
      states.set(event.toolCallId, { id: event.toolCallId, name: event.toolName, done: true, error: event.isError });
    }
  }
  return Array.from(states.values()).slice(-12);
}

function buildSessionTree(entries: SessionEntry[]): TreeNode[] {
  const nodes = new Map<string, TreeNode>();
  const roots: TreeNode[] = [];
  for (const entry of entries) {
    if (!entry.id) continue;
    nodes.set(entry.id, { entry, children: [] });
  }
  for (const node of nodes.values()) {
    const parentID = node.entry.parentId;
    if (parentID && nodes.has(parentID)) {
      nodes.get(parentID)!.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
}

function entryLabel(entry: SessionEntry): string {
  if (entry.type === "message" && entry.message) {
    return `${entry.message.role}: ${textFromContent(entry.message.content).slice(0, 36) || entry.message.role}`;
  }
  if (entry.type === "model_change") return `model: ${entry.model || ""}`;
  if (entry.type === "active_tools_change") return `tools: ${(entry.toolNames || []).join(", ")}`;
  if (entry.type === "compaction") return "compaction";
  if (entry.type === "leaf") return `leaf: ${entry.leafId || ""}`;
  return entry.type;
}

function eventSummary(event: AgentEvent): string {
  if ("turn" in event && event.turn) return `turn ${event.turn}`;
  if (event.type === "tool_execution_start" || event.type === "tool_execution_update" || event.type === "tool_execution_end") {
    return event.toolName;
  }
  if ((event.type === "message_start" || event.type === "message_update" || event.type === "message_end") && event.message) {
    return event.message.role;
  }
  return "";
}

function formatText(text: string): string {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
